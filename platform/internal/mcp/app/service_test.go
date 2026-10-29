package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// ── fakes ───────────────────────────────────────────────────────────

type fakeAuth struct {
	caller app.Caller
	err    error
}

func (f fakeAuth) Authenticate(context.Context, string) (app.Caller, error) { return f.caller, f.err }

type recordingAudit struct {
	entries []app.AuditEntry
	// tenants is the tenant each row was written under, which is what
	// row-level security will key on.
	tenants []tenancy.ID
	err     error
}

func (r *recordingAudit) Record(ctx context.Context, e app.AuditEntry) error {
	tenant, _ := tenancy.FromContext(ctx)
	r.entries = append(r.entries, e)
	r.tenants = append(r.tenants, tenant)
	return r.err
}

type recordedCall struct {
	tool    string
	toolset domain.Toolset
	outcome domain.Outcome
}

type recordingMetrics struct {
	calls   []recordedCall
	opened  int
	lastRes string
}

func (m *recordingMetrics) ToolCalled(tool string, ts domain.Toolset, o domain.Outcome) {
	m.calls = append(m.calls, recordedCall{tool, ts, o})
}

func (m *recordingMetrics) SessionOpened(transport string) { m.opened++; m.lastRes = transport }

type denyLimiter struct{}

func (denyLimiter) Allow(context.Context, string) bool { return false }

// ── helpers ─────────────────────────────────────────────────────────

func callerWith(t *testing.T, tenant tenancy.ID, scopes ...string) app.Caller {
	t.Helper()
	ss, err := identity.ParseScopes(scopes)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	token := identity.NewTokenID()
	actor := identity.TokenActor(token)
	return app.Caller{
		Tenant: tenant, Token: token, Scopes: ss,
		Principal: authz.Principal{
			Actor: actor, Tenant: tenant, TokenTenant: tenant, Grant: identity.GrantForScopes(ss),
		},
	}
}

// seenTenant is a probe tool: it answers with the tenant its context was
// scoped to, which is how a test proves a session reaches its own tenant
// and no other.
func seenTenant(name string, ts domain.Toolset, perm identity.Permission) app.Tool {
	return app.Tool{
		Name: name, Toolset: ts, Permission: perm, Selectors: []string{"locale"},
		Handler: func(ctx context.Context, sess app.Session, _ json.RawMessage) (app.Result, error) {
			tenant, ok := tenancy.FromContext(ctx)
			if !ok {
				return app.Result{}, errors.New("no tenant on the context")
			}
			if _, ok := authz.From(ctx); !ok {
				return app.Result{}, errors.New("no principal on the context")
			}
			return app.Result{
				Explanation: "ok", Data: tenant.String(), Affected: []string{sess.ID},
			}, nil
		},
	}
}

func newService(t *testing.T, caller app.Caller, audit *recordingAudit, m *recordingMetrics, opts ...app.Option) *app.Service {
	t.Helper()
	base := []app.Option{
		app.WithAudit(audit), app.WithMetrics(m),
		app.WithTools(
			seenTenant("catalog_search", domain.ToolsetRead, identity.PermCatalogRead),
			seenTenant("message_upsert", domain.ToolsetWrite, identity.PermCatalogWrite),
			seenTenant("releases_publish", domain.ToolsetRead, identity.PermReleasesPublish),
		),
	}
	svc, err := app.New(fakeAuth{caller: caller}, append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func open(t *testing.T, svc *app.Service, caller app.Caller, want domain.Toolset) app.Session {
	t.Helper()
	if err := svc.AllowToolset(caller, want); err != nil {
		t.Fatalf("AllowToolset: %v", err)
	}
	return svc.Open("streamable-http", caller, want)
}

// ── the write gate ──────────────────────────────────────────────────

func TestAllowToolset(t *testing.T) {
	tenant := tenancy.NewID()
	tests := []struct {
		name    string
		scopes  []string
		want    domain.Toolset
		wantErr error
	}{
		{name: "a read token opens a read session", scopes: []string{"read"}, want: domain.ToolsetRead},
		{name: "a read token cannot open a write session", scopes: []string{"read"},
			want: domain.ToolsetWrite, wantErr: domain.ErrWriteNotGranted},
		{name: "a write token opens a write session", scopes: []string{"read", "write"}, want: domain.ToolsetWrite},
		{name: "a write token may still open a read session", scopes: []string{"write"}, want: domain.ToolsetRead},
		{name: "publish alone does not open a write session", scopes: []string{"publish"},
			want: domain.ToolsetWrite, wantErr: domain.ErrWriteNotGranted},
		{name: "admin does not open a write session", scopes: []string{"admin"},
			want: domain.ToolsetWrite, wantErr: domain.ErrWriteNotGranted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			caller := callerWith(t, tenant, tc.scopes...)
			svc := newService(t, caller, &recordingAudit{}, &recordingMetrics{})
			err := svc.AllowToolset(caller, tc.want)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("AllowToolset = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// The second lock: even a token that carries `write` cannot write
// through a session that did not ask for the write toolset.
func TestReadSessionCannotCallAWriteTool(t *testing.T) {
	caller := callerWith(t, tenancy.NewID(), "read", "write")
	audit, metrics := &recordingAudit{}, &recordingMetrics{}
	svc := newService(t, caller, audit, metrics)

	sess := open(t, svc, caller, domain.ToolsetRead)
	if _, err := svc.Call(t.Context(), sess, "message_upsert", nil); !errors.Is(err, domain.ErrToolNotInSession) {
		t.Fatalf("err = %v, want ErrToolNotInSession", err)
	}
	// It is not even offered.
	for _, tool := range svc.Tools(domain.ToolsetRead) {
		if tool.Name == "message_upsert" {
			t.Error("a read session lists the write tool")
		}
	}
	if len(metrics.calls) != 1 || metrics.calls[0].outcome != domain.OutcomeDenied {
		t.Errorf("metrics = %+v, want one denied call", metrics.calls)
	}
	if len(audit.entries) != 1 || audit.entries[0].Outcome != domain.OutcomeDenied {
		t.Errorf("audit = %+v, want one denied row", audit.entries)
	}
}

func TestWriteSessionCallsAWriteTool(t *testing.T) {
	tenant := tenancy.NewID()
	caller := callerWith(t, tenant, "read", "write")
	audit, metrics := &recordingAudit{}, &recordingMetrics{}
	svc := newService(t, caller, audit, metrics)

	sess := open(t, svc, caller, domain.ToolsetWrite)
	res, err := svc.Call(t.Context(), sess, "message_upsert", json.RawMessage(`{"locale":"de"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.Data != tenant.String() {
		t.Errorf("the tool saw tenant %v, want %s", res.Data, tenant)
	}
	if len(metrics.calls) != 1 || metrics.calls[0].outcome != domain.OutcomeOK ||
		metrics.calls[0].toolset != domain.ToolsetWrite {
		t.Errorf("metrics = %+v", metrics.calls)
	}
}

// ── authorization ───────────────────────────────────────────────────

// A write session on a token without the permission a tool needs is
// still refused: the toolset is not a grant.
func TestPermissionIsStillChecked(t *testing.T) {
	caller := callerWith(t, tenancy.NewID(), "read", "write")
	audit, metrics := &recordingAudit{}, &recordingMetrics{}
	svc := newService(t, caller, audit, metrics)

	sess := open(t, svc, caller, domain.ToolsetWrite)
	_, err := svc.Call(t.Context(), sess, "releases_publish", nil)
	if !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("err = %v, want forbidden", err)
	}
	if audit.entries[0].Outcome != domain.OutcomeDenied {
		t.Errorf("outcome = %q, want denied", audit.entries[0].Outcome)
	}
}

func TestUnknownToolIsInvalid(t *testing.T) {
	caller := callerWith(t, tenancy.NewID(), "read")
	audit, metrics := &recordingAudit{}, &recordingMetrics{}
	svc := newService(t, caller, audit, metrics)

	sess := open(t, svc, caller, domain.ToolsetRead)
	if _, err := svc.Call(t.Context(), sess, "tenant_delete", nil); !errors.Is(err, domain.ErrToolNotFound) {
		t.Fatalf("err = %v, want ErrToolNotFound", err)
	}
	if audit.entries[0].Outcome != domain.OutcomeInvalid {
		t.Errorf("outcome = %q, want invalid", audit.entries[0].Outcome)
	}
}

func TestRateLimitIsAWall(t *testing.T) {
	caller := callerWith(t, tenancy.NewID(), "read")
	audit, metrics := &recordingAudit{}, &recordingMetrics{}
	svc := newService(t, caller, audit, metrics, app.WithLimiter(denyLimiter{}))

	sess := open(t, svc, caller, domain.ToolsetRead)
	if _, err := svc.Call(t.Context(), sess, "catalog_search", nil); !errors.Is(err, app.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if audit.entries[0].Outcome != domain.OutcomeDenied {
		t.Errorf("outcome = %q, want denied", audit.entries[0].Outcome)
	}
}

// ── tenant isolation ────────────────────────────────────────────────

// Two sessions, two tenants, one service: each tool call runs in its own
// session's tenant, and so does its audit row.
func TestTenantIsolation(t *testing.T) {
	first, second := tenancy.NewID(), tenancy.NewID()
	callerA, callerB := callerWith(t, first, "read"), callerWith(t, second, "read")
	audit, metrics := &recordingAudit{}, &recordingMetrics{}
	svc := newService(t, callerA, audit, metrics)

	sessA := open(t, svc, callerA, domain.ToolsetRead)
	sessB := open(t, svc, callerB, domain.ToolsetRead)

	for _, tc := range []struct {
		sess app.Session
		want tenancy.ID
	}{{sessA, first}, {sessB, second}} {
		res, err := svc.Call(t.Context(), tc.sess, "catalog_search", nil)
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if res.Data != tc.want.String() {
			t.Errorf("the tool saw %v, want %s", res.Data, tc.want)
		}
	}
	if audit.tenants[0] != first || audit.tenants[1] != second {
		t.Errorf("audit rows were written under %v, want %s then %s", audit.tenants, first, second)
	}
	// A session never borrows another's tenant, however the service is
	// shared.
	if sessA.Tenant == sessB.Tenant || sessA.ID == sessB.ID {
		t.Error("the two sessions are not distinct")
	}
	if metrics.opened != 2 {
		t.Errorf("sessions opened = %d, want 2", metrics.opened)
	}
}

// ── the audit row ───────────────────────────────────────────────────

func TestAuditRecordsShapeNotContent(t *testing.T) {
	tenant := tenancy.NewID()
	caller := callerWith(t, tenant, "read", "write")
	audit, metrics := &recordingAudit{}, &recordingMetrics{}
	svc := newService(t, caller, audit, metrics)

	sess := open(t, svc, caller, domain.ToolsetWrite)
	args := json.RawMessage(`{"locale":"de","source":"You have {count} unread messages"}`)
	if _, err := svc.Call(t.Context(), sess, "message_upsert", args); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(audit.entries))
	}
	e := audit.entries[0]
	switch {
	case e.Tool != "message_upsert":
		t.Errorf("tool = %q", e.Tool)
	case e.Outcome != domain.OutcomeOK:
		t.Errorf("outcome = %q", e.Outcome)
	case e.Actor != caller.Principal.Actor || e.Token != caller.Token:
		t.Errorf("actor = %v / token = %v", e.Actor, e.Token)
	case e.Session != sess.ID:
		t.Errorf("session = %q, want %q", e.Session, sess.ID)
	case e.Toolset != domain.ToolsetWrite:
		t.Errorf("toolset = %q", e.Toolset)
	case len(e.Affected) != 1:
		t.Errorf("affected = %v, want the id the tool reported", e.Affected)
	}
	want := map[string]string{"locale": "de", "source": "string(len=32)"}
	if !maps.Equal(e.Arguments, want) {
		t.Errorf("arguments = %v, want %v", e.Arguments, want)
	}
	if audit.tenants[0] != tenant {
		t.Errorf("the row was written under %s, want %s", audit.tenants[0], tenant)
	}
}

// A ledger that is down must not swallow a good answer, and must not
// turn one into an error either.
func TestAuditFailureDoesNotFailTheCall(t *testing.T) {
	caller := callerWith(t, tenancy.NewID(), "read")
	audit := &recordingAudit{err: errors.New("the ledger is unreachable")}
	svc := newService(t, caller, audit, &recordingMetrics{})

	sess := open(t, svc, caller, domain.ToolsetRead)
	if _, err := svc.Call(t.Context(), sess, "catalog_search", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
}

// The ledger and the tenant's audit trail (RFC 0006 §6.1) are both
// written through WithAudit, the same row to each, and one failing does
// not keep the other from being written.
func TestEveryAuditIsWritten(t *testing.T) {
	tenant := tenancy.NewID()
	caller := callerWith(t, tenant, "read")
	ledger := &recordingAudit{err: errors.New("the ledger is unreachable")}
	trail := &recordingAudit{}
	svc := newService(t, caller, ledger, &recordingMetrics{}, app.WithAudit(trail))

	sess := open(t, svc, caller, domain.ToolsetRead)
	if _, err := svc.Call(t.Context(), sess, "catalog_search", json.RawMessage(`{"locale":"de"}`)); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(ledger.entries) != 1 || len(trail.entries) != 1 {
		t.Fatalf("ledger %d rows, trail %d; want one each", len(ledger.entries), len(trail.entries))
	}
	if ledger.entries[0].ID != trail.entries[0].ID || trail.tenants[0] != tenant {
		t.Errorf("the trail did not get the ledger's row in the session's tenant: %+v", trail.entries[0])
	}
}

// ── the capability probe ────────────────────────────────────────────

func TestWhoAmI(t *testing.T) {
	tenant := tenancy.NewID()
	caller := callerWith(t, tenant, "read")
	svc := newService(t, caller, &recordingAudit{}, &recordingMetrics{})

	sess := open(t, svc, caller, domain.ToolsetRead)
	res, err := svc.Call(t.Context(), sess, app.WhoAmIName, nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	id, ok := res.Data.(app.Identity)
	if !ok {
		t.Fatalf("data = %T, want app.Identity", res.Data)
	}
	switch {
	case id.Tenant != tenant.String():
		t.Errorf("tenant = %q, want %s", id.Tenant, tenant)
	case id.Toolset != string(domain.ToolsetRead):
		t.Errorf("toolset = %q", id.Toolset)
	case id.WriteAvailable:
		t.Error("a read-only token reports that a write session is available")
	case len(id.Tools) == 0:
		t.Error("no tools reported")
	case res.Explanation == "":
		t.Error("no explanation beside the payload")
	}
	// The probe never reports a permission the token does not hold.
	for _, p := range id.Permissions {
		if identity.Permission(p) == identity.PermTranslationsReview {
			t.Error("review is in no scope's permissions and must not be reported")
		}
	}
}

func TestWhoAmIIsAlwaysRegistered(t *testing.T) {
	svc, err := app.New(fakeAuth{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, ts := range []domain.Toolset{domain.ToolsetRead, domain.ToolsetWrite} {
		tools := svc.Tools(ts)
		if len(tools) != 1 || tools[0].Name != app.WhoAmIName {
			t.Errorf("%s toolset = %v, want only the capability probe", ts, tools)
		}
	}
}
