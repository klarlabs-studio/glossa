package domain_test

import (
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

var saveAt = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// TestNextPolicyNumbersTheVersionAndPinsThePrevious: saving a document
// is not storing what the caller sent. The version is the server's, it
// is one past the one it replaces, and the document it replaces stays
// behind it so the pull requests that predate the save keep grading
// against what they were opened under (RFC 0005 §4.3).
func TestNextPolicyNumbersTheVersionAndPinsThePrevious(t *testing.T) {
	current := checkpolicy.Policy{Schema: checkpolicy.Schema, Version: 6, FailOn: checkpolicy.Error}
	candidate := checkpolicy.Policy{
		FailOn: checkpolicy.Warning,
		Rules:  []checkpolicy.Rule{{Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Error}},
	}

	next, err := domain.NextPolicy(current, candidate, saveAt, 14*24*time.Hour)
	if err != nil {
		t.Fatalf("NextPolicy: %v", err)
	}
	if next.Version != 7 {
		t.Errorf("version = %d, want 7", next.Version)
	}
	if next.Schema != checkpolicy.Schema {
		t.Errorf("schema = %q, want %q", next.Schema, checkpolicy.Schema)
	}
	if next.EffectiveFrom == nil || !next.EffectiveFrom.Equal(saveAt) {
		t.Errorf("effective_from = %v, want %v", next.EffectiveFrom, saveAt)
	}
	if next.GraceUntil == nil || !next.GraceUntil.Equal(saveAt.Add(14*24*time.Hour)) {
		t.Errorf("grace_until = %v, want %v", next.GraceUntil, saveAt.Add(14*24*time.Hour))
	}
	if next.Previous == nil || next.Previous.Version != 6 {
		t.Fatalf("previous = %+v, want the version it replaced", next.Previous)
	}
	if next.Previous.Previous != nil {
		t.Error("the history is one version deep in the document; the record is the version table")
	}
	// The candidate's own content survives the bookkeeping.
	if next.FailOn != checkpolicy.Warning || len(next.Rules) != 1 {
		t.Errorf("next = %+v, want the candidate's fail_on and rule", next)
	}
}

// TestNextPolicyWithoutGracePinsNothing: a policy that only loosens
// wants every pull request graded by it at once.
func TestNextPolicyWithoutGracePinsNothing(t *testing.T) {
	next, err := domain.NextPolicy(checkpolicy.Policy{Version: 2}, checkpolicy.Policy{}, saveAt, 0)
	if err != nil {
		t.Fatalf("NextPolicy: %v", err)
	}
	if next.Previous != nil || next.GraceUntil != nil {
		t.Errorf("next pinned with a zero grace: previous = %+v, grace_until = %v", next.Previous, next.GraceUntil)
	}
	if next.Version != 3 {
		t.Errorf("version = %d, want 3", next.Version)
	}
}

// TestNextPolicyRefusesTheServersOwnBookkeeping: version,
// effective_from, grace_until and previous are the server's answer to
// "when did this become true", not something a caller may assert. A
// write that set them could rewrite history or unpin an open pull
// request.
func TestNextPolicyRefusesTheServersOwnBookkeeping(t *testing.T) {
	at := saveAt
	cases := map[string]checkpolicy.Policy{
		"version":        {Version: 12},
		"effective_from": {EffectiveFrom: &at},
		"grace_until":    {GraceUntil: &at},
		"previous":       {Previous: &checkpolicy.Policy{}},
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := domain.NextPolicy(checkpolicy.Policy{}, candidate, saveAt, 0); !errors.Is(err, domain.ErrPolicyBookkeeping) {
				t.Errorf("NextPolicy with %s = %v, want ErrPolicyBookkeeping", name, err)
			}
		})
	}
}

// TestNextPolicyRefusesAnInvalidDocument: the evaluator's own rules
// hold on the way in, so a document that could never be graded is
// never stored.
func TestNextPolicyRefusesAnInvalidDocument(t *testing.T) {
	candidate := checkpolicy.Policy{
		Rules: []checkpolicy.Rule{{Selector: checkpolicy.Selector{Layer: "linguistic"}, Severity: checkpolicy.Error}},
	}
	if _, err := domain.NextPolicy(checkpolicy.Policy{}, candidate, saveAt, 0); !errors.Is(err, checkpolicy.ErrAdvisoryLayer) {
		t.Errorf("NextPolicy = %v, want ErrAdvisoryLayer: a build never fails on an opinion", err)
	}
}

// TestNextPolicyBoundsTheGrace: a grace nobody bounded is a policy that
// never takes effect.
func TestNextPolicyBoundsTheGrace(t *testing.T) {
	for _, grace := range []time.Duration{-time.Hour, domain.MaxGrace + time.Hour} {
		if _, err := domain.NextPolicy(checkpolicy.Policy{}, checkpolicy.Policy{}, saveAt, grace); !errors.Is(err, domain.ErrInvalidGrace) {
			t.Errorf("NextPolicy with grace %v = %v, want ErrInvalidGrace", grace, err)
		}
	}
}
