//go:build integration

package app_test

import (
	"context"
	"errors"
	identitypg "go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	localizationidentity "go.klarlabs.de/glossa/platform/internal/localization/adapters/identity"
	"maps"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/coverage"
	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/projection"
	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	catalogport "go.klarlabs.de/glossa/platform/internal/localization/adapters/catalog"
	localizationpg "go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
)

// branchHarness wires Catalog with Localization the way the composition
// root does (coverage, impact and projection ports, Localization's
// subscribers on an outbox the test drains), on a clock the test moves.
type branchHarness struct {
	svc        *app.Service
	loc        *localizationapp.Service
	dispatcher *outbox.Dispatcher
	tenant     tenancy.ID
	now        time.Time
	project    domain.ProjectID
}

func newBranchHarness(t *testing.T) *branchHarness {
	t.Helper()
	ctx := context.Background()
	if err := env.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	tenant, err := env.SeedTenant(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	h := &branchHarness{tenant: tenant, now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	uow := db.NewUnitOfWork(env.App)
	h.svc = app.New(postgres.NewTransactor(uow), app.WithClock(func() time.Time { return h.now }),
		app.WithScanner(postgres.NewScanner(uow)))
	h.loc = localizationapp.New(localizationpg.NewTransactor(uow), catalogport.New(h.svc),
		localizationapp.WithReviewers(localizationidentity.NewReviewers(identitypg.NewTransactor(uow, nil))))
	h.svc.SetCoverage(coverage.New(h.loc))
	h.svc.SetImpact(coverage.New(h.loc))
	h.svc.SetProjection(projection.New(h.loc))
	reg := outbox.NewRegistry()
	if err := h.loc.Subscribe(reg); err != nil {
		t.Fatal(err)
	}
	h.dispatcher, err = outbox.NewDispatcher(outbox.NewPostgresStore(uow), reg, outbox.DispatcherConfig{
		BatchSize: 100, MaxAttempts: 3, PollInterval: 10 * time.Millisecond, Lease: time.Minute,
		HandlerTimeout: 10 * time.Second, InlineAttempts: 1, InlineBackoff: time.Millisecond,
	}, outbox.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	owner := h.owner()
	p, _, err := h.svc.CreateProject(owner, app.NewProject{Slug: "brotwerk", Name: "Brotwerk", SourceLocale: "en"}, "")
	if err != nil {
		t.Fatal(err)
	}
	h.project = p.ID
	if _, _, err := h.loc.AddLocale(owner, p.ID.UUID(), "de"); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *branchHarness) owner() context.Context {
	return authztest.Member(context.Background(), h.tenant, []string{"owner"})
}

// drain delivers every pending event and fails on a dead letter: every
// subscriber must accept what a branch publishes.
func (h *branchHarness) drain(t *testing.T) {
	t.Helper()
	for range 20 {
		n, err := h.dispatcher.ProcessBatch(context.Background())
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if n == 0 {
			if dead := count(t, "SELECT count(*) FROM outbox_events WHERE status = 'dead'"); dead > 0 {
				t.Fatalf("%d events dead-lettered", dead)
			}
			return
		}
	}
	t.Fatal("outbox did not drain")
}

func items(messages map[string]string) []app.UpsertItem {
	var out []app.UpsertItem
	for _, k := range slices.Sorted(maps.Keys(messages)) {
		out = append(out, app.UpsertItem{Key: k, Text: messages[k]})
	}
	return out
}

// pushDefault is the default branch's push (plain glossa push).
func (h *branchHarness) pushDefault(t *testing.T, messages map[string]string) []app.UpsertResult {
	t.Helper()
	res, err := h.svc.UpsertMessages(h.owner(), h.project, items(messages))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Error != nil {
			t.Fatalf("push %s: %+v", r.Key, r.Error)
		}
	}
	return res
}

func (h *branchHarness) pushBranch(t *testing.T, branch string, messages map[string]string, complete bool) app.BranchReport {
	t.Helper()
	rep, err := h.svc.PushBranch(h.owner(), h.project, app.BranchPush{Branch: branch, Items: items(messages), Complete: complete})
	if err != nil {
		t.Fatalf("push %s: %v", branch, err)
	}
	return rep
}

func (h *branchHarness) translate(t *testing.T, key, text string) {
	t.Helper()
	approved := "approved"
	if _, _, err := h.loc.PutTranslation(h.owner(), h.project.UUID(), key, "de",
		localizationapp.TranslationInput{Text: text, State: &approved}, nil); err != nil {
		t.Fatalf("translate %s: %v", key, err)
	}
}

func (h *branchHarness) message(t *testing.T, key string) domain.Message {
	t.Helper()
	m, err := h.svc.GetMessage(h.owner(), h.project, key)
	if err != nil {
		t.Fatalf("message %s: %v", key, err)
	}
	return m
}

func statuses(rep app.BranchReport) map[string]app.BranchItemStatus {
	out := map[string]app.BranchItemStatus{}
	for _, it := range rep.Items {
		out[it.Key] = it.Status
	}
	return out
}

func keys(ks []domain.MessageKey) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = string(k)
	}
	return out
}

func TestBranchPushProposesWithoutTouchingLiveSource(t *testing.T) {
	h := newBranchHarness(t)
	h.pushDefault(t, map[string]string{"checkout.pay": "Pay now", "cart.title": "Cart", "home.title": "Home"})
	h.translate(t, "checkout.pay", "Jetzt bezahlen")
	h.drain(t)
	revisions := count(t, "SELECT count(*) FROM catalog_source_revisions")

	pr := 42
	rep, err := h.svc.PushBranch(h.owner(), h.project, app.BranchPush{
		Branch: "feature/pay", PushInfo: domain.PushInfo{HeadCommit: "0a1b2c3d", PR: &pr}, Complete: true,
		Items: items(map[string]string{"checkout.pay": "Pay securely", "cart.title": "Cart", "checkout.tip": "Add a tip"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]app.BranchItemStatus{
		"checkout.pay": app.BranchSourceProposal, "cart.title": app.BranchUnchanged, "checkout.tip": app.BranchNewKey,
	}
	if got := statuses(rep); !maps.Equal(got, want) {
		t.Errorf("items %v, want %v", got, want)
	}
	if !slices.Equal(keys(rep.NewKeys), []string{"checkout.tip"}) || !slices.Equal(keys(rep.SourceProposals), []string{"checkout.pay"}) ||
		!slices.Equal(keys(rep.Removed), []string{"home.title"}) || len(rep.Conflicts) != 0 {
		t.Errorf("report %+v", rep)
	}
	if rep.Outdated["de"] != 1 || len(rep.Outdated) != 1 {
		t.Errorf("outdated per locale %v, want de:1", rep.Outdated)
	}
	b := rep.Branch
	if b.State != domain.BranchOpen || b.PR == nil || *b.PR != 42 || b.HeadCommit != "0a1b2c3d" || b.Version != 2 {
		t.Errorf("branch %+v", b)
	}

	// Nothing live changed: the source log records merged history only.
	if pay := h.message(t, "checkout.pay"); pay.Revision != 1 || pay.Source.Text != "Pay now" {
		t.Errorf("live source changed: %+v", pay)
	}
	if n := count(t, "SELECT count(*) FROM catalog_source_revisions"); n != revisions+1 {
		t.Errorf("%d source revisions, want %d (only the new key's first)", n, revisions+1)
	}
	tip := h.message(t, "checkout.tip")
	if tip.State != domain.MessageProposed {
		t.Errorf("new key state %q", tip.State)
	}
	// The release source is the live catalog only.
	snap, err := h.svc.ReleaseSource(h.owner(), h.project)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range snap.Messages {
		if m.Key == "checkout.tip" || m.Source.Text != map[domain.MessageKey]string{
			"checkout.pay": "Pay now", "cart.title": "Cart", "home.title": "Home",
		}[m.Key] {
			t.Errorf("release source has %s %q", m.Key, m.Source.Text)
		}
	}

	// A proposed message is translated like any other.
	h.translate(t, "checkout.tip", "Trinkgeld geben")
	h.drain(t)
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'catalog.branch.pushed' AND aggregate_id = $1", b.ID.String()); n != 1 {
		t.Errorf("%d branch pushed events", n)
	}

	// The status report reads the same, and pushing again is a no-op.
	status, err := h.svc.BranchStatus(h.owner(), h.project, "feature/pay")
	if err != nil || !slices.Equal(keys(status.NewKeys), []string{"checkout.tip"}) || status.Outdated["de"] != 1 {
		t.Errorf("status %v %+v", err, status)
	}
	again := h.pushBranch(t, "feature/pay", map[string]string{"checkout.pay": "Pay securely", "cart.title": "Cart", "checkout.tip": "Add a tip"}, true)
	if n := count(t, "SELECT count(*) FROM catalog_source_revisions"); n != revisions+1 || again.Branch.Version != 3 {
		t.Errorf("repush: %d revisions, branch v%d", n, again.Branch.Version)
	}

	// The overlay is what the branch's preview adds.
	o, err := h.svc.BranchOverlay(h.owner(), h.project, "feature/pay")
	if err != nil || len(o.Proposals) != 2 || o.Proposals[0].Key != "checkout.pay" || o.Proposals[0].Source.Text != "Pay securely" ||
		o.Proposals[0].BaseRevision != 1 || o.Messages[tip.ID].State != domain.MessageProposed {
		t.Errorf("overlay %v %+v", err, o)
	}
}

func TestProposedMessageTakesItsBranchsSource(t *testing.T) {
	h := newBranchHarness(t)
	h.pushBranch(t, "feature/a", map[string]string{"x.new": "First"}, false)
	id := h.message(t, "x.new").ID
	// The branch owns its proposed message: changing it is the message's
	// own (pre-merge) history, and translations become outdated.
	h.pushBranch(t, "feature/a", map[string]string{"x.new": "Second"}, false)
	m := h.message(t, "x.new")
	if m.ID != id || m.Revision != 2 || m.State != domain.MessageProposed || m.Source.Text != "Second" {
		t.Errorf("after re-push %+v", m)
	}
	h.drain(t)
}

func TestKeyConflicts(t *testing.T) {
	h := newBranchHarness(t)
	a := h.pushBranch(t, "feature/a", map[string]string{"promo.banner": "Summer sale"}, false)
	b := h.pushBranch(t, "feature/b", map[string]string{"promo.banner": "Winter sale"}, false)
	if statuses(b)["promo.banner"] != app.BranchKeyConflict || len(b.Conflicts) != 1 ||
		!slices.Equal(b.Conflicts[0].Branches, []domain.BranchName{"feature/a"}) {
		t.Fatalf("b = %+v", b)
	}
	// Recorded for both: a's report shows the conflict too.
	sa, err := h.svc.BranchStatus(h.owner(), h.project, "feature/a")
	if err != nil || len(sa.Conflicts) != 1 || sa.Conflicts[0].Key != "promo.banner" ||
		!slices.Equal(sa.Conflicts[0].Branches, []domain.BranchName{"feature/b"}) {
		t.Errorf("a's status %v %+v", err, sa)
	}
	// The message keeps the first branch's source.
	if m := h.message(t, "promo.banner"); m.Source.Text != "Summer sale" || m.Revision != 1 {
		t.Errorf("conflict changed the message: %+v", m)
	}
	var pushed struct {
		ConflictingBranches []string `json:"conflicting_branches"`
	}
	if err := env.Super.QueryRow(context.Background(), `SELECT payload->'conflicting_branches' FROM outbox_events
		WHERE event_type = 'catalog.branch.pushed' AND aggregate_id = $1`, b.Branch.ID.String()).Scan(&pushed.ConflictingBranches); err != nil ||
		!slices.Equal(pushed.ConflictingBranches, []string{a.Branch.ID.String()}) {
		t.Errorf("pushed event conflicting_branches %v %v", err, pushed.ConflictingBranches)
	}

	// Same source as a: c shares a's proposed message (and, like a,
	// conflicts with b).
	c := h.pushBranch(t, "feature/c", map[string]string{"promo.banner": "Summer sale"}, false)
	if c.Items[0].Message.ID != h.message(t, "promo.banner").ID || len(c.Conflicts) != 1 ||
		!slices.Equal(c.Conflicts[0].Branches, []domain.BranchName{"feature/b"}) {
		t.Errorf("c = %+v", c)
	}
	if b, _ := h.svc.BranchStatus(h.owner(), h.project, "feature/b"); len(b.Conflicts) != 1 ||
		!slices.Equal(b.Conflicts[0].Branches, []domain.BranchName{"feature/a", "feature/c"}) {
		t.Errorf("b's conflicts %+v", b.Conflicts)
	}
	if n := count(t, "SELECT count(*) FROM catalog_messages WHERE key = 'promo.banner'"); n != 1 {
		t.Errorf("%d messages for one key", n)
	}

	// b comes round to the same source: no conflict left anywhere.
	if b = h.pushBranch(t, "feature/b", map[string]string{"promo.banner": "Summer sale"}, false); statuses(b)["promo.banner"] != app.BranchNewKey {
		t.Errorf("b shares: %+v", b.Items)
	}
	for _, name := range []string{"feature/a", "feature/b", "feature/c"} {
		s, err := h.svc.BranchStatus(h.owner(), h.project, name)
		if err != nil || len(s.Conflicts) != 0 {
			t.Errorf("%s still conflicts: %v %+v", name, err, s.Conflicts)
		}
	}
	// A closed branch no longer conflicts.
	h.pushBranch(t, "feature/b", map[string]string{"promo.banner": "Autumn sale"}, false)
	if _, err := h.svc.CloseBranch(h.owner(), h.project, "feature/b"); err != nil {
		t.Fatal(err)
	}
	if s, _ := h.svc.BranchStatus(h.owner(), h.project, "feature/a"); len(s.Conflicts) != 0 {
		t.Errorf("a conflicts with a closed branch: %+v", s.Conflicts)
	}
	h.drain(t)
}

func TestDefaultBranchPushActivates(t *testing.T) {
	h := newBranchHarness(t)
	h.pushDefault(t, map[string]string{"checkout.pay": "Pay now"})
	h.translate(t, "checkout.pay", "Jetzt bezahlen")
	h.pushBranch(t, "feature/pay", map[string]string{"checkout.pay": "Pay securely", "checkout.tip": "Add a tip"}, false)
	h.pushBranch(t, "feature/other", map[string]string{"checkout.pay": "Pay later"}, false)
	h.translate(t, "checkout.tip", "Trinkgeld geben")
	h.drain(t)
	tip := h.message(t, "checkout.tip")

	// The PR merges; CI pushes the default branch. No webhook needed.
	res := h.pushDefault(t, map[string]string{"checkout.pay": "Pay securely", "checkout.tip": "Add a tip"})
	if res[0].Status != app.UpsertRevised || res[1].Status != app.UpsertUpdated {
		t.Errorf("statuses %s %s", res[0].Status, res[1].Status)
	}
	active := h.message(t, "checkout.tip")
	if active.ID != tip.ID || active.State != domain.MessageActive || active.Revision != 1 {
		t.Errorf("activated %+v", active)
	}
	if tr, err := h.loc.GetTranslation(h.owner(), h.project.UUID(), "checkout.tip", "de"); err != nil || tr.Content.Text != "Trinkgeld geben" {
		t.Errorf("translation lost on activation: %v %+v", err, tr)
	}
	if pay := h.message(t, "checkout.pay"); pay.Revision != 2 || pay.Source.Text != "Pay securely" {
		t.Errorf("proposal did not become a revision: %+v", pay)
	}
	// The existing events make the translation outdated.
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'localization.translation.outdated'"); n != 1 {
		t.Errorf("%d outdated events, want 1", n)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'catalog.message.activated' AND aggregate_id = $1", tip.ID.String()); n != 1 {
		t.Errorf("%d activated events", n)
	}
	// Merged proposals are settled; the other branch's still stands.
	s, err := h.svc.BranchStatus(h.owner(), h.project, "feature/pay")
	if err != nil || len(s.NewKeys) != 0 || len(s.SourceProposals) != 0 {
		t.Errorf("merged branch still proposes: %v %+v", err, s)
	}
	o, err := h.svc.BranchStatus(h.owner(), h.project, "feature/other")
	if err != nil || !slices.Equal(keys(o.SourceProposals), []string{"checkout.pay"}) {
		t.Errorf("other branch: %v %+v", err, o)
	}
	snap, err := h.svc.ReleaseSource(h.owner(), h.project)
	if err != nil || len(snap.Messages) != 2 {
		t.Errorf("release source after merge: %v %d", err, len(snap.Messages))
	}
	h.drain(t)
}

func TestClosedBranchProposalsExpireAndComeBack(t *testing.T) {
	h := newBranchHarness(t)
	h.pushBranch(t, "feature/tip", map[string]string{"checkout.tip": "Add a tip"}, false)
	h.translate(t, "checkout.tip", "Trinkgeld geben")
	closedAt := h.now
	if b, err := h.svc.CloseBranch(h.owner(), h.project, "feature/tip"); err != nil || b.State != domain.BranchClosed || !b.ClosedAt.Equal(closedAt) {
		t.Fatalf("close: %v %+v", err, b)
	}
	sweeper, err := authz.Background(tenancy.ContextWithTenant(context.Background(), h.tenant), "catalog.sweep", authz.CatalogWrite)
	if err != nil {
		t.Fatal(err)
	}
	h.now = closedAt.Add(domain.ProposalRetention - time.Minute)
	if n, err := h.svc.SweepProposals(sweeper); err != nil || n != 0 {
		t.Fatalf("early sweep: %d %v", n, err)
	}
	h.now = closedAt.Add(domain.ProposalRetention)
	if n, err := h.svc.SweepProposals(sweeper); err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	m := h.message(t, "checkout.tip")
	if m.State != domain.MessageObsolete {
		t.Fatalf("after sweep %+v", m)
	}
	if n, _ := h.svc.SweepProposals(sweeper); n != 0 {
		t.Errorf("second sweep obsoleted %d", n)
	}

	// Reopening brings it back with its translation and history.
	if b, err := h.svc.ReopenBranch(h.owner(), h.project, "feature/tip"); err != nil || b.State != domain.BranchOpen || b.ClosedAt != nil {
		t.Fatalf("reopen: %v %+v", err, b)
	}
	back := h.message(t, "checkout.tip")
	if back.ID != m.ID || back.State != domain.MessageProposed || back.Revision != 1 {
		t.Errorf("reopened %+v", back)
	}
	if tr, err := h.loc.GetTranslation(h.owner(), h.project.UUID(), "checkout.tip", "de"); err != nil || tr.Content.Text != "Trinkgeld geben" {
		t.Errorf("translation after reopen: %v %+v", err, tr)
	}

	// Merged without the default branch bringing it in: swept the same way.
	if _, err := h.svc.MergeBranch(h.owner(), h.project, "feature/tip"); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(domain.ProposalRetention)
	if n, err := h.svc.SweepProposals(sweeper); err != nil || n != 1 {
		t.Fatalf("sweep after merge: %d %v", n, err)
	}
	// A later push of the key, from another branch, proposes it again.
	rep := h.pushBranch(t, "feature/tip-again", map[string]string{"checkout.tip": "Add a tip"}, false)
	if statuses(rep)["checkout.tip"] != app.BranchNewKey || h.message(t, "checkout.tip").State != domain.MessageProposed {
		t.Errorf("later push: %+v", rep)
	}
	for typ, want := range map[string]int{
		"catalog.branch.closed": 1, "catalog.branch.reopened": 1, "catalog.branch.merged": 1,
		"catalog.message.obsoleted": 2, "catalog.message.proposed": 2,
	} {
		if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = $1", typ); n != want {
			t.Errorf("%d %s events, want %d", n, typ, want)
		}
	}
	h.drain(t)
}

func TestBranchLifecycleRules(t *testing.T) {
	h := newBranchHarness(t)
	h.pushBranch(t, "feature/x", map[string]string{"x.one": "One"}, false)
	// Closing twice is one change; a push on a closed branch reopens it.
	for range 2 {
		if _, err := h.svc.CloseBranch(h.owner(), h.project, "feature/x"); err != nil {
			t.Fatal(err)
		}
	}
	rep := h.pushBranch(t, "feature/x", map[string]string{"x.one": "One"}, false)
	if rep.Branch.State != domain.BranchOpen {
		t.Errorf("push did not reopen: %+v", rep.Branch)
	}
	if _, err := h.svc.MergeBranch(h.owner(), h.project, "feature/x"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.PushBranch(h.owner(), h.project, app.BranchPush{Branch: "feature/x", Items: items(map[string]string{"x.one": "One"})}); !errors.Is(err, domain.ErrBranchMerged) {
		t.Errorf("push on merged: %v", err)
	}
	if _, err := h.svc.CloseBranch(h.owner(), h.project, "nope"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("close unknown: %v", err)
	}
	if _, err := h.svc.PushBranch(h.owner(), h.project, app.BranchPush{Branch: "bad name", Items: items(map[string]string{"a": "A"})}); !errors.Is(err, domain.ErrInvalidBranchName) {
		t.Errorf("bad name: %v", err)
	}
	if _, err := h.svc.PushBranch(h.owner(), h.project, app.BranchPush{Branch: "feature/y"}); !errors.Is(err, app.ErrTooManyBranchItems) {
		t.Errorf("empty push: %v", err)
	}
	b, err := h.svc.ReportBranchPreview(h.owner(), h.project, "feature/x", "https://pr-1.preview.example.com")
	if err != nil || b.PreviewURL != "https://pr-1.preview.example.com" {
		t.Errorf("preview: %v %+v", err, b)
	}
}

func TestCompleteBranchPushWithdrawsKeys(t *testing.T) {
	h := newBranchHarness(t)
	h.pushBranch(t, "feature/a", map[string]string{"a.kept": "Kept", "a.dropped": "Dropped", "a.shared": "Shared"}, true)
	h.pushBranch(t, "feature/b", map[string]string{"a.shared": "Shared"}, false)
	rep := h.pushBranch(t, "feature/a", map[string]string{"a.kept": "Kept"}, true)
	if !slices.Equal(keys(rep.NewKeys), []string{"a.kept"}) {
		t.Errorf("new keys %v", rep.NewKeys)
	}
	// Nobody proposes a.dropped any more; b still proposes a.shared.
	if m := h.message(t, "a.dropped"); m.State != domain.MessageObsolete {
		t.Errorf("dropped %q", m.State)
	}
	if m := h.message(t, "a.shared"); m.State != domain.MessageProposed {
		t.Errorf("shared %q", m.State)
	}
	// A partial push withdraws nothing.
	rep = h.pushBranch(t, "feature/b", map[string]string{"b.more": "More"}, false)
	if !slices.Equal(keys(rep.NewKeys), []string{"a.shared", "b.more"}) {
		t.Errorf("partial push new keys %v", rep.NewKeys)
	}
	h.drain(t)
}

func TestBranchesAreAuthorizedAndTenantIsolated(t *testing.T) {
	h := newBranchHarness(t)
	h.pushBranch(t, "feature/a", map[string]string{"a.b": "A"}, false)
	translator := authztest.Member(context.Background(), h.tenant, []string{"translator"}, "de")
	if _, err := h.svc.PushBranch(translator, h.project, app.BranchPush{Branch: "feature/a", Items: items(map[string]string{"a.b": "B"})}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator pushes a branch: %v", err)
	}
	if _, err := h.svc.CloseBranch(translator, h.project, "feature/a"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator closes a branch: %v", err)
	}
	if _, err := h.svc.BranchStatus(translator, h.project, "feature/a"); err != nil {
		t.Errorf("translator reads branch status: %v", err)
	}
	if _, err := h.svc.SweepProposals(translator); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator sweeps: %v", err)
	}
	other, err := env.SeedTenant(context.Background(), "bolt")
	if err != nil {
		t.Fatal(err)
	}
	intruder := authztest.Member(context.Background(), other, []string{"owner"})
	if _, err := h.svc.BranchStatus(intruder, h.project, "feature/a"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("intruder reads a branch: %v", err)
	}
	if _, err := h.svc.PushBranch(intruder, h.project, app.BranchPush{Branch: "feature/a", Items: items(map[string]string{"a.b": "B"})}); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("intruder pushes: %v", err)
	}
	if n, err := h.svc.SweepProposals(intruder); err != nil || n != 0 {
		t.Errorf("intruder's sweep: %d %v", n, err)
	}
}

func TestClosedBranchesReportsWhenEachClosed(t *testing.T) {
	h := newBranchHarness(t)
	h.pushBranch(t, "feature/open", map[string]string{"a.open": "A"}, false)
	h.pushBranch(t, "feature/closed", map[string]string{"a.closed": "B"}, false)
	h.pushBranch(t, "feature/merged", map[string]string{"a.merged": "C"}, false)

	// An open branch is not in the map at any time.
	if got, err := h.svc.ClosedBranches(h.owner(), h.project); err != nil || len(got) != 0 {
		t.Fatalf("with no closed branch = %v, %v", got, err)
	}
	closedAt := h.now
	if _, err := h.svc.CloseBranch(h.owner(), h.project, "feature/closed"); err != nil {
		t.Fatal(err)
	}
	h.now = closedAt.Add(time.Hour)
	mergedAt := h.now
	if _, err := h.svc.MergeBranch(h.owner(), h.project, "feature/merged"); err != nil {
		t.Fatal(err)
	}

	got, err := h.svc.ClosedBranches(h.owner(), h.project)
	if err != nil {
		t.Fatal(err)
	}
	want := map[domain.BranchName]time.Time{"feature/closed": closedAt.UTC(), "feature/merged": mergedAt.UTC()}
	if len(got) != len(want) {
		t.Fatalf("ClosedBranches = %v, want %v", got, want)
	}
	for name, at := range want {
		if !got[name].Equal(at) {
			t.Errorf("%s closed at %v, want %v", name, got[name], at)
		}
	}

	// Reopening takes the branch out again: its builds stop expiring.
	if _, err := h.svc.ReopenBranch(h.owner(), h.project, "feature/closed"); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.svc.ClosedBranches(h.owner(), h.project); len(got) != 1 {
		t.Errorf("after reopening = %v, want only the merged branch", got)
	}
	// A translator may read them; another tenant sees nothing.
	translator := authztest.Member(context.Background(), h.tenant, []string{"translator"}, "de")
	if _, err := h.svc.ClosedBranches(translator, h.project); err != nil {
		t.Errorf("translator: %v", err)
	}
	other, err := env.SeedTenant(context.Background(), "bolt")
	if err != nil {
		t.Fatal(err)
	}
	intruder := authztest.Member(context.Background(), other, []string{"owner"})
	if got, err := h.svc.ClosedBranches(intruder, h.project); err != nil || len(got) != 0 {
		t.Errorf("intruder: %v, %v", got, err)
	}
}

func TestSweepAllProposalsVisitsEveryTenantWithExpiredProposals(t *testing.T) {
	h := newBranchHarness(t)
	h.pushBranch(t, "feature/tip", map[string]string{"checkout.tip": "Add a tip"}, false)
	closedAt := h.now
	if _, err := h.svc.CloseBranch(h.owner(), h.project, "feature/tip"); err != nil {
		t.Fatal(err)
	}

	// The sweep runs outside any tenant, like the daily purge job.
	if n, err := h.svc.SweepAllProposals(context.Background()); err != nil || n != 0 {
		t.Fatalf("before the retention period: %d %v", n, err)
	}
	h.now = closedAt.Add(domain.ProposalRetention)
	n, err := h.svc.SweepAllProposals(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("SweepAllProposals = %d, %v; want the tenant's one proposal", n, err)
	}
	if m := h.message(t, "checkout.tip"); m.State != domain.MessageObsolete {
		t.Errorf("after the sweep %+v", m)
	}
	// Idempotent: nothing is left to obsolete.
	if n, err := h.svc.SweepAllProposals(context.Background()); err != nil || n != 0 {
		t.Errorf("second run = %d, %v", n, err)
	}
	// A request context carries a tenant; the sweep refuses it.
	if _, err := h.svc.SweepAllProposals(h.owner()); err == nil {
		t.Error("SweepAllProposals ran in a tenant's request context")
	}
	h.drain(t)
}
