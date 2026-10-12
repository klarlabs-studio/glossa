//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// spyTransactor counts the Store calls a unit of work makes and fails
// the ones fail picks, so a test can see how the derivation writes are
// issued and what happens when one fails.
type spyTransactor struct {
	app.Transactor
	mu    sync.Mutex
	calls map[string]int
	// fail returns the error a call of method on arg (the unit, when the
	// call has one) is to fail with.
	fail func(method string, arg any) error
}

func (s *spyTransactor) hit(method string, arg any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[method]++
	if s.fail != nil {
		return s.fail(method, arg)
	}
	return nil
}

func (s *spyTransactor) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

func (s *spyTransactor) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.calls)
}

func (s *spyTransactor) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return s.Transactor.InTenant(ctx, func(ctx context.Context, st app.Store) error {
		return fn(ctx, &spyStore{Store: st, spy: s})
	})
}

type spyStore struct {
	app.Store
	spy *spyTransactor
}

func (s *spyStore) EnsureDerivation(ctx context.Context, tr, project uuid.UUID, at time.Time) error {
	if err := s.spy.hit("EnsureDerivation", tr); err != nil {
		return err
	}
	return s.Store.EnsureDerivation(ctx, tr, project, at)
}

func (s *spyStore) LockDerivation(ctx context.Context, tr uuid.UUID) (int, error) {
	if err := s.spy.hit("LockDerivation", tr); err != nil {
		return 0, err
	}
	return s.Store.LockDerivation(ctx, tr)
}

func (s *spyStore) LockActiveUnit(ctx context.Context, tr uuid.UUID) (*domain.TMUnit, error) {
	if err := s.spy.hit("LockActiveUnit", tr); err != nil {
		return nil, err
	}
	return s.Store.LockActiveUnit(ctx, tr)
}

func (s *spyStore) InsertUnit(ctx context.Context, u domain.TMUnit) error {
	if err := s.spy.hit("InsertUnit", u); err != nil {
		return err
	}
	return s.Store.InsertUnit(ctx, u)
}

func (s *spyStore) TouchUnit(ctx context.Context, id uuid.UUID, rev int, at time.Time) error {
	if err := s.spy.hit("TouchUnit", id); err != nil {
		return err
	}
	return s.Store.TouchUnit(ctx, id, rev, at)
}

func (s *spyStore) RetireUnit(ctx context.Context, u domain.TMUnit) error {
	if err := s.spy.hit("RetireUnit", u); err != nil {
		return err
	}
	return s.Store.RetireUnit(ctx, u)
}

func (s *spyStore) SetDerivation(ctx context.Context, tr uuid.UUID, rev int, at time.Time) error {
	if err := s.spy.hit("SetDerivation", tr); err != nil {
		return err
	}
	return s.Store.SetDerivation(ctx, tr, rev, at)
}

func (s *spyStore) LockDerivations(ctx context.Context, refs []app.DerivationRef, at time.Time) (map[uuid.UUID]int, error) {
	if err := s.spy.hit("LockDerivations", refs); err != nil {
		return nil, err
	}
	return s.Store.LockDerivations(ctx, refs, at)
}

func (s *spyStore) LockActiveUnits(ctx context.Context, trs []uuid.UUID) (map[uuid.UUID]domain.TMUnit, error) {
	if err := s.spy.hit("LockActiveUnits", trs); err != nil {
		return nil, err
	}
	return s.Store.LockActiveUnits(ctx, trs)
}

func (s *spyStore) InsertUnits(ctx context.Context, us []domain.TMUnit) error {
	if err := s.spy.hit("InsertUnits", us); err != nil {
		return err
	}
	return s.Store.InsertUnits(ctx, us)
}

func (s *spyStore) TouchUnits(ctx context.Context, ts []app.UnitTouch, at time.Time) error {
	if err := s.spy.hit("TouchUnits", ts); err != nil {
		return err
	}
	return s.Store.TouchUnits(ctx, ts, at)
}

func (s *spyStore) RetireUnits(ctx context.Context, us []domain.TMUnit) error {
	if err := s.spy.hit("RetireUnits", us); err != nil {
		return err
	}
	return s.Store.RetireUnits(ctx, us)
}

func (s *spyStore) SetDerivations(ctx context.Context, ds []app.DerivationRevision, at time.Time) error {
	if err := s.spy.hit("SetDerivations", ds); err != nil {
		return err
	}
	return s.Store.SetDerivations(ctx, ds, at)
}

// spied installs a spy under the next harness.
func spied(t *testing.T) *spyTransactor {
	t.Helper()
	spy := &spyTransactor{calls: map[string]int{}}
	wrapTransactor = func(inner app.Transactor) app.Transactor {
		spy.Transactor = inner
		return spy
	}
	t.Cleanup(func() { wrapTransactor = func(inner app.Transactor) app.Transactor { return inner } })
	return spy
}

// deliver hands derive_tm one batch of revised events naming ids.
func deliver(h *harness, project uuid.UUID, ids ...uuid.UUID) []error {
	ds := make([]outbox.Delivery, len(ids))
	for i, id := range ids {
		payload, _ := json.Marshal(map[string]string{"translation_id": id.String(), "project_id": project.String()})
		ds[i] = outbox.Delivery{
			EventID: uuid.New(), TenantID: h.tenant, Type: "localization.translation.revised",
			AggregateType: "translation", AggregateID: id.String(), Payload: payload, Attempt: 1,
		}
	}
	return app.HandleTranslationEvents(h.svc, tenancy.ContextWithTenant(context.Background(), h.tenant), ds)
}

func noErrors(t *testing.T, errs []error) {
	t.Helper()
	for i, err := range errs {
		if err != nil {
			t.Errorf("delivery %d: %v", i, err)
		}
	}
}

// actorID matches the ID in an actor, which differs from run to run.
var actorID = regexp.MustCompile(`:[0-9a-f-]{36}`)

// tmState is the translation memory and its bookkeeping without what
// differs between runs (IDs, times), in a stable order.
func tmState(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	rows, err := env.Super.Query(ctx, `
		SELECT message_key, target_locale, target_mf2, translation_revision, coalesce(retired_reason, ''),
		       coalesce(retired_by, ''), created_by, hit_count
		FROM knowledge_tm_units ORDER BY message_key, target_locale, retired_at NULLS FIRST, translation_revision`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var key, loc, target, reason, by, author string
		var rev, hits int
		if err := rows.Scan(&key, &loc, &target, &rev, &reason, &by, &author, &hits); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("unit %s/%s %q rev=%d retired=%q by=%q author=%q hits=%d", key, loc, target, rev, reason, by, author, hits))
	}
	drows, err := env.Super.Query(ctx, `SELECT revision FROM knowledge_tm_derivations ORDER BY revision`)
	if err != nil {
		t.Fatal(err)
	}
	defer drows.Close()
	var revs []string
	for drows.Next() {
		var r int
		if err := drows.Scan(&r); err != nil {
			t.Fatal(err)
		}
		revs = append(revs, fmt.Sprint(r))
	}
	return actorID.ReplaceAllString(strings.Join(out, "\n"), ":<id>") + "\nderivations at revisions " + strings.Join(revs, ",")
}

// batchScenario leaves a project whose twenty translations are approved
// and derived, then changes them without delivering the events: five
// get new approved text (one of them twice), five become unapproved
// drafts with other text, two become drafts of the same text, and the
// rest stay. It returns the project and the translations in the order
// their events are to be delivered: reversed, with duplicates.
func batchScenario(t *testing.T, h *harness) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	const n = 20
	messages := map[string]string{}
	var keys []string
	for i := range n {
		k := fmt.Sprintf("k.%02d", i)
		keys = append(keys, k)
		messages[k] = fmt.Sprintf("Text %02d", i)
	}
	p := h.project(t, "shop", true, []string{"de"}, messages)
	for _, k := range keys {
		h.translate(t, p, k, "de", "Deutsch "+messages[k], ptr("approved"))
	}
	h.drain(t) // units for all twenty

	for _, k := range keys[:5] {
		h.translate(t, p, k, "de", "Neu "+messages[k], ptr("approved"))
	}
	h.translate(t, p, keys[0], "de", "Nochmal "+messages[keys[0]], ptr("approved"))
	for _, k := range keys[5:10] {
		h.translate(t, p, k, "de", "Entwurf "+messages[k], ptr("draft"))
	}
	for _, k := range keys[10:12] {
		h.translate(t, p, k, "de", "Deutsch "+messages[k], ptr("draft"))
	}
	ids := translationIDs(t, h, p, "de", keys...)
	order := slices.Clone(ids)
	order = append(order, ids[0], ids[7], ids[3])
	slices.Reverse(order)
	return p, order
}

// A batch derives the memory the way the same events delivered one at a
// time do — duplicates, reordering, a translation that lost its
// approval — and the way the per-translation path does.
func TestDeriveTMBatchEqualsSequential(t *testing.T) {
	run := func(t *testing.T, mode string) string {
		spy := spied(t)
		h := newHarness(t)
		p, order := batchScenario(t, h)
		switch mode {
		case "batch":
			noErrors(t, deliver(h, p, order...))
		case "one by one":
			for _, id := range order {
				noErrors(t, deliver(h, p, id))
			}
		case "per translation":
			spy.fail = func(method string, _ any) error {
				if method == "LockDerivations" {
					return errors.New("forced")
				}
				return nil
			}
			spy.reset()
			noErrors(t, deliver(h, p, order...))
			if spy.count("LockDerivation") == 0 {
				t.Error("the per-translation path did not run")
			}
		}
		return tmState(t)
	}
	want := run(t, "one by one")
	for _, mode := range []string{"batch", "per translation"} {
		if got := run(t, mode); got != want {
			t.Errorf("%s derived\n%s\nwant (one by one)\n%s", mode, got, want)
		}
	}
	// The 5 revised are superseded and 7 lost their approval: 13 stay active.
	if got := strings.Count(want, `retired=""`); got != 13 {
		t.Errorf("active units = %d, want 13:\n%s", got, want)
	}
	for _, reason := range []string{"superseded", "overwritten", "unapproved"} {
		if !strings.Contains(want, `retired="`+reason+`"`) {
			t.Errorf("no unit retired as %s:\n%s", reason, want)
		}
	}
}

// The batch's writes are a handful of statements however many
// translations it names, none made per translation.
func TestDeriveTMBatchWritesAreSetBased(t *testing.T) {
	spy := spied(t)
	h := newHarness(t)
	p, order := batchScenario(t, h)
	spy.reset()
	noErrors(t, deliver(h, p, order...))
	for _, m := range []string{"EnsureDerivation", "LockDerivation", "LockActiveUnit", "InsertUnit", "TouchUnit", "RetireUnit", "SetDerivation"} {
		if n := spy.count(m); n != 0 {
			t.Errorf("%s called %d times, want 0: the batch writes set-based", m, n)
		}
	}
	for _, m := range []string{"LockDerivations", "LockActiveUnits", "InsertUnits", "RetireUnits", "SetDerivations"} {
		if n := spy.count(m); n != 1 {
			t.Errorf("%s called %d times, want 1", m, n)
		}
	}
}

// If the set-based write fails, the translations are derived one by
// one, and one that cannot be derived fails alone: the others are
// committed.
func TestDeriveTMBatchFallsBackPerTranslation(t *testing.T) {
	spy := spied(t)
	h := newHarness(t)
	const n = 10
	messages := map[string]string{}
	var keys []string
	for i := range n {
		k := fmt.Sprintf("k.%02d", i)
		keys = append(keys, k)
		messages[k] = fmt.Sprintf("Text %02d", i)
	}
	p := h.project(t, "shop", false, []string{"de"}, messages)
	for _, k := range keys {
		h.translate(t, p, k, "de", "Deutsch "+messages[k], nil)
	}
	const bad = 3
	ids := translationIDs(t, h, p, "de", keys...)
	spy.fail = func(method string, arg any) error {
		switch method {
		case "InsertUnits":
			return errors.New("forced: batch insert")
		case "InsertUnit":
			if arg.(domain.TMUnit).MessageKey == keys[bad] {
				return errors.New("forced: bad translation")
			}
		}
		return nil
	}
	errs := deliver(h, p, ids...)
	for i, err := range errs {
		if (err != nil) != (i == bad) {
			t.Errorf("delivery %d: %v, want an error only for %d", i, err, bad)
		}
	}
	if errs[bad] != nil && outbox.IsPermanent(errs[bad]) {
		t.Errorf("the failure is permanent: %v", errs[bad])
	}
	if got := count(t, "SELECT count(*) FROM knowledge_tm_units WHERE retired_at IS NULL"); got != n-1 {
		t.Errorf("active units = %d, want %d: the others are committed", got, n-1)
	}
	if got := count(t, "SELECT count(*) FROM knowledge_tm_units WHERE message_key = $1", keys[bad]); got != 0 {
		t.Errorf("the failed translation has %d units, want 0", got)
	}
	if got := count(t, "SELECT count(*) FROM knowledge_tm_derivations"); got != n-1 {
		t.Errorf("derivations = %d, want %d", got, n-1)
	}
	// Delivered again with the fault gone, it is derived.
	spy.fail = nil
	noErrors(t, deliver(h, p, ids[bad]))
	if got := count(t, "SELECT count(*) FROM knowledge_tm_units WHERE retired_at IS NULL"); got != n {
		t.Errorf("active units after redelivery = %d, want %d", got, n)
	}
}
