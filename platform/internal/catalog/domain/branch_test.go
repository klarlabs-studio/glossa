package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
)

func newBranch(t *testing.T) domain.Branch {
	t.Helper()
	b, err := domain.NewBranch(domain.NewProjectID(), "feature/checkout", t0)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBranchNames(t *testing.T) {
	for _, ok := range []string{"main", "feature/checkout", "fix-1.2_x", "dependabot/npm_and_yarn/vite-5.0.1"} {
		if _, err := domain.ParseBranchName(ok); err != nil {
			t.Errorf("ParseBranchName(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a b", "a\tb", "x..y", "a~1", "a^", "a:b", "a?", "a*", "a[", `a\b`, "/a", "a/", "a.lock",
		"@", ".hidden", "feat/.hidden", "a/b.lock/c", "a@{1}", "a//b",
		strings.Repeat("a", domain.MaxBranchNameLen+1)} {
		if _, err := domain.ParseBranchName(bad); !errors.Is(err, domain.ErrInvalidBranchName) {
			t.Errorf("ParseBranchName(%q) = %v, want ErrInvalidBranchName", bad, err)
		}
	}
}

func TestNewBranchIsOpen(t *testing.T) {
	b := newBranch(t)
	if b.State != domain.BranchOpen || b.Version != 1 || b.ClosedAt != nil || b.ID.IsZero() {
		t.Errorf("new branch = %+v", b)
	}
}

func TestBranchPushRecordsHeadAndPR(t *testing.T) {
	b := newBranch(t)
	pr := 42
	if _, err := b.Push(domain.PushInfo{HeadCommit: "0123abcd", PR: &pr}, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if b.HeadCommit != "0123abcd" || b.PR == nil || *b.PR != 42 || b.Version != 2 {
		t.Errorf("after push: %+v", b)
	}
	for _, bad := range []domain.PushInfo{{HeadCommit: "xyz"}, {HeadCommit: "ABCDEF12"}, {PR: ptr(0)}} {
		c := newBranch(t)
		if _, err := c.Push(bad, t0); !errors.Is(err, domain.ErrInvalidPush) {
			t.Errorf("push %+v: %v", bad, err)
		}
	}
}

func TestBranchLifecycle(t *testing.T) {
	b := newBranch(t)
	closed := t0.Add(time.Hour)
	if changed, err := b.Close(closed); err != nil || !changed || b.State != domain.BranchClosed || !b.ClosedAt.Equal(closed) {
		t.Fatalf("close: %v %v %+v", changed, err, b)
	}
	if changed, err := b.Close(closed.Add(time.Hour)); err != nil || changed || !b.ClosedAt.Equal(closed) {
		t.Errorf("closing twice: %v %v", changed, err)
	}
	if changed, err := b.Reopen(closed.Add(time.Hour)); err != nil || !changed || b.State != domain.BranchOpen || b.ClosedAt != nil {
		t.Fatalf("reopen: %v %v %+v", changed, err, b)
	}
	if changed, err := b.Reopen(t0); err != nil || changed {
		t.Errorf("reopening an open branch: %v %v", changed, err)
	}
	// A push on a closed branch reopens it: CI pushes for a reopened PR
	// before (or without) the webhook saying so.
	_, _ = b.Close(t0)
	reopened, err := b.Push(domain.PushInfo{}, t0.Add(2*time.Hour))
	if err != nil || !reopened || b.State != domain.BranchOpen || b.ClosedAt != nil {
		t.Fatalf("push on closed: %v %v %+v", reopened, err, b)
	}
	merged := t0.Add(3 * time.Hour)
	if changed, err := b.Merge(merged); err != nil || !changed || b.State != domain.BranchMerged || !b.ClosedAt.Equal(merged) {
		t.Fatalf("merge: %v %v %+v", changed, err, b)
	}
	if changed, err := b.Merge(merged.Add(time.Hour)); err != nil || changed {
		t.Errorf("merging twice: %v %v", changed, err)
	}
	// Merged is final.
	if _, err := b.Push(domain.PushInfo{}, merged); !errors.Is(err, domain.ErrBranchMerged) {
		t.Errorf("push on merged: %v", err)
	}
	if _, err := b.Close(merged); !errors.Is(err, domain.ErrBranchMerged) {
		t.Errorf("close merged: %v", err)
	}
	if _, err := b.Reopen(merged); !errors.Is(err, domain.ErrBranchMerged) {
		t.Errorf("reopen merged: %v", err)
	}
}

func TestClosedBranchCanMerge(t *testing.T) {
	// GitHub closes a PR as it merges it; the two webhooks can arrive in
	// either order.
	b := newBranch(t)
	_, _ = b.Close(t0)
	if changed, err := b.Merge(t0.Add(time.Minute)); err != nil || !changed || b.State != domain.BranchMerged {
		t.Errorf("merge closed: %v %v", changed, err)
	}
}

func TestProposalsExpire14DaysAfterClose(t *testing.T) {
	b := newBranch(t)
	if b.ProposalsExpired(t0.Add(365 * 24 * time.Hour)) {
		t.Error("an open branch's proposals expired")
	}
	_, _ = b.Close(t0)
	if b.ProposalsExpired(t0.Add(domain.ProposalRetention - time.Second)) {
		t.Error("expired before 14 days")
	}
	if !b.ProposalsExpired(t0.Add(domain.ProposalRetention)) {
		t.Error("not expired after 14 days")
	}
	// Distinct from Context's retention of builds and captures, whose
	// closed-branch grace is 7 days (RFC 0004 §2.3): this one is about
	// the text, and stays at 14.
	if domain.ProposalRetention != 14*24*time.Hour {
		t.Errorf("ProposalRetention = %v, want 14 days (RFC 0004 §4.1)", domain.ProposalRetention)
	}
}

func TestPreviewURL(t *testing.T) {
	b := newBranch(t)
	if changed, err := b.ReportPreview("https://pr-42.preview.example.com/", t0); err != nil || !changed || b.PreviewURL != "https://pr-42.preview.example.com/" {
		t.Fatalf("preview: %v %v", changed, err)
	}
	if changed, _ := b.ReportPreview("https://pr-42.preview.example.com/", t0); changed {
		t.Error("same URL reported a change")
	}
	for _, bad := range []string{"ftp://x", "not a url", "https://", "/relative", "https://" + strings.Repeat("a", 2001)} {
		if _, err := b.ReportPreview(bad, t0); !errors.Is(err, domain.ErrInvalidPreviewURL) {
			t.Errorf("preview %q: %v", bad, err)
		}
	}
	if changed, err := b.ReportPreview("", t0); err != nil || !changed || b.PreviewURL != "" {
		t.Errorf("clearing the preview: %v %v", changed, err)
	}
}

func TestBranchEventCarriesState(t *testing.T) {
	b := newBranch(t)
	pr := 7
	_, _ = b.Push(domain.PushInfo{HeadCommit: "abcdef1", PR: &pr}, t0)
	e := domain.BranchEventOf(b, ada)
	if e.BranchID != b.ID.String() || e.Name != "feature/checkout" || e.State != "open" || e.PR == nil || *e.PR != 7 ||
		e.HeadCommit != "abcdef1" || e.Version != 2 || e.By != string(ada) {
		t.Errorf("event = %+v", e)
	}
}

func ptr[T any](v T) *T { return &v }
