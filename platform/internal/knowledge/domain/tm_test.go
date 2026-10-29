package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

var now = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

func reconcile(t *testing.T, active *domain.TMUnit, cur domain.ApprovedText, isApproved bool) domain.Derivation {
	t.Helper()
	d, err := domain.Reconcile(active, cur, isApproved, now)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func approved(t *testing.T, revision int, source, target string) domain.ApprovedText {
	t.Helper()
	return domain.ApprovedText{
		TranslationID: uuid.MustParse("0192a1b2-0000-7000-8000-000000000001"),
		ProjectID:     uuid.MustParse("0192a1b2-0000-7000-8000-0000000000aa"),
		MessageID:     uuid.MustParse("0192a1b2-0000-7000-8000-0000000000bb"),
		Revision:      revision, MessageKey: "checkout.pay", Namespace: "default",
		SourceLocale: bcp47.MustParse("en"), TargetLocale: bcp47.MustParse("de"),
		Source: mf2(t, source), Target: mf2(t, target), By: "person:x",
	}
}

func TestReconcileCreatesAUnitForAnApproval(t *testing.T) {
	a := approved(t, 1, "Pay {$amount}", "{$amount} zahlen")
	d := reconcile(t, nil, a, true)
	if d.Create == nil || d.Retire != "" || d.Touch {
		t.Fatalf("decision = %+v", d)
	}
	u := d.Create
	if u.Origin != domain.OriginTranslation || u.TranslationRevision != 1 || u.ProjectID == nil || *u.ProjectID != a.ProjectID {
		t.Errorf("unit = %+v", u)
	}
	if u.SourceNorm.Text != "Pay {1}" || u.TargetNorm.Text != "{1} zahlen" || u.TargetMF2 != "{$amount} zahlen" {
		t.Errorf("unit text = %q → %q (%q)", u.SourceNorm.Text, u.TargetNorm.Text, u.TargetMF2)
	}
	if !u.Active() {
		t.Error("a new unit is active")
	}
}

func TestReconcileLifecycle(t *testing.T) {
	first := reconcile(t, nil, approved(t, 1, "Pay {$amount}", "{$amount} zahlen"), true).Create

	// Re-approving the same text (a review revision) only touches it.
	same := reconcile(t, first, approved(t, 3, "Pay {$amount}", "{$amount} zahlen"), true)
	if same.Create != nil || same.Retire != "" || !same.Touch {
		t.Errorf("same text: %+v", same)
	}
	// New approved text supersedes the unit.
	changed := reconcile(t, first, approved(t, 4, "Pay {$amount}", "Jetzt {$amount} zahlen"), true)
	if changed.Retire != domain.RetireSuperseded || changed.Create == nil {
		t.Errorf("new approved text: %+v", changed)
	}
	// Taking the approval back retires it; overwriting it with new,
	// unapproved text too, with its own reason.
	if d := reconcile(t, first, approved(t, 5, "Pay {$amount}", "{$amount} zahlen"), false); d.Retire != domain.RetireUnapproved || d.Create != nil {
		t.Errorf("unapproved: %+v", d)
	}
	if d := reconcile(t, first, approved(t, 5, "Pay {$amount}", "Zahlen"), false); d.Retire != domain.RetireOverwritten {
		t.Errorf("overwritten: %+v", d)
	}
	// Nothing active and nothing approved: nothing to do.
	if d := reconcile(t, nil, approved(t, 2, "Pay {$amount}", "Zahlen"), false); d.Create != nil || d.Retire != "" || d.Touch {
		t.Errorf("no-op: %+v", d)
	}
	// A new source revision the same text was re-approved against is new
	// knowledge too.
	if d := reconcile(t, first, approved(t, 6, "Pay {$amount} now", "{$amount} zahlen"), true); d.Retire != domain.RetireSuperseded || d.Create == nil {
		t.Errorf("new source: %+v", d)
	}
}

func TestRetireKeepsTheUnitAsHistory(t *testing.T) {
	u := reconcile(t, nil, approved(t, 1, "Save", "Speichern"), true).Create
	if err := u.Retire(domain.RetireDeleted, "person:y", now); err != nil {
		t.Fatal(err)
	}
	if u.Active() || u.RetiredReason != domain.RetireDeleted || u.RetiredBy != "person:y" || u.RetiredAt == nil {
		t.Errorf("retired unit = %+v", u)
	}
	if err := u.Retire(domain.RetireDeleted, "person:y", now); !errors.Is(err, domain.ErrUnitRetired) {
		t.Errorf("retiring twice: %v", err)
	}
}

func TestFuzzyScore(t *testing.T) {
	tests := []struct {
		similarity float64
		want       int
	}{
		{1, 99}, {0.999, 99}, {0.87, 87}, {0.5, 50}, {0.499, 49}, {0, 0},
	}
	for _, tc := range tests {
		if got := domain.FuzzyScore(tc.similarity); got != tc.want {
			t.Errorf("FuzzyScore(%v) = %d, want %d", tc.similarity, got, tc.want)
		}
	}
}

func TestNewImportedUnit(t *testing.T) {
	project := uuid.MustParse("0192a1b2-0000-7000-8000-0000000000aa")
	en, de := bcp47.MustParse("en"), bcp47.MustParse("de")
	u, err := domain.NewImportedUnit(domain.ImportedText{
		ProjectID: &project, SourceLocale: en, TargetLocale: de,
		Source: mf2(t, "Pay {$amount}"), Target: mf2(t, "{$amount} zahlen"),
	}, "person:x", now)
	if err != nil {
		t.Fatal(err)
	}
	if u.Origin != domain.OriginImport || u.TranslationID != nil || u.MessageID != nil || *u.ProjectID != project {
		t.Errorf("provenance = %+v", u)
	}
	if u.SourceNorm.Text != "Pay {1}" || u.TargetMF2 != "{$amount} zahlen" || u.CreatedBy != "person:x" || !u.Active() {
		t.Errorf("unit = %+v", u)
	}
	for name, bad := range map[string]domain.ImportedText{
		"same locales": {SourceLocale: en, TargetLocale: en, Source: mf2(t, "a"), Target: mf2(t, "b")},
		"no locale":    {TargetLocale: de, Source: mf2(t, "a"), Target: mf2(t, "b")},
		"empty source": {SourceLocale: en, TargetLocale: de, Source: mf2(t, ""), Target: mf2(t, "b")},
		"empty target": {SourceLocale: en, TargetLocale: de, Source: mf2(t, "a"), Target: mf2(t, "")},
		"too long":     {SourceLocale: en, TargetLocale: de, Source: mf2(t, "a"), Target: mf2(t, strings.Repeat("x", domain.MaxUnitTextBytes+1))},
	} {
		if _, err := domain.NewImportedUnit(bad, "person:x", now); !errors.Is(err, domain.ErrInvalidUnit) {
			t.Errorf("%s: err = %v, want ErrInvalidUnit", name, err)
		}
	}
}

func TestMatchScoreRanks(t *testing.T) {
	if !(domain.ScoreContext > domain.ScoreExact && domain.ScoreExact > domain.MaxFuzzyScore && domain.MinFuzzyScore == 50) {
		t.Error("101 > 100 > 99 ≥ fuzzy ≥ 50")
	}
}
