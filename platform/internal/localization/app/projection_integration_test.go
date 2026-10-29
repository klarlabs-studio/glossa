//go:build integration

package app_test

import (
	"testing"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
)

func keysOf(t *testing.T, h *harness, p catalogdomain.ProjectID, q catalogapp.MessageQuery) []string {
	t.Helper()
	ms, _, err := h.catalog.ListMessages(h.developer(), p, q, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, m := range ms {
		keys = append(keys, string(m.Key))
	}
	return keys
}

// The outbox subscriber catches the projection up with a whole batch at
// once (#89): several pushes' events, two revisions of one message
// among them, delivered together, outdate each translation once, and
// the projection ends at the highest version whatever the order.
func TestTheSubscriberCatchesUpABatchOfMessageEvents(t *testing.T) {
	h := newHarnessProjecting(t, false)
	keys := []string{"a.one", "a.two", "a.three"}
	p := catalogdomain.ProjectID(h.setup(t, false, []string{"de"}, map[string]string{
		keys[0]: "One", keys[1]: "Two", keys[2]: "Three",
	}))
	ctx := h.developer()
	for _, k := range keys {
		if _, _, err := h.svc.PutTranslation(ctx, p.UUID(), k, "de", app.TranslationInput{Text: "Übersetzt"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	h.drain(t)

	push := func(items ...catalogapp.UpsertItem) {
		t.Helper()
		if _, err := h.catalog.UpsertMessages(ctx, p, items); err != nil {
			t.Fatal(err)
		}
	}
	push(catalogapp.UpsertItem{Key: keys[0], Text: "One!"}, catalogapp.UpsertItem{Key: keys[1], Text: "Two!"})
	push(catalogapp.UpsertItem{Key: keys[0], Text: "One!!"}, catalogapp.UpsertItem{Key: keys[2], Text: "Three!"})
	if got := keysOf(t, h, p, catalogapp.MessageQuery{OutdatedIn: "de"}); len(got) != 0 {
		t.Fatalf("outdated_in before delivery = %v: the projection is synchronous", got)
	}
	h.drain(t)
	if got := keysOf(t, h, p, catalogapp.MessageQuery{OutdatedIn: "de"}); len(got) != 3 {
		t.Errorf("outdated_in after the batch = %v, want all three", got)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'localization.translation.outdated'"); n != 3 {
		t.Errorf("%d outdated events, want one per translation", n)
	}
	if n := count(t, "SELECT count(*) FROM localization_messages WHERE key = $1 AND source_revision = 3", keys[0]); n != 1 {
		t.Errorf("a.one not projected at its last source revision")
	}
}

// A bulk upsert brings Localization's projection up to date in its own
// transaction: missing_in and outdated_in see the push at once, before
// any event is delivered, and the outbox catch-up later changes nothing
// — the outdated event is published once.
func TestBulkUpsertProjectsSynchronously(t *testing.T) {
	h := newHarness(t)
	p := catalogdomain.ProjectID(h.setup(t, false, []string{"de"}, map[string]string{"home.title": "Welcome"}))
	ctx := h.developer()
	if _, _, err := h.svc.PutTranslation(ctx, p.UUID(), "home.title", "de", app.TranslationInput{Text: "Willkommen"}, nil); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	outdatedEvents := func() int {
		return count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'localization.translation.outdated'")
	}

	res, err := h.catalog.UpsertMessages(ctx, p, []catalogapp.UpsertItem{
		{Key: "home.title", Text: "Welcome home"}, {Key: "home.greeting", Text: "Hello"},
	})
	if err != nil || res[0].Status != catalogapp.UpsertRevised || res[1].Status != catalogapp.UpsertCreated {
		t.Fatalf("upsert = %+v, %v", res, err)
	}
	// No event delivered yet.
	if got := keysOf(t, h, p, catalogapp.MessageQuery{MissingIn: "de"}); len(got) != 1 || got[0] != "home.greeting" {
		t.Errorf("missing_in right after the push = %v", got)
	}
	if got := keysOf(t, h, p, catalogapp.MessageQuery{OutdatedIn: "de"}); len(got) != 1 || got[0] != "home.title" {
		t.Errorf("outdated_in right after the push = %v", got)
	}
	if n := outdatedEvents(); n != 1 {
		t.Errorf("outdated events after the push = %d, want 1", n)
	}
	// Inside the push, the outdating is the pusher's own change.
	pusher, _ := authz.EventActor(ctx)
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'localization.translation.outdated' AND actor = $1",
		pusher.String()); n != 1 {
		t.Errorf("the outdated event does not name the pusher %s", pusher)
	}

	h.drain(t) // the catch-up finds the projection current
	if n := outdatedEvents(); n != 1 {
		t.Errorf("outdated events after catch-up = %d, want still 1", n)
	}
	if got := keysOf(t, h, p, catalogapp.MessageQuery{OutdatedIn: "de"}); len(got) != 1 {
		t.Errorf("outdated_in after catch-up = %v", got)
	}

	// Pushing the same catalog again changes nothing.
	if _, err := h.catalog.UpsertMessages(ctx, p, []catalogapp.UpsertItem{{Key: "home.title", Text: "Welcome home"}}); err != nil {
		t.Fatal(err)
	}
	if n := outdatedEvents(); n != 1 {
		t.Errorf("outdated events after a no-op push = %d", n)
	}
}
