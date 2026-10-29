//go:build integration

package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// v0Rows is a v0.3 history whose text holds the canary: the import is
// handed only its digests, and the test proves the canary reached no
// row of the table.
func v0Rows() app.V0HistoryImport {
	at := time.Date(2025, 3, 14, 15, 9, 0, 0, time.UTC)
	rows := []app.V0HistoryEntry{
		{V0ID: "101", Actor: "v0:6f1c2a9e-1b7d-4c55-9a51-3c1a3e1f0b2d", Key: "greeting", Locale: "de",
			AfterSHA256: sha("Hallo " + canary)},
		{V0ID: "102", Actor: "v0:ai:openai", Key: "greeting", Locale: "de",
			BeforeSHA256: sha("Hallo " + canary), AfterSHA256: sha("Grüß dich, " + canary)},
		{V0ID: "103", Actor: "v0:unknown", Unresolved: "translation_deleted",
			BeforeSHA256: sha(canary + " gone")},
	}
	for i := range rows {
		rows[i].Action, rows[i].Project = domain.ActionV0TranslationChanged, uuid.MustParse(project)
		rows[i].OccurredAt = at.Add(time.Duration(i) * time.Minute)
	}
	return app.V0HistoryImport{Restore: "glossa-v03.sql.gz", RestoreSHA256: sha("the dump"), Entries: rows}
}

// The import on the real table: appended after the live entries already
// there, followed by more live entries, idempotent, and the chain
// verifies at every step.
func TestImportedHistoryJoinsTheChain(t *testing.T) {
	h := newHarness(t)
	ctx := h.tenant("acme")
	tenant, _ := tenancy.FromContext(ctx)
	owner := authztest.Member(ctx, tenant, []string{"owner"})

	h.publish(ctx, translationRevised(person))
	h.dispatch()

	r, err := h.svc.ImportV0History(owner, v0Rows())
	if err != nil || r.Recorded != 3 || r.Existing != 0 {
		t.Fatalf("import: %+v, %v", r, err)
	}
	h.verify(ctx, 4)

	// A live event after the import continues the same chain.
	h.publish(ctx, translationRevised(token))
	h.dispatch()
	h.verify(ctx, 5)

	// Importing again — the same tenant, another of its projects — adds
	// nothing.
	r, err = h.svc.ImportV0History(owner, v0Rows())
	if err != nil || r.Recorded != 0 || r.Existing != 3 {
		t.Fatalf("second import: %+v, %v", r, err)
	}
	es := h.entries(ctx)
	if len(es) != 5 {
		t.Fatalf("%d entries, want 5", len(es))
	}
	for i, want := range []struct {
		source domain.Source
		actor  string
	}{
		{domain.SourceOutbox, person},
		{domain.SourceImport, "v0:6f1c2a9e-1b7d-4c55-9a51-3c1a3e1f0b2d"},
		{domain.SourceImport, "v0:ai:openai"},
		{domain.SourceImport, "v0:unknown"},
		{domain.SourceOutbox, token},
	} {
		if es[i].Source != want.source || es[i].Actor != want.actor {
			t.Errorf("entry %d: %s by %s, want %s by %s", i+1, es[i].Source, es[i].Actor, want.source, want.actor)
		}
	}
	// The chain orders by append: v0.3's 2025 rows sit after today's.
	if !es[1].OccurredAt.Before(es[0].OccurredAt) || es[1].OccurredAt.Year() != 2025 {
		t.Errorf("occurred_at is v0.3's: %v after %v", es[1].OccurredAt, es[0].OccurredAt)
	}
	if es[3].Locale != "" || es[3].Project.UUID.String() != project {
		t.Errorf("the unresolved row: locale %q, project %v", es[3].Locale, es[3].Project)
	}
	// The backfill counts only outbox entries, so imported ones never
	// make it think the outbox's history is projected.
	if n, err := h.store.OutboxEntries(ctx); err != nil || n != 2 {
		t.Errorf("outbox entries = %d, %v; want 2", n, err)
	}
	h.assertNoCanary()
}

// What 0051 lets in, and what it still keeps out: the database itself
// refuses a v0.3 actor on a live entry and a platform actor on an
// imported one, and an imported entry is as append-only as any.
func TestTheDatabaseKeepsImportedActorsApart(t *testing.T) {
	h := newHarness(t)
	ctx := h.tenant("acme")
	tenant, _ := tenancy.FromContext(ctx)
	owner := authztest.Member(ctx, tenant, []string{"owner"})
	if _, err := h.svc.ImportV0History(owner, v0Rows()); err != nil {
		t.Fatal(err)
	}
	insert := func(d domain.Draft) error {
		return h.store.InChain(ctx, func(ctx context.Context, c app.Chain) error {
			head, err := c.Head(ctx)
			if err != nil {
				return err
			}
			// Built past Draft.Validate, as a buggy or hostile writer
			// would: only the table's own check stands in the way.
			e := domain.Entry{Draft: d, Tenant: tenant.UUID(), Sequence: head.Sequence + 1, PrevHash: head.Hash, Hash: make([]byte, 32)}
			return c.Insert(ctx, e)
		})
	}
	base := domain.Draft{EventID: uuid.New(), Action: "identity.person.signed_in", OccurredAt: time.Now(),
		AggregateType: "person", AggregateID: uuid.NewString(), Summary: []byte(`{}`)}
	for what, d := range map[string]domain.Draft{
		"a live entry naming a v0.3 actor":          withSource(base, domain.SourceDirect, "v0:unknown"),
		"an imported entry naming a platform actor": withSource(base, domain.SourceImport, person),
		"an imported entry naming nobody":           withSource(base, domain.SourceImport, "unknown"),
		"a fourth source":                           withSource(base, "replay", person),
	} {
		wantPgCode(t, insert(d), "23514", what)
	}

	for _, stmt := range []string{
		"UPDATE audit_entries SET actor = 'v0:unknown' WHERE source = 'import'",
		"DELETE FROM audit_entries WHERE source = 'import'",
	} {
		err := h.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
			_, err := tx.Exec(ctx, stmt)
			return err
		})
		wantPgCode(t, err, "42501", stmt)
	}
	h.verify(ctx, 3)

	// And another tenant sees none of it.
	if es := h.entries(h.tenant("globex")); len(es) != 0 {
		t.Errorf("globex sees %d of acme's entries", len(es))
	}
}

func withSource(d domain.Draft, s domain.Source, actor string) domain.Draft {
	d.Source, d.Actor = s, actor
	return d
}
