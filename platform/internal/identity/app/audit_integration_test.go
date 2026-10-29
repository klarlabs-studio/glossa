//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"testing"

	auditpg "go.klarlabs.de/glossa/platform/internal/audit/adapters/postgres"
	auditapp "go.klarlabs.de/glossa/platform/internal/audit/app"
	auditdomain "go.klarlabs.de/glossa/platform/internal/audit/domain"
	identityaudit "go.klarlabs.de/glossa/platform/internal/identity/adapters/audit"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Sign-ins never reach the outbox, so Identity writes them to the audit
// trail itself (RFC 0006 §6.1): a success in every tenant the sign-in
// opens, a failure on a known account the same way, and nothing at all
// for an address with no account.
func TestSignInsAreAudited(t *testing.T) {
	trail := auditpg.NewStore(db.NewUnitOfWork(env.App))
	h := newHarnessMailing(t, false, func(d *app.Deps) {
		d.Audit = identityaudit.New(auditapp.New(trail))
	})
	ctx := context.Background()
	const addr, pw = "ada@example.com", "correct horse battery"
	if err := h.svc.Register(ctx, addr, pw, "Ada"); err != nil {
		t.Fatal(err)
	}
	first, err := h.svc.SignInWithPassword(ctx, addr, pw, "")
	if err != nil {
		t.Fatal(err)
	}
	person := "person:" + first.Person.ID.String()
	me, err := h.svc.GetMe(h.as(t, first, tenancy.ID{}))
	if err != nil || len(me.Memberships) != 1 {
		t.Fatalf("individual tenant: %v", err)
	}
	individual := me.Memberships[0].Tenant.ID
	acme, _, err := h.svc.CreateOrganization(h.as(t, first, tenancy.ID{}), "acme", "Acme", "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.svc.SignInWithPassword(ctx, addr, pw, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SignInWithPassword(ctx, addr, "not the password at all", ""); err == nil {
		t.Fatal("a wrong password signed in")
	}
	if _, err := h.svc.SignInWithPassword(ctx, "nobody@example.com", pw, ""); err == nil {
		t.Fatal("an unknown address signed in")
	}
	h.svc.WaitAudits()

	type want struct{ action, actor, method, reason string }
	ok := want{auditdomain.ActionSignedIn, person, "password", ""}
	failed := want{auditdomain.ActionSignInFailed, "unknown", "password", app.FailureInvalidCredentials}
	for tenant, wants := range map[tenancy.ID][]want{
		individual: {ok, ok, failed},
		acme.ID:    {ok, failed}, // the first sign-in came before acme existed
	} {
		es, err := trail.Entries(tenancy.ContextWithTenant(ctx, tenant), 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(es) != len(wants) {
			t.Fatalf("tenant %s: %d entries, want %d", tenant, len(es), len(wants))
		}
		for i, w := range wants {
			e := es[i]
			var s map[string]string
			if err := json.Unmarshal(e.Summary, &s); err != nil {
				t.Fatal(err)
			}
			if e.Action != w.action || e.Actor != w.actor || e.AggregateID != first.Person.ID.String() ||
				s["method"] != w.method || s["reason"] != w.reason || len(s) > 2 {
				t.Errorf("tenant %s entry %d: %s by %s on %s %v, want %+v", tenant, i, e.Action, e.Actor, e.AggregateID, s, w)
			}
		}
	}

	// No address, no password, no session token in any entry.
	var leaked int
	if err := env.Super.QueryRow(ctx, `SELECT count(*) FROM audit_entries
		WHERE row_to_json(audit_entries)::text ~ $1`, `example\.com|correct horse|not the password|`+first.SessionToken).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Errorf("%d entries carry an address, a password or a session", leaked)
	}
}
