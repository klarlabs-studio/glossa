package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// releaseTools are the three release tools of RFC 0005 §7.3, in the
// order Publish registers them.
var releaseTools = []string{
	tools.ReleasePublishName, tools.ReleasePromoteName, tools.ReleaseRollbackName,
}

// ── the Release context, faked ──────────────────────────────────────

// fakeReleases stands in for Release's application service. Like every
// other fake here it reads the tenant from the context, which is what
// row-level security does: another tenant's project is not refused, it
// is not there.
//
// It also stands in for the publish gate another wave-5 slice adds to
// the Release context: gate, when set, is the error Publish answers
// with, so a test can prove the tool passes a refusal through verbatim
// rather than deciding anything about it.
type fakeReleases struct {
	owner map[uuid.UUID]tenancy.ID
	// serving is environment → the release it currently serves.
	serving map[string]uuid.UUID
	// history is the releases an environment served before, newest last.
	history map[string][]uuid.UUID
	// published, promoted and rolled back record what reached the
	// context, so a test asserts the call and not only the answer.
	published []tools.PublishRequest
	promoted  []uuid.UUID
	rolledTo  []uuid.UUID
	// gate is the error Publish answers with, standing in for the
	// environment's completeness requirement.
	gate error
	// held, when set, makes every publish and promote a release request
	// (RFC 0006 §5.1): the release is recorded, nothing moves.
	held *tools.Held
	// version counts the releases this fake has published.
	version int
}

func (f *fakeReleases) release(id uuid.UUID, environment string, version int) tools.Release {
	return tools.Release{
		ID: id.String(), Version: version, Environment: environment,
		Digest: "sha256:0123456789abcdef", Policy: []string{"approved"},
		Locales: []string{"en", "de"}, Messages: 42, CreatedAt: "2026-09-30T10:00:00Z",
	}
}

func (f *fakeReleases) Publish(
	ctx context.Context, project uuid.UUID, in tools.PublishRequest,
) (tools.Published, error) {
	if owner, ok := f.owner[project]; !ok || !scoped(ctx, owner) {
		return tools.Published{}, tools.ErrNotFound
	}
	if f.gate != nil {
		return tools.Published{}, f.gate
	}
	f.published = append(f.published, in)
	f.version++
	id := uuid.New()
	if f.held != nil {
		return tools.Published{Release: f.release(id, in.Environment, f.version), Held: f.held}, nil
	}
	f.history[in.Environment] = append(f.history[in.Environment], id)
	f.serving[in.Environment] = id
	return tools.Published{Release: f.release(id, in.Environment, f.version)}, nil
}

func (f *fakeReleases) Promote(
	ctx context.Context, project uuid.UUID, environment string, id uuid.UUID,
) (tools.Deployed, error) {
	if owner, ok := f.owner[project]; !ok || !scoped(ctx, owner) {
		return tools.Deployed{}, tools.ErrNotFound
	}
	f.promoted = append(f.promoted, id)
	if f.held != nil {
		return tools.Deployed{Environment: environment, Release: f.release(id, "staging", 7), Held: f.held}, nil
	}
	moved := f.serving[environment] != id
	f.serving[environment] = id
	return tools.Deployed{Environment: environment, Release: f.release(id, "staging", 7), Moved: moved}, nil
}

func (f *fakeReleases) Rollback(
	ctx context.Context, project uuid.UUID, environment string, id uuid.UUID,
) (tools.Deployed, error) {
	if owner, ok := f.owner[project]; !ok || !scoped(ctx, owner) {
		return tools.Deployed{}, tools.ErrNotFound
	}
	if id == uuid.Nil {
		past := f.history[environment]
		if len(past) < 2 {
			return tools.Deployed{}, errors.New("release: the environment has no earlier release")
		}
		id = past[len(past)-2]
	}
	f.rolledTo = append(f.rolledTo, id)
	moved := f.serving[environment] != id
	f.serving[environment] = id
	return tools.Deployed{Environment: environment, Release: f.release(id, environment, 6), Moved: moved}, nil
}

// seedReleases adds the release port to a seeded world, owned by its
// tenant, with the second tenant's project beside it.
func seedReleases(w *world) *fakeReleases {
	r := &fakeReleases{
		owner:   map[uuid.UUID]tenancy.ID{w.project: w.tenant, w.otherProject: w.other},
		serving: map[string]uuid.UUID{},
		history: map[string][]uuid.UUID{},
	}
	w.sources.Releases = r
	return r
}

// releaseArgs are valid arguments for each release tool, so a gate test
// proves a refusal came from the gate and not from a bad argument.
func releaseArgs(project uuid.UUID) map[string]map[string]any {
	release := uuid.New().String()
	return map[string]map[string]any{
		tools.ReleasePublishName: {"project": project.String(), "environment": "staging"},
		tools.ReleasePromoteName: {
			"project": project.String(), "environment": "production", "release": release,
		},
		tools.ReleaseRollbackName: {"project": project.String(), "environment": "production"},
	}
}

// publishSession opens a session with an explicit toolset, which the
// two-lock tests need: the toolset a client asks for and the scopes its
// token carries are independent, and every interesting case is one
// where they disagree.
func publishSession(
	t *testing.T, w *world, audit *recordingAudit, ts domain.Toolset, scopes ...string,
) (*app.Service, app.Session) {
	t.Helper()
	ss, err := identity.ParseScopes(scopes)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	token := identity.NewTokenID()
	caller := app.Caller{
		Tenant: w.tenant, Token: token, Scopes: ss,
		Principal: authz.Principal{
			Actor: identity.TokenActor(token), Tenant: w.tenant, TokenTenant: w.tenant,
			Grant: identity.GrantForScopes(ss),
		},
	}
	opts := []app.Option{app.WithTools(tools.All(w.sources)...)}
	if audit != nil {
		opts = append(opts, app.WithAudit(audit))
	}
	svc, err := app.New(fakeAuth{caller: caller}, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, svc.Open("streamable-http", caller, ts)
}

// ── registration ────────────────────────────────────────────────────

func TestPublishRegistersTheThreeReleaseToolsInThePublishToolset(t *testing.T) {
	w := seed()
	seedReleases(w)

	got := tools.Publish(w.sources)
	names := make([]string, len(got))
	for i, tool := range got {
		names[i] = tool.Name
		if tool.Toolset != domain.ToolsetPublish {
			t.Errorf("%s is in the %s toolset, want publish", tool.Name, tool.Toolset)
		}
		if tool.ReadOnly {
			t.Errorf("%s is marked read-only; it changes what an environment serves", tool.Name)
		}
		if tool.Permission != identity.PermReleasesPublish {
			t.Errorf("%s checks %q, want %q", tool.Name, tool.Permission, identity.PermReleasesPublish)
		}
		if tool.Description == "" || tool.Title == "" {
			t.Errorf("%s has no description or title", tool.Name)
		}
	}
	if !slices.Equal(names, releaseTools) {
		t.Fatalf("tools = %v, want %v", names, releaseTools)
	}
}

func TestPublishSkipsItsToolsWithoutThePort(t *testing.T) {
	if got := tools.Publish(tools.Sources{}); len(got) != 0 {
		t.Fatalf("tools = %d, want none", len(got))
	}
}

// The release tools reach a session through All, and a publish session
// is offered exactly the read tools and these three: not the write
// tools, because `publish` and `write` are different scopes and
// different toolsets.
func TestAPublishSessionSeesTheReadToolsAndTheReleaseToolsOnly(t *testing.T) {
	w := seed()
	seedReleases(w)
	svc, _ := publishSession(t, w, nil, domain.ToolsetPublish, "read", "publish")

	var names []string
	for _, tool := range svc.Tools(domain.ToolsetPublish) {
		names = append(names, tool.Name)
	}
	for _, want := range releaseTools {
		if !slices.Contains(names, want) {
			t.Errorf("a publish session is not offered %s", want)
		}
	}
	for _, unwanted := range writeTools {
		if slices.Contains(names, unwanted) {
			t.Errorf("a publish session is offered %s, a write tool", unwanted)
		}
	}
	for _, want := range readTools {
		if !slices.Contains(names, want) {
			t.Errorf("a publish session is not offered the read tool %s", want)
		}
	}
	// And the other way round: a write session is offered none of them.
	var write []string
	for _, tool := range svc.Tools(domain.ToolsetWrite) {
		write = append(write, tool.Name)
	}
	for _, unwanted := range releaseTools {
		if slices.Contains(write, unwanted) {
			t.Errorf("a write session is offered %s, a release tool", unwanted)
		}
	}
}

// ── the two locks, on publish ───────────────────────────────────────

// The first lock. A token without the publish scope cannot open a
// publish session at all — and `write` is not a substitute: the scopes
// are orthogonal, and a write token carries no PermReleasesPublish.
func TestOnlyAPublishTokenCanOpenAPublishSession(t *testing.T) {
	w := seed()
	seedReleases(w)
	svc, _ := publishSession(t, w, nil, domain.ToolsetRead, "read")

	for _, scopes := range [][]string{{"read"}, {"read", "write"}} {
		if err := svc.AllowToolset(callerWith(t, w.tenant, scopes...), domain.ToolsetPublish); !errors.Is(err, domain.ErrPublishNotGranted) {
			t.Errorf("%v: err = %v, want ErrPublishNotGranted", scopes, err)
		}
	}
	if err := svc.AllowToolset(callerWith(t, w.tenant, "read", "publish"), domain.ToolsetPublish); err != nil {
		t.Fatalf("a publish token was refused a publish session: %v", err)
	}
	// And a publish token is not thereby a write token.
	if err := svc.AllowToolset(callerWith(t, w.tenant, "read", "publish"), domain.ToolsetWrite); !errors.Is(err, domain.ErrWriteNotGranted) {
		t.Errorf("a publish token opened a write session: %v", err)
	}
}

// A write-only token cannot use a release tool, whichever session it
// manages to open. In a write session the toolset gate stops it; if it
// could somehow open a publish session, AllowToolset stops that first.
func TestAWriteOnlyTokenCannotUseTheReleaseTools(t *testing.T) {
	w := seed()
	r := seedReleases(w)
	audit := &recordingAudit{}
	svc, sess := publishSession(t, w, audit, domain.ToolsetWrite, "read", "write")

	args := releaseArgs(w.project)
	for _, name := range releaseTools {
		if _, err := call(t, svc, sess, name, args[name]); !errors.Is(err, domain.ErrToolNotInSession) {
			t.Errorf("%s: err = %v, want ErrToolNotInSession", name, err)
		}
	}
	if len(r.published) != 0 || len(r.promoted) != 0 || len(r.rolledTo) != 0 {
		t.Fatal("a refused call still reached the Release context")
	}
	for _, e := range audit.entries {
		if e.Outcome != domain.OutcomeDenied {
			t.Errorf("%s was audited as %s, want denied", e.Tool, e.Outcome)
		}
	}
	if len(audit.entries) != len(releaseTools) {
		t.Fatalf("audited %d refusals, want %d", len(audit.entries), len(releaseTools))
	}
}

// The second lock, and the one that is easy to forget: a token that
// *does* carry publish, in a session that opened the read toolset,
// still cannot publish. The refusal is audited like any other call.
func TestAPublishTokenInAReadSessionStillCannotPublish(t *testing.T) {
	w := seed()
	r := seedReleases(w)
	audit := &recordingAudit{}
	svc, sess := publishSession(t, w, audit, domain.ToolsetRead, "read", "publish")

	args := releaseArgs(w.project)
	for _, name := range releaseTools {
		if _, err := call(t, svc, sess, name, args[name]); !errors.Is(err, domain.ErrToolNotInSession) {
			t.Errorf("%s: err = %v, want ErrToolNotInSession", name, err)
		}
	}
	if len(r.published) != 0 || len(r.promoted) != 0 || len(r.rolledTo) != 0 {
		t.Fatal("a release operation got through a read session")
	}
	for _, e := range audit.entries {
		if e.Outcome != domain.OutcomeDenied {
			t.Errorf("%s was audited as %s, want denied", e.Tool, e.Outcome)
		}
		if e.Toolset != domain.ToolsetRead {
			t.Errorf("%s was audited in the %s toolset, want read", e.Tool, e.Toolset)
		}
	}
}

// A publish session on a token whose grant lacks the permission is
// refused by authz, not by the tool: the same check the REST endpoint
// makes. (A token cannot normally reach here, since AllowToolset stops
// it; this proves the tool would still be refused if it did.)
func TestAReleaseToolChecksThePermissionTheEndpointChecks(t *testing.T) {
	w := seed()
	r := seedReleases(w)
	audit := &recordingAudit{}
	svc, sess := publishSession(t, w, audit, domain.ToolsetPublish, "read")

	args := releaseArgs(w.project)
	for _, name := range releaseTools {
		if _, err := call(t, svc, sess, name, args[name]); !errors.Is(err, authz.ErrForbidden) {
			t.Errorf("%s: err = %v, want ErrForbidden", name, err)
		}
	}
	if len(r.published) != 0 || len(r.promoted) != 0 || len(r.rolledTo) != 0 {
		t.Fatal("a call the grant forbids still reached the Release context")
	}
	for _, e := range audit.entries {
		if e.Outcome != domain.OutcomeDenied {
			t.Errorf("%s was audited as %s, want denied", e.Tool, e.Outcome)
		}
	}
}

// Both locks open: a publish token in a publish session publishes,
// promotes and rolls back, and each call reaches the Release context.
func TestBothLocksOpenLetTheReleaseThrough(t *testing.T) {
	w := seed()
	r := seedReleases(w)
	audit := &recordingAudit{}
	svc, sess := publishSession(t, w, audit, domain.ToolsetPublish, "read", "publish")

	pub, err := call(t, svc, sess, tools.ReleasePublishName, map[string]any{
		"project": w.project.String(), "environment": "staging",
		"note": "the checkout copy", "idempotency_key": "agent-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, ok := pub.Data.(tools.Published)
	if !ok {
		t.Fatalf("data = %T, want a Published", pub.Data)
	}
	if out.Release.Version != 1 || out.Replayed {
		t.Fatalf("release = %+v", out)
	}
	if !slices.Equal(pub.Affected, []string{out.Release.ID}) {
		t.Errorf("affected = %v, want the release's id", pub.Affected)
	}
	if len(r.published) != 1 || r.published[0].Environment != "staging" ||
		r.published[0].Note != "the checkout copy" || r.published[0].IdempotencyKey != "agent-1" {
		t.Fatalf("publish request = %+v", r.published)
	}

	// Publish a second one so the environment has something to roll back
	// to, then promote it into production and roll production back.
	if _, err := call(t, svc, sess, tools.ReleasePublishName, map[string]any{
		"project": w.project.String(), "environment": "staging",
	}); err != nil {
		t.Fatal(err)
	}
	second := r.serving["staging"]
	prom, err := call(t, svc, sess, tools.ReleasePromoteName, map[string]any{
		"project": w.project.String(), "environment": "production", "release": second.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, ok := prom.Data.(tools.Deployed)
	if !ok {
		t.Fatalf("data = %T, want a Deployed", prom.Data)
	}
	if !dep.Moved || dep.Environment != "production" {
		t.Fatalf("deployed = %+v", dep)
	}
	if !slices.Equal(r.promoted, []uuid.UUID{second}) {
		t.Fatalf("promoted = %v, want %v", r.promoted, second)
	}

	if _, err := call(t, svc, sess, tools.ReleaseRollbackName, map[string]any{
		"project": w.project.String(), "environment": "staging",
	}); err != nil {
		t.Fatal(err)
	}
	if len(r.rolledTo) != 1 {
		t.Fatalf("rolled back %d times, want 1", len(r.rolledTo))
	}
	for _, e := range audit.entries {
		if e.Outcome != domain.OutcomeOK {
			t.Errorf("%s was audited as %s, want ok", e.Tool, e.Outcome)
		}
		if e.Toolset != domain.ToolsetPublish {
			t.Errorf("%s was audited in the %s toolset, want publish", e.Tool, e.Toolset)
		}
	}
}

// Promoting the release an environment already serves changes nothing,
// and the answer says so rather than reporting a move that never
// happened.
func TestPromotingWhatIsAlreadyServedSaysNothingMoved(t *testing.T) {
	w := seed()
	seedReleases(w)
	svc, sess := publishSession(t, w, nil, domain.ToolsetPublish, "read", "publish")

	id := uuid.New()
	for i := range 2 {
		res, err := call(t, svc, sess, tools.ReleasePromoteName, map[string]any{
			"project": w.project.String(), "environment": "production", "release": id.String(),
		})
		if err != nil {
			t.Fatal(err)
		}
		dep := res.Data.(tools.Deployed)
		if i == 0 && !dep.Moved {
			t.Fatal("the first promote did not move the pointer")
		}
		if i == 1 {
			if dep.Moved {
				t.Fatal("promoting the served release moved the pointer")
			}
			if !strings.Contains(res.Explanation, "nothing moved") {
				t.Errorf("explanation = %q, want it to say nothing moved", res.Explanation)
			}
		}
	}
}

// ── the publish gate belongs to the Release context ─────────────────

// The gate is another slice's, and this one must inherit it rather than
// re-decide it: whatever the context refuses with comes back unchanged,
// and nothing about the refusal is the tool's to soften.
func TestThePublishGatesRefusalIsPassedThrough(t *testing.T) {
	w := seed()
	r := seedReleases(w)
	r.gate = errors.New("release: policy_not_met: fr is 62% complete, production requires 100%")
	audit := &recordingAudit{}
	svc, sess := publishSession(t, w, audit, domain.ToolsetPublish, "read", "publish")

	_, err := call(t, svc, sess, tools.ReleasePublishName, map[string]any{
		"project": w.project.String(), "environment": "production",
	})
	if err == nil || !strings.Contains(err.Error(), "policy_not_met") {
		t.Fatalf("err = %v, want the context's policy_not_met", err)
	}
	if len(r.published) != 0 {
		t.Fatal("a gated publish was recorded as published")
	}
	if len(audit.entries) != 1 || audit.entries[0].Outcome != domain.OutcomeError {
		t.Fatalf("audit = %+v", audit.entries)
	}
}

// There is no way to force a publish past its environment's policy: not
// an argument that is refused — an argument that does not exist, in the
// schema and in the port. Forcing takes a person and a recorded reason,
// for the same reason a translation an agent writes enters review.
func TestReleasePublishOffersNoWayToForceTheGate(t *testing.T) {
	w := seed()
	seedReleases(w)

	for _, tool := range tools.Publish(w.sources) {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("%s schema: %v", tool.Name, err)
		}
		for name := range schema.Properties {
			for _, forbidden := range []string{"force", "override", "reason", "skip", "ignore"} {
				if strings.Contains(name, forbidden) {
					t.Errorf("%s declares a %q argument", tool.Name, name)
				}
			}
		}
	}
	svc, sess := publishSession(t, w, nil, domain.ToolsetPublish, "read", "publish")
	_, err := call(t, svc, sess, tools.ReleasePublishName, map[string]any{
		"project": w.project.String(), "environment": "production", "force": true,
	})
	var invalid *app.InvalidArgumentError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want an invalid-argument error", err)
	}
}

// ── nothing is destroyed ────────────────────────────────────────────

// A release is immutable and a rollback only moves a pointer. No tool
// in any toolset offers a way to delete a release, an environment or a
// deployment (RFC 0005 §7.2).
func TestNoReleaseToolDestroysAnything(t *testing.T) {
	w := seed()
	seedReleases(w)

	for _, tool := range tools.All(w.sources) {
		for _, word := range []string{"delete", "remove", "destroy", "purge", "revoke", "rename"} {
			if strings.Contains(tool.Name, word) {
				t.Errorf("%s exists", tool.Name)
			}
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			continue
		}
		for name := range schema.Properties {
			for _, word := range []string{"delete", "destroy", "purge"} {
				if strings.Contains(name, word) {
					t.Errorf("%s declares a %q argument", tool.Name, name)
				}
			}
		}
	}
	// And the port itself offers none: a Releases implementation has
	// exactly three methods, so an adapter cannot quietly add a fourth
	// that a tool could reach.
	var port tools.Releases = &fakeReleases{}
	if _, ok := port.(interface {
		Delete(context.Context, uuid.UUID, uuid.UUID) error
	}); ok {
		t.Fatal("the Releases port offers a delete")
	}
}

// ── tenancy ─────────────────────────────────────────────────────────

// A publish token sees and moves nothing of a second tenant's project.
// The id is well formed and the arguments are valid: what stops it is
// that the project is not there, which is the answer a typo gets.
func TestReleasesNeverCrossTenants(t *testing.T) {
	w := seed()
	r := seedReleases(w)
	svc, sess := publishSession(t, w, nil, domain.ToolsetPublish, "read", "publish")

	args := releaseArgs(w.otherProject)
	for _, name := range releaseTools {
		if _, err := call(t, svc, sess, name, args[name]); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if len(r.published) != 0 || len(r.promoted) != 0 || len(r.rolledTo) != 0 {
		t.Fatal("a cross-tenant call reached the Release context")
	}
}

// ── arguments ───────────────────────────────────────────────────────

func TestReleaseToolsRefuseArgumentsTheyCannotUse(t *testing.T) {
	w := seed()
	r := seedReleases(w)
	svc, sess := publishSession(t, w, nil, domain.ToolsetPublish, "read", "publish")

	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"no project", tools.ReleasePublishName, map[string]any{"environment": "staging"}},
		{"no environment", tools.ReleasePublishName, map[string]any{"project": w.project.String()}},
		{"a note longer than the context allows", tools.ReleasePublishName, map[string]any{
			"project": w.project.String(), "environment": "staging",
			"note": strings.Repeat("x", tools.MaxNoteRunes+1),
		}},
		{"no release to promote", tools.ReleasePromoteName, map[string]any{
			"project": w.project.String(), "environment": "production",
		}},
		{"a release that is not an id", tools.ReleasePromoteName, map[string]any{
			"project": w.project.String(), "environment": "production", "release": "the latest",
		}},
		{"a rollback target that is not an id", tools.ReleaseRollbackName, map[string]any{
			"project": w.project.String(), "environment": "production", "release": "yesterday",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := call(t, svc, sess, tc.tool, tc.args)
			var invalid *app.InvalidArgumentError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want an invalid-argument error", err)
			}
		})
	}
	if len(r.published) != 0 || len(r.promoted) != 0 || len(r.rolledTo) != 0 {
		t.Fatal("an invalid call reached the Release context")
	}
}

// The ledger records a release call's selectors verbatim and its note
// as a length: a note is free text a person wrote (RFC 0005 §11).
func TestAReleaseCallIsAuditedWithoutItsNote(t *testing.T) {
	w := seed()
	seedReleases(w)
	audit := &recordingAudit{}
	svc, sess := publishSession(t, w, audit, domain.ToolsetPublish, "read", "publish")

	if _, err := call(t, svc, sess, tools.ReleasePublishName, map[string]any{
		"project": w.project.String(), "environment": "staging",
		"note": "shipping the new checkout copy", "idempotency_key": "agent-1",
	}); err != nil {
		t.Fatal(err)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("audited %d calls, want 1", len(audit.entries))
	}
	got := audit.entries[0].Arguments
	if got["environment"] != "staging" {
		t.Errorf("environment = %q, want it recorded verbatim", got["environment"])
	}
	if got["idempotency_key"] != "agent-1" {
		t.Errorf("idempotency_key = %q, want it recorded verbatim", got["idempotency_key"])
	}
	if !strings.HasPrefix(got["note"], "string(len=") {
		t.Errorf("note = %q, want only its length", got["note"])
	}
}

// ── release approvals (RFC 0006 §5.1) ───────────────────────────────

// A publish or promote held for approval moved nothing, and an agent
// must be told exactly that: never "published to production" about a
// release production does not serve.
func TestAHeldPublishOrPromoteIsReportedAsHeldNeverAsDeployed(t *testing.T) {
	w := seed()
	r := seedReleases(w)
	r.held = &tools.Held{RequestID: uuid.NewString(), Environment: "production", Approvals: 2}
	svc, sess := publishSession(t, w, &recordingAudit{}, domain.ToolsetPublish, "read", "publish")

	res, err := call(t, svc, sess, tools.ReleasePublishName, map[string]any{
		"project": w.project.String(), "environment": "production",
	})
	if err != nil {
		t.Fatal(err)
	}
	pub, ok := res.Data.(tools.Published)
	if !ok || pub.Held == nil || pub.Held.RequestID != r.held.RequestID {
		t.Fatalf("data = %#v, want the held request", res.Data)
	}
	for _, want := range []string{"NOT deployed", r.held.RequestID, "2 people"} {
		if !strings.Contains(res.Explanation, want) {
			t.Errorf("publish explanation %q lacks %q", res.Explanation, want)
		}
	}
	if strings.Contains(res.Explanation, "was published to") {
		t.Errorf("a held publish reads as deployed: %q", res.Explanation)
	}

	before := r.serving["production"]
	res, err = call(t, svc, sess, tools.ReleasePromoteName, map[string]any{
		"project": w.project.String(), "environment": "production", "release": uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, ok := res.Data.(tools.Deployed)
	if !ok || dep.Held == nil || dep.Moved {
		t.Fatalf("data = %#v, want a held promote that moved nothing", res.Data)
	}
	if !strings.Contains(res.Explanation, "NOT deployed") || strings.Contains(res.Explanation, "was promoted to") {
		t.Errorf("promote explanation = %q", res.Explanation)
	}
	if r.serving["production"] != before {
		t.Errorf("a held promote moved the pointer: %v", r.serving)
	}
}
