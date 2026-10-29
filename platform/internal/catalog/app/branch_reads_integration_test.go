//go:build integration

package app_test

import (
	"errors"
	"slices"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// The reads and the upsert the Branches API serves (RFC 0004 §4.1, §9).

// nextPage is the page the token of the previous one asks for.
func nextPage(t *testing.T, size int, token *string) pagination.Page {
	t.Helper()
	p, err := pagination.Parse(&size, token)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func branchNames(bs []domain.Branch) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = string(b.Name)
	}
	return out
}

func TestListBranchesFiltersByStateAndName(t *testing.T) {
	h := newBranchHarness(t)
	h.pushBranch(t, "feature/a", map[string]string{"a.one": "One"}, false)
	h.pushBranch(t, "feature/b", map[string]string{"b.one": "One"}, false)
	h.pushBranch(t, "feature/c", map[string]string{"c.one": "One"}, false)
	if _, err := h.svc.CloseBranch(h.owner(), h.project, "feature/b"); err != nil {
		t.Fatal(err)
	}
	h.drain(t)

	all, next, err := h.svc.ListBranches(h.owner(), h.project, app.BranchFilter{}, firstPage())
	if err != nil || next != nil || !slices.Equal(branchNames(all), []string{"feature/a", "feature/b", "feature/c"}) {
		t.Fatalf("all: %v %v %v", branchNames(all), next, err)
	}
	open, _, err := h.svc.ListBranches(h.owner(), h.project, app.BranchFilter{State: domain.BranchOpen}, firstPage())
	if err != nil || !slices.Equal(branchNames(open), []string{"feature/a", "feature/c"}) {
		t.Errorf("open: %v %v", branchNames(open), err)
	}
	named, _, err := h.svc.ListBranches(h.owner(), h.project, app.BranchFilter{Name: "feature/b"}, firstPage())
	if err != nil || !slices.Equal(branchNames(named), []string{"feature/b"}) {
		t.Errorf("by name: %v %v", branchNames(named), err)
	}
	// A filter, not a lookup: an unknown or unparsable name is empty.
	for _, name := range []string{"feature/nope", "bad name"} {
		got, _, err := h.svc.ListBranches(h.owner(), h.project, app.BranchFilter{Name: name}, firstPage())
		if err != nil || len(got) != 0 {
			t.Errorf("by name %q: %v %v", name, branchNames(got), err)
		}
	}
	if _, _, err := h.svc.ListBranches(h.owner(), h.project, app.BranchFilter{State: "dangling"}, firstPage()); !errors.Is(err, app.ErrInvalidBranchState) {
		t.Errorf("bad state filter: %v", err)
	}

	// Pages are cursor-based, by name.
	first, next, err := h.svc.ListBranches(h.owner(), h.project, app.BranchFilter{}, pagination.Page{Size: 2})
	if err != nil || next == nil || !slices.Equal(branchNames(first), []string{"feature/a", "feature/b"}) {
		t.Fatalf("page 1: %v %v %v", branchNames(first), next, err)
	}
	second, next, err := h.svc.ListBranches(h.owner(), h.project, app.BranchFilter{}, nextPage(t, 2, next))
	if err != nil || next != nil || !slices.Equal(branchNames(second), []string{"feature/c"}) {
		t.Errorf("page 2: %v %v %v", branchNames(second), next, err)
	}
}

func TestGetBranchByIDAndListProposals(t *testing.T) {
	h := newBranchHarness(t)
	h.pushDefault(t, map[string]string{"checkout.pay": "Pay now"})
	h.drain(t)
	rep := h.pushBranch(t, "feature/pay", map[string]string{"checkout.pay": "Pay securely", "checkout.tip": "Add a tip"}, true)
	h.drain(t)

	b, err := h.svc.GetBranch(h.owner(), h.project, rep.Branch.ID)
	if err != nil || b.Name != "feature/pay" {
		t.Fatalf("by id: %v %+v", err, b)
	}
	if _, err := h.svc.GetBranch(h.owner(), h.project, domain.NewBranchID()); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}

	ps, next, err := h.svc.ListProposals(h.owner(), h.project, "feature/pay", firstPage())
	if err != nil || next != nil || len(ps) != 2 {
		t.Fatalf("proposals: %v %d %v", err, len(ps), err)
	}
	if ps[0].Key != "checkout.pay" || ps[0].Kind != domain.ProposalSourceChange || ps[0].BaseRevision != 1 ||
		ps[1].Key != "checkout.tip" || ps[1].Kind != domain.ProposalNewKey {
		t.Errorf("proposals %+v", ps)
	}
	page, next, err := h.svc.ListProposals(h.owner(), h.project, "feature/pay", pagination.Page{Size: 1})
	if err != nil || next == nil || len(page) != 1 || page[0].Key != "checkout.pay" {
		t.Fatalf("page 1: %v %+v %v", err, page, next)
	}
	page, _, err = h.svc.ListProposals(h.owner(), h.project, "feature/pay", nextPage(t, 1, next))
	if err != nil || len(page) != 1 || page[0].Key != "checkout.tip" {
		t.Errorf("page 2: %v %+v", err, page)
	}
	if _, _, err := h.svc.ListProposals(h.owner(), h.project, "feature/nope", firstPage()); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown branch: %v", err)
	}
}

func TestUpsertBranchOpensRecordsAndReopens(t *testing.T) {
	h := newBranchHarness(t)
	pr := 7
	b, created, err := h.svc.UpsertBranch(h.owner(), h.project, app.BranchUpsert{
		Branch: "feature/pay", PushInfo: domain.PushInfo{HeadCommit: "0a1b2c3d", PR: &pr},
	})
	if err != nil || !created || b.State != domain.BranchOpen || b.PR == nil || *b.PR != 7 || b.HeadCommit != "0a1b2c3d" {
		t.Fatalf("create: %v %t %+v", err, created, b)
	}
	url := "https://pr-7.preview.example.com"
	b, created, err = h.svc.UpsertBranch(h.owner(), h.project, app.BranchUpsert{
		Branch: "feature/pay", PushInfo: domain.PushInfo{HeadCommit: "9f9f9f9"}, PreviewURL: &url,
	})
	if err != nil || created || b.HeadCommit != "9f9f9f9" || b.PreviewURL != url || *b.PR != 7 {
		t.Fatalf("update: %v %t %+v", err, created, b)
	}
	if _, _, err := h.svc.UpsertBranch(h.owner(), h.project, app.BranchUpsert{Branch: "bad name"}); !errors.Is(err, domain.ErrInvalidBranchName) {
		t.Errorf("invalid name: %v", err)
	}

	// A closed branch reopens; a merged one takes no more.
	if _, err := h.svc.CloseBranch(h.owner(), h.project, "feature/pay"); err != nil {
		t.Fatal(err)
	}
	if b, _, err := h.svc.UpsertBranch(h.owner(), h.project, app.BranchUpsert{Branch: "feature/pay"}); err != nil || b.State != domain.BranchOpen {
		t.Fatalf("reopen: %v %+v", err, b)
	}
	if _, err := h.svc.MergeBranch(h.owner(), h.project, "feature/pay"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.UpsertBranch(h.owner(), h.project, app.BranchUpsert{Branch: "feature/pay"}); !errors.Is(err, domain.ErrBranchMerged) {
		t.Errorf("merged: %v", err)
	}
	h.drain(t)
	for typ, want := range map[string]int{"catalog.branch.opened": 1, "catalog.branch.reopened": 1} {
		if got := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = '"+typ+"'"); got != want {
			t.Errorf("%s events %d, want %d", typ, got, want)
		}
	}
}

func TestBranchPushPublishesOpenedOnce(t *testing.T) {
	h := newBranchHarness(t)
	h.pushBranch(t, "feature/pay", map[string]string{"checkout.tip": "Add a tip"}, false)
	h.pushBranch(t, "feature/pay", map[string]string{"checkout.tip": "Add a tip, please"}, false)
	h.drain(t)
	if got := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'catalog.branch.opened'"); got != 1 {
		t.Errorf("opened events %d, want 1", got)
	}
}

func TestOpenBranchesProposingAMessage(t *testing.T) {
	h := newBranchHarness(t)
	h.pushBranch(t, "feature/a", map[string]string{"checkout.tip": "Add a tip"}, false)
	h.pushBranch(t, "feature/b", map[string]string{"checkout.tip": "Add a tip"}, false)
	h.pushBranch(t, "feature/c", map[string]string{"other.key": "Other"}, false)
	h.drain(t)
	m := h.message(t, "checkout.tip")

	got, err := h.svc.OpenBranchesProposing(h.owner(), h.project, m.ID)
	if err != nil || !slices.Equal(got, []domain.BranchName{"feature/a", "feature/b"}) {
		t.Fatalf("proposing: %v %v", got, err)
	}
	if _, err := h.svc.CloseBranch(h.owner(), h.project, "feature/a"); err != nil {
		t.Fatal(err)
	}
	if got, err := h.svc.OpenBranchesProposing(h.owner(), h.project, m.ID); err != nil ||
		!slices.Equal(got, []domain.BranchName{"feature/b"}) {
		t.Errorf("after close: %v %v", got, err)
	}
	h.drain(t)
}
