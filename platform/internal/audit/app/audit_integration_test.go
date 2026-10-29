//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	auditpg "go.klarlabs.de/glossa/platform/internal/audit/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

var env *dbtest.Env

func TestMain(m *testing.M) {
	var err error
	env, err = dbtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	env.Close()
	os.Exit(code)
}

const (
	person  = "person:0190a1b2-0000-7000-8000-0000000000aa"
	token   = "token:0190a1b2-0000-7000-8000-0000000000ab"
	project = "0190a1b2-0000-7000-8000-000000000001"
	// canary only ever appears in message text (RFC 0006 §12.5).
	canary = "Quokkafluent"
)

// system is a background process's actor, as identity derives one.
var system = "system:" + uuid.NewSHA1(uuid.NameSpaceOID, []byte("test.sweep")).String()

type harness struct {
	t          *testing.T
	uow        *db.UnitOfWork
	svc        *app.Service
	store      *auditpg.Store
	dispatcher *outbox.Dispatcher
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if err := env.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	store := auditpg.NewStore(uow)
	svc := app.New(store, app.WithHistory(outbox.NewHistory(uow)))
	reg := outbox.NewRegistry()
	if err := svc.Subscribe(reg); err != nil {
		t.Fatal(err)
	}
	d, err := outbox.NewDispatcher(outbox.NewPostgresStore(uow), reg, outbox.DispatcherConfig{
		BatchSize: 100, MaxAttempts: 3, PollInterval: 10 * time.Millisecond,
		Lease: 30 * time.Second, HandlerTimeout: 10 * time.Second,
	}, outbox.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, uow: uow, svc: svc, store: store, dispatcher: d}
}

func (h *harness) tenant(slug string) context.Context {
	h.t.Helper()
	id, err := env.SeedTenant(context.Background(), slug)
	if err != nil {
		h.t.Fatal(err)
	}
	return tenancy.ContextWithTenant(context.Background(), id)
}

type event struct {
	typ, actor string
	payload    map[string]any
}

func translationRevised(by string) event {
	return event{typ: "localization.translation.revised", actor: by, payload: map[string]any{
		"translation_id": uuid.NewString(), "project_id": project, "message_id": uuid.NewString(),
		"locale": "de", "revision": 2, "source_revision": 1, "state": "needs_review", "origin": "human",
		"by": by, "text": "Der " + canary + " ist da",
	}}
}

// publish records events the way every context does: outbox.Publish in
// the tenant's transaction.
func (h *harness) publish(ctx context.Context, events ...event) []uuid.UUID {
	h.t.Helper()
	var ids []uuid.UUID
	err := h.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		for _, e := range events {
			id, err := outbox.Publish(ctx, tx, outbox.Event{
				Type: e.typ, AggregateType: "translation", AggregateID: uuid.NewString(),
				Actor: outbox.Actor(e.actor), Payload: e.payload,
			})
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		h.t.Fatalf("publish: %v", err)
	}
	return ids
}

// dispatch delivers every pending event.
func (h *harness) dispatch() {
	h.t.Helper()
	for {
		n, err := h.dispatcher.ProcessBatch(context.Background())
		if err != nil {
			h.t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
}

func (h *harness) entries(ctx context.Context) []domain.Entry {
	h.t.Helper()
	es, err := h.store.Entries(ctx, 0, 10_000)
	if err != nil {
		h.t.Fatalf("read entries: %v", err)
	}
	return es
}

func (h *harness) verify(ctx context.Context, want int64) {
	h.t.Helper()
	r, err := h.svc.Verify(ctx)
	if err != nil {
		h.t.Fatalf("the chain does not verify: %v", err)
	}
	if r.Entries != want {
		h.t.Errorf("verified %d entries, want %d", r.Entries, want)
	}
}

func TestEventsBecomeEntriesWithTheirActors(t *testing.T) {
	h := newHarness(t)
	ctx := h.tenant("acme")
	ids := h.publish(ctx,
		translationRevised(person),
		translationRevised(token),
		event{typ: "quality.check_run.recorded", actor: system, payload: map[string]any{
			"run_id": uuid.NewString(), "project_id": project, "ref": "refs/heads/main", "trigger": "sweep",
			"conclusion": "success",
		}},
	)
	h.dispatch()

	es := h.entries(ctx)
	if len(es) != 3 {
		t.Fatalf("%d entries, want 3", len(es))
	}
	for i, want := range []struct{ action, actor string }{
		{"localization.translation.revised", person},
		{"localization.translation.revised", token},
		{"quality.check_run.recorded", system},
	} {
		e := es[i]
		if e.Sequence != int64(i+1) || e.Action != want.action || e.Actor != want.actor || e.EventID != ids[i] {
			t.Errorf("entry %d: seq %d, %s by %s (event %s), want %s by %s (event %s)",
				i, e.Sequence, e.Action, e.Actor, e.EventID, want.action, want.actor, ids[i])
		}
		if e.Source != domain.SourceOutbox || !e.Project.Valid || e.Project.UUID.String() != project {
			t.Errorf("entry %d: source %s, project %v", i, e.Source, e.Project)
		}
	}
	if es[0].Locale != "de" || es[2].Locale != "" {
		t.Errorf("locales %q, %q", es[0].Locale, es[2].Locale)
	}
	h.verify(ctx, 3)
	h.assertNoCanary()
}

// assertNoCanary reads every stored entry past row-level security and
// checks no message text reached it.
func (h *harness) assertNoCanary() {
	h.t.Helper()
	var leaked int
	err := env.Super.QueryRow(context.Background(),
		"SELECT count(*) FROM audit_entries WHERE row_to_json(audit_entries)::text LIKE '%' || $1 || '%'", canary).Scan(&leaked)
	if err != nil {
		h.t.Fatal(err)
	}
	if leaked > 0 {
		h.t.Errorf("%d entries carry message text", leaked)
	}
}

func TestARedeliveryRecordsNothingTwice(t *testing.T) {
	h := newHarness(t)
	ctx := h.tenant("acme")
	id := h.publish(ctx, translationRevised(person))[0]
	h.dispatch()

	// The dispatcher redelivers after a crash or an expired lease; the
	// handler sees the same event again.
	page, err := outbox.NewHistory(h.uow).Page(ctx, outbox.HistoryCursor{}, 10)
	if err != nil || len(page) != 1 || page[0].EventID != id {
		t.Fatalf("history: %v %+v", err, page)
	}
	for range 3 {
		if err := h.svc.HandleEvent(ctx, page[0]); err != nil {
			t.Fatal(err)
		}
	}
	if es := h.entries(ctx); len(es) != 1 {
		t.Errorf("%d entries after redelivery, want 1", len(es))
	}
	h.verify(ctx, 1)
}

// TestABatchAppendsOneLinkedEntryPerEvent: the dispatcher hands the
// projection a claimed batch at once (#89); each tenant's events land
// in one append, gap-free and linked, interleaved with direct writes,
// and handing the same batch over again — duplicates included —
// records nothing twice.
func TestABatchAppendsOneLinkedEntryPerEvent(t *testing.T) {
	h := newHarness(t)
	acme, globex := h.tenant("acme"), h.tenant("globex")
	if err := h.svc.RecordSignIn(acme, domain.SignIn{
		Attempt: uuid.Must(uuid.NewV7()), Person: uuid.Must(uuid.NewV7()), Method: "password", At: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	const n = 150 // more than one claimed batch of 100
	for i := range n {
		h.publish(acme, translationRevised(person))
		if i%3 == 0 {
			h.publish(globex, translationRevised(token))
		}
	}
	h.dispatch()
	h.verify(acme, n+1)
	h.verify(globex, n/3)

	page, err := outbox.NewHistory(h.uow).Page(acme, outbox.HistoryCursor{}, n)
	if err != nil || len(page) != n {
		t.Fatalf("history: %v, %d events", err, len(page))
	}
	again := append(page[:10:10], page[:10]...)
	for i, err := range h.svc.HandleBatch(acme, again) {
		if err != nil {
			t.Errorf("redelivery %d: %v", i, err)
		}
	}
	h.verify(acme, n+1)

	broken := page[0]
	broken.EventID, broken.Type = uuid.Must(uuid.NewV7()), "nobody.unmapped.happened"
	errs := h.svc.HandleBatch(acme, []outbox.Delivery{broken, page[1]})
	if !outbox.IsPermanent(errs[0]) || errs[1] != nil {
		t.Errorf("results = %v, want the unmapped event alone to fail permanently", errs)
	}
	h.verify(acme, n+1)
}

func TestTwoTenantsKeepTheirOwnChainsAndSeeOnlyTheirOwn(t *testing.T) {
	h := newHarness(t)
	acme, globex := h.tenant("acme"), h.tenant("globex")
	h.publish(acme, translationRevised(person), translationRevised(person))
	h.publish(globex, translationRevised(token))
	h.publish(acme, translationRevised(token))
	h.dispatch()

	a, g := h.entries(acme), h.entries(globex)
	if len(a) != 3 || len(g) != 1 {
		t.Fatalf("acme %d entries, globex %d; want 3 and 1", len(a), len(g))
	}
	if g[0].Sequence != 1 || g[0].Actor != token {
		t.Errorf("globex's chain does not start at its own first entry: %+v", g[0])
	}
	h.verify(acme, 3)
	h.verify(globex, 1)

	// Row-level security: inside acme's scope, globex's entries do not
	// exist, and acme cannot write into globex's chain.
	globexID, _ := tenancy.FromContext(globex)
	err := h.uow.InTenantTx(acme, func(ctx context.Context, tx *db.TenantTx) error {
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM audit_entries WHERE tenant_id = $1", globexID.UUID()).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("acme sees %d of globex's entries", n)
		}
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM audit_entries").Scan(&n); err != nil {
			return err
		}
		if n != 3 {
			t.Errorf("acme sees %d entries, want its own 3", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A row that would be a first entry as far as acme can see, so the
	// chain trigger lets it through and row-level security decides.
	e, err := domain.Append(globexID.UUID(), domain.Head{}, domain.SignIn{
		Attempt: uuid.Must(uuid.NewV7()), Person: uuid.Must(uuid.NewV7()), Method: "password", At: time.Now(),
	}.Draft())
	if err != nil {
		t.Fatal(err)
	}
	err = h.uow.InTenantTx(acme, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := tx.Exec(ctx, `INSERT INTO audit_entries (tenant_id, sequence, event_id, source, action, actor,
			occurred_at, aggregate_type, aggregate_id, prev_hash, hash) VALUES ($1, $2, $3, 'direct', $4, $5, now(), $6, $7, $8, $9)`,
			e.Tenant, e.Sequence, e.EventID, e.Action, e.Actor, e.AggregateType, e.AggregateID, e.PrevHash, e.Hash)
		return err
	})
	wantPgCode(t, err, "42501", "acme writing into globex's chain")
}

func wantPgCode(t *testing.T, err error, code, what string) {
	t.Helper()
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != code {
		t.Errorf("%s: err = %v, want SQLSTATE %s", what, err, code)
	}
}

func TestTheTrailIsAppendOnly(t *testing.T) {
	h := newHarness(t)
	ctx := h.tenant("acme")
	h.publish(ctx, translationRevised(person))
	h.dispatch()
	for _, stmt := range []string{
		"UPDATE audit_entries SET actor = 'unknown'",
		"UPDATE audit_entries SET summary = '{}'::jsonb WHERE sequence = 1",
		"DELETE FROM audit_entries",
		"TRUNCATE audit_entries",
	} {
		err := h.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
			_, err := tx.Exec(ctx, stmt)
			return err
		})
		wantPgCode(t, err, "42501", stmt)
	}
	if es := h.entries(ctx); len(es) != 1 || es[0].Actor != person {
		t.Errorf("the entry changed: %+v", es)
	}
}

// The database holds the chain's shape on its own: whatever the code
// does, an entry that skips a sequence or links to the wrong hash is
// refused.
func TestTheDatabaseRefusesAGapOrAFork(t *testing.T) {
	h := newHarness(t)
	ctx := h.tenant("acme")
	h.publish(ctx, translationRevised(person))
	h.dispatch()
	head := h.entries(ctx)[0].Head()
	tenant, _ := tenancy.FromContext(ctx)
	draft := domain.SignIn{Attempt: uuid.Must(uuid.NewV7()), Person: uuid.Must(uuid.NewV7()), Method: "password", At: time.Now()}.Draft()

	for name, from := range map[string]domain.Head{
		"a gap":                  {Sequence: head.Sequence + 1, Hash: head.Hash},
		"a fork":                 {Sequence: head.Sequence, Hash: make([]byte, 32)},
		"a replay of sequence 1": {},
	} {
		e, err := domain.Append(tenant.UUID(), from, draft)
		if err != nil {
			t.Fatal(err)
		}
		err = h.store.InChain(ctx, func(ctx context.Context, c app.Chain) error { return c.Insert(ctx, e) })
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	h.verify(ctx, 1)
}

func TestConcurrentAppendsToOneTenantSerialize(t *testing.T) {
	h := newHarness(t)
	ctx := h.tenant("acme")
	other := h.tenant("globex")
	const writers = 24
	var wg sync.WaitGroup
	errs := make(chan error, 2*writers)
	for i := range writers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs <- h.svc.RecordSignIn(ctx, domain.SignIn{
				Attempt: uuid.Must(uuid.NewV7()), Person: uuid.Must(uuid.NewV7()), Method: "password", At: time.Now(),
			})
		}()
		go func() {
			defer wg.Done()
			errs <- h.svc.RecordToolCall(other, domain.ToolCall{
				Call: uuid.Must(uuid.NewV7()), Actor: token, Toolset: "read", Tool: fmt.Sprintf("tool_%d", i),
				Outcome: "ok", At: time.Now(),
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []context.Context{ctx, other} {
		es := h.entries(c)
		if len(es) != writers {
			t.Fatalf("%d entries, want %d", len(es), writers)
		}
		for i, e := range es {
			if e.Sequence != int64(i+1) {
				t.Fatalf("entry %d has sequence %d: the chain has a gap", i, e.Sequence)
			}
		}
		h.verify(c, writers)
	}
}

func TestDirectWritesForSignInsAndToolCalls(t *testing.T) {
	h := newHarness(t)
	ctx := observability.ContextWithRequestID(h.tenant("acme"), "req-7")
	p := uuid.Must(uuid.NewV7())
	call := uuid.Must(uuid.NewV7())
	for _, err := range []error{
		h.svc.RecordSignIn(ctx, domain.SignIn{Attempt: uuid.Must(uuid.NewV7()), Person: p, Method: "passkey", At: time.Now()}),
		h.svc.RecordSignIn(ctx, domain.SignIn{
			Attempt: uuid.Must(uuid.NewV7()), Person: p, Method: "password", Failure: "invalid_credentials", At: time.Now(),
		}),
		h.svc.RecordToolCall(ctx, domain.ToolCall{
			Call: call, Actor: token, Toolset: "write", Tool: "message_upsert", Outcome: "ok",
			Arguments: map[string]string{"locale": "de", "text": "string(len=40)"}, Affected: 1, At: time.Now(),
		}),
		// The MCP ledger row is the call's identity: recording it again
		// records nothing.
		h.svc.RecordToolCall(ctx, domain.ToolCall{Call: call, Actor: token, Toolset: "write", Tool: "message_upsert",
			Outcome: "ok", At: time.Now()}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	es := h.entries(ctx)
	if len(es) != 3 {
		t.Fatalf("%d entries, want 3", len(es))
	}
	for i, want := range []struct{ action, actor string }{
		{domain.ActionSignedIn, "person:" + p.String()},
		{domain.ActionSignInFailed, "unknown"},
		{domain.ActionToolCalled, token},
	} {
		if es[i].Action != want.action || es[i].Actor != want.actor || es[i].Source != domain.SourceDirect ||
			es[i].RequestID != "req-7" {
			t.Errorf("entry %d: %s by %s (%s, request %q)", i, es[i].Action, es[i].Actor, es[i].Source, es[i].RequestID)
		}
	}
	if es[2].AggregateID != call.String() {
		t.Errorf("the tool call entry does not point at its ledger row: %s", es[2].AggregateID)
	}
	h.verify(ctx, 3)
}

func TestATamperedRowFailsVerification(t *testing.T) {
	h := newHarness(t)
	ctx := h.tenant("acme")
	h.publish(ctx, translationRevised(person), translationRevised(token), translationRevised(person))
	h.dispatch()
	h.verify(ctx, 3)

	// Only a superuser can edit the table (§9.5); the chain is what
	// makes the edit visible.
	tenant, _ := tenancy.FromContext(ctx)
	if _, err := env.Super.Exec(context.Background(),
		`UPDATE audit_entries SET actor = $1 WHERE tenant_id = $2 AND sequence = 2`, person, tenant.UUID()); err != nil {
		t.Fatal(err)
	}
	r, err := h.svc.Verify(ctx)
	var ce *domain.ChainError
	if !errors.As(err, &ce) || ce.Sequence != 2 || r.Entries != 1 {
		t.Errorf("a tampered actor: %v (verified %d)", err, r.Entries)
	}
}

// Entries as Postgres keeps them — microsecond times, the summary as
// jsonb hands it back — export into a glossa.audit/v1 file that
// verifies, whole or as a range in the middle of the chain; one altered
// byte does not (RFC 0006 §6.2).
func TestStoredEntriesExportAndVerify(t *testing.T) {
	h := newHarness(t)
	ctx := h.tenant("acme")
	h.publish(ctx, translationRevised(person), translationRevised(token), translationRevised(person),
		translationRevised(token), translationRevised(person))
	h.dispatch()
	entries := h.entries(ctx)
	if len(entries) != 5 {
		t.Fatalf("%d entries recorded, want 5", len(entries))
	}
	tenant, _ := tenancy.FromContext(ctx)
	key, err := domain.ParseSigningKey("audit-it", "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA")
	if err != nil {
		t.Fatal(err)
	}
	opts := domain.ExportOptions{CreatedAt: time.Now()}
	for name, r := range map[string]struct {
		from domain.Head
		rng  []domain.Entry
	}{
		"the whole chain": {domain.Head{}, entries},
		"sequences 2–4":   {entries[0].Head(), entries[1:4]},
	} {
		manifest, lines, err := domain.Export(tenant.UUID(), r.from, r.rng, key, opts)
		if err != nil {
			t.Fatalf("%s: export: %v", name, err)
		}
		rep := domain.VerifyExport(manifest, strings.NewReader(string(lines)), []domain.PublicKey{key.Public()})
		if !rep.OK || rep.Verified != int64(len(r.rng)) {
			t.Fatalf("%s: does not verify: %v", name, rep.Failure)
		}
		tampered := []byte(string(lines))
		tampered[len(tampered)/2] ^= 0x01
		if rep := domain.VerifyExport(manifest, strings.NewReader(string(tampered)), []domain.PublicKey{key.Public()}); rep.OK {
			t.Fatalf("%s: a tampered export verifies", name)
		}
	}
}

// insertHistory writes an outbox row as it was before migration 0042
// and before Audit existed: delivered, actor "unknown".
func insertHistory(t *testing.T, ctx context.Context, typ string, at time.Time, payload map[string]any) uuid.UUID {
	t.Helper()
	tenant, _ := tenancy.FromContext(ctx)
	id := uuid.Must(uuid.NewV7())
	raw, _ := json.Marshal(payload)
	_, err := env.Super.Exec(context.Background(), `INSERT INTO outbox_events
		(id, tenant_id, event_type, aggregate_type, aggregate_id, actor, payload, occurred_at, status, delivered_at)
		VALUES ($1, $2, $3, 'project', $4, 'unknown', $5, $6, 'delivered', $6)`,
		id, tenant.UUID(), typ, uuid.NewString(), raw, at)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestBackfillThenLiveEventsContinueTheChain(t *testing.T) {
	h := newHarness(t)
	acme, globex := h.tenant("acme"), h.tenant("globex")
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	// Inserted out of order: the backfill projects in the order events
	// occurred, not the order they were written.
	third := insertHistory(t, acme, "catalog.project.updated", t0.Add(2*time.Hour), map[string]any{"project_id": project})
	first := insertHistory(t, acme, "catalog.project.created", t0, map[string]any{"project_id": project, "by": person})
	second := insertHistory(t, acme, "catalog.widget.frobbed", t0.Add(time.Hour), map[string]any{"text": canary})
	insertHistory(t, globex, "catalog.project.created", t0, map[string]any{"project_id": project, "by": token})

	r, err := h.svc.Backfill(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := app.BackfillReport{Tenants: 2, Events: 4, Recorded: 4, ActorFromPayload: 2, Unknown: 2, Retired: 1}
	if r != want {
		t.Errorf("report %+v, want %+v", r, want)
	}
	es := h.entries(acme)
	if len(es) != 3 || es[0].EventID != first || es[1].EventID != second || es[2].EventID != third {
		t.Fatalf("history was not projected in the order it occurred: %+v", es)
	}
	if es[0].Actor != person || es[1].Actor != "unknown" || es[2].Actor != "unknown" {
		t.Errorf("actors %q %q %q: the backfill must take the payload's by or say unknown", es[0].Actor, es[1].Actor, es[2].Actor)
	}

	// Live events continue the same chain.
	h.publish(acme, translationRevised(token))
	h.dispatch()
	es = h.entries(acme)
	if len(es) != 4 || es[3].Sequence != 4 || es[3].Actor != token || es[3].Action != "localization.translation.revised" {
		t.Fatalf("the live event did not continue the chain: %+v", es)
	}
	h.verify(acme, 4)
	h.verify(globex, 1)

	// Running it again records nothing — including the live event, which
	// is history now too.
	again, err := h.svc.Backfill(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again.Recorded != 0 {
		t.Errorf("a second backfill recorded %d entries", again.Recorded)
	}
	h.assertNoCanary()
}

// Live delivery and the backfill race at the first deploy: whichever
// writes an event first, it is recorded once and the chain stays whole.
func TestBackfillAlongsideLiveDelivery(t *testing.T) {
	h := newHarness(t)
	ctx := h.tenant("acme")
	for i := range 30 {
		insertHistory(t, ctx, "catalog.project.updated", time.Date(2026, 3, 1, 12, i, 0, 0, time.UTC),
			map[string]any{"project_id": project, "by": person})
	}
	h.publish(ctx, translationRevised(person), translationRevised(token))
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = h.svc.Backfill(context.Background()) }()
	go func() { defer wg.Done(); h.dispatch() }()
	wg.Wait()
	if _, err := h.svc.Backfill(context.Background()); err != nil {
		t.Fatal(err)
	}
	es := h.entries(ctx)
	if len(es) != 32 {
		t.Errorf("%d entries, want 32", len(es))
	}
	seen := map[uuid.UUID]bool{}
	for _, e := range es {
		if seen[e.EventID] {
			t.Errorf("event %s recorded twice", e.EventID)
		}
		seen[e.EventID] = true
	}
	h.verify(ctx, 32)
}

func TestTheTraceOfThePublishingRequestIsKept(t *testing.T) {
	h := newHarness(t)
	ctx := h.tenant("acme")
	tenant, _ := tenancy.FromContext(ctx)
	// A request that published under a trace leaves its W3C carrier on
	// the event; the entry keeps the trace id as its correlation id.
	_, err := env.Super.Exec(context.Background(), `INSERT INTO outbox_events
		(id, tenant_id, event_type, aggregate_type, aggregate_id, actor, payload, trace_context)
		VALUES ($1, $2, 'catalog.project.updated', 'project', 'p', $3, '{}', $4)`,
		uuid.Must(uuid.NewV7()), tenant.UUID(), person,
		`{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}`)
	if err != nil {
		t.Fatal(err)
	}
	h.dispatch()
	es := h.entries(ctx)
	if len(es) != 1 || es[0].TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace id %+v", es)
	}
	if strings.Contains(fmt.Sprint(es[0].Summary), "traceparent") {
		t.Error("the carrier leaked into the summary")
	}
}
