package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

func waiver(f domain.Finding) domain.Waiver {
	rev := 0
	if f.SourceRevision != nil {
		rev = *f.SourceRevision
	}
	return domain.Waiver{
		ID: uuid.New(), Project: uuid.New(), Fingerprint: f.Fingerprint,
		Reason: "Login is the German term", Scope: domain.WaiverProject,
		SourceRevision: rev, CreatedBy: "u_1", CreatedAt: time.Now(),
	}
}

func TestWaiverNeedsAReason(t *testing.T) {
	f := finding()
	for what, mutate := range map[string]func(*domain.Waiver){
		"no reason":              func(w *domain.Waiver) { w.Reason = "" },
		"whitespace reason":      func(w *domain.Waiver) { w.Reason = "  \n" },
		"no scope":               func(w *domain.Waiver) { w.Scope = "" },
		"branch without a ref":   func(w *domain.Waiver) { w.Scope = domain.WaiverBranch },
		"not a fingerprint":      func(w *domain.Waiver) { w.Fingerprint = "7c1a" },
		"an empty fingerprint":   func(w *domain.Waiver) { w.Fingerprint = "" },
		"a bare prefix":          func(w *domain.Waiver) { w.Fingerprint = domain.FingerprintPrefix },
		"a thousand-and-one why": func(w *domain.Waiver) { w.Reason = string(make([]rune, 1001)) },
	} {
		w := waiver(f)
		mutate(&w)
		if err := w.Validate(); err == nil {
			t.Errorf("%s validated", what)
		}
	}
	if err := waiver(f).Validate(); err != nil {
		t.Errorf("a good waiver did not validate: %v", err)
	}
	w := waiver(f)
	w.Reason = ""
	if !errors.Is(w.Validate(), domain.ErrReasonRequired) {
		t.Error("a reasonless waiver must be ErrReasonRequired")
	}
}

func TestWaiverDiesWhenTheSourceMoves(t *testing.T) {
	f := finding() // source revision 7
	w := waiver(f)
	now := time.Now()
	if !w.Covers(f, "main", now) {
		t.Fatal("the waiver does not cover the finding it was made for")
	}
	moved := f
	nine := 9
	moved.SourceRevision = &nine
	if w.Covers(moved, "main", now) {
		t.Error("the waiver still covers a finding against a newer source revision")
	}
	if got := w.StaleReason(moved); got != "waived against source revision 7, now at 9" {
		t.Errorf("stale reason = %q", got)
	}
}

func TestWaiverScopeAndLife(t *testing.T) {
	f := finding()
	now := time.Now()
	branch := waiver(f)
	branch.Scope, branch.Ref = domain.WaiverBranch, "feat/pay"
	if !branch.Covers(f, "feat/pay", now) || branch.Covers(f, "main", now) {
		t.Error("a branch-scoped waiver must reach its branch and only its branch")
	}
	expired := waiver(f)
	past := now.Add(-time.Hour)
	expired.ExpiresAt = &past
	if expired.Covers(f, "main", now) {
		t.Error("an expired waiver still covers")
	}
	revoked := waiver(f)
	revoked.RevokedAt = &past
	if revoked.Covers(f, "main", now) {
		t.Error("a revoked waiver still covers")
	}
	other := waiver(f)
	other.Fingerprint = domain.FingerprintPrefix + "0000000000000000"
	if other.Covers(f, "main", now) {
		t.Error("a waiver for another fingerprint covers this finding")
	}
}

// A waived finding is still computed, still reported and counted on its
// own — and it can never fail a check.
func TestWaivedFindingsAreReportedAndNeverFail(t *testing.T) {
	f := finding()
	other := domain.New(domain.Finding{
		Layer: domain.LayerCompleteness, Code: checkpolicy.CodeMissingTranslation,
		Severity: domain.Error, Locus: domain.Locus{Key: "checkout.total", Locale: "fr"},
		Message: "missing translation",
	})
	got := domain.Waivers([]domain.Waiver{waiver(f)}, []domain.Finding{f, other}, "main", time.Now())
	if len(got) != 2 {
		t.Fatalf("waiving dropped a finding: %d of 2 left", len(got))
	}
	if got[0].Severity != domain.Waived || got[0].Waiver == "" {
		t.Errorf("the waived finding = %+v", got[0])
	}
	if got[1].Severity != domain.Error {
		t.Errorf("an unwaived finding changed: %+v", got[1])
	}
	for _, p := range []checkpolicy.Policy{{}, {FailOn: checkpolicy.Warning}} {
		counts, conclusion := domain.Conclude(p, got[:1])
		if conclusion != domain.ConclusionSuccess {
			t.Errorf("a waived finding failed the run under fail_on=%q", p.FailOn)
		}
		if counts.Waived != 1 || counts.Errors != 0 || counts.Warnings != 0 || counts.Total() != 1 {
			t.Errorf("counts = %+v; waived is counted on its own", counts)
		}
	}
	counts, conclusion := domain.Conclude(checkpolicy.Policy{}, got)
	if conclusion != domain.ConclusionFailure || counts.Errors != 1 || counts.Waived != 1 {
		t.Errorf("counts = %+v, conclusion = %q", counts, conclusion)
	}
}

func TestCheckRunValidate(t *testing.T) {
	r := domain.CheckRun{
		ID: uuid.New(), Project: uuid.New(), Ref: "main", Trigger: domain.TriggerPullRequest,
		Layers: []domain.Layer{domain.LayerStructure, domain.LayerParity}, StartedAt: time.Now(),
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("a good run did not validate: %v", err)
	}
	bad := r
	bad.Trigger = "cron"
	if !errors.Is(bad.Validate(), domain.ErrUnknownTrigger) {
		t.Error("an unknown trigger validated")
	}
	bad = r
	bad.Layers = []domain.Layer{"arguments"}
	if !errors.Is(bad.Validate(), domain.ErrUnknownLayer) {
		t.Error("an unknown layer validated")
	}
}
