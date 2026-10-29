package domain_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

var (
	t0 = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	de = bcp47.MustParse("de")
	en = bcp47.MustParse("en")

	reviewing = domain.WritePolicy{ReviewRequired: true, Flow: domain.DefaultReviewFlow()}
	trusting  = domain.WritePolicy{ReviewRequired: false, Flow: domain.DefaultReviewFlow()}
)

func parse(t *testing.T, locale bcp47.Tag, syntax mfcontent.Syntax, text string) mfcontent.Content {
	t.Helper()
	c, err := mfcontent.Parse(syntax, text, locale)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return c
}

func human(t *testing.T) domain.Provenance {
	t.Helper()
	p, err := domain.NewProvenance(domain.OriginHuman, nil, "person:1")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, text string, sourceRev int) domain.Write {
	t.Helper()
	return domain.Write{Content: parse(t, de, mfcontent.MF1, text), Provenance: human(t), SourceRevision: sourceRev}
}

func state(s domain.ReviewState) *domain.ReviewState { return &s }

func newTranslation(t *testing.T, policy domain.WritePolicy) (domain.Translation, domain.Revision) {
	t.Helper()
	tr, rev, err := domain.NewTranslation(uuid.New(), uuid.New(), de, write(t, "Jetzt bezahlen", 1), policy, false, true, t0)
	if err != nil {
		t.Fatal(err)
	}
	return tr, rev
}

func TestNewTranslationFollowsPolicy(t *testing.T) {
	tr, rev := newTranslation(t, reviewing)
	if tr.State != domain.StateNeedsReview || tr.Revision != 1 || rev.Number != 1 || rev.Kind != domain.KindContent {
		t.Errorf("reviewing policy: %+v / %+v", tr, rev)
	}
	if tr.Origin != domain.OriginHuman || tr.By != "person:1" || rev.Provenance.Origin != domain.OriginHuman {
		t.Errorf("provenance: %+v", rev.Provenance)
	}
	tr, _ = newTranslation(t, trusting)
	if tr.State != domain.StateApproved {
		t.Errorf("trusting policy: state %s", tr.State)
	}
}

func TestStateOnWrite(t *testing.T) {
	cases := []struct {
		name      string
		policy    domain.WritePolicy
		requested *domain.ReviewState
		canReview bool
		want      domain.ReviewState
		err       error
	}{
		{"draft", reviewing, state(domain.StateDraft), false, domain.StateDraft, nil},
		{"approve needs reviewer", reviewing, state(domain.StateApproved), false, "", domain.ErrReviewForbidden},
		{"reviewer approves on write", reviewing, state(domain.StateApproved), true, domain.StateApproved, nil},
		{"no review required", trusting, state(domain.StateApproved), false, domain.StateApproved, nil},
		{"write never rejects", reviewing, state(domain.StateRejected), true, "", domain.ErrWriteCannotReject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := tc.policy.StateOnWrite(tc.requested, tc.canReview, write(t, "Jetzt bezahlen", 1), false)
			if !errors.Is(err, tc.err) || got != tc.want {
				t.Errorf("= %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
		})
	}
}

// Outdated is derived, never stored: made against an older revision.
func TestOutdatedIsDerivedFromSourceRevision(t *testing.T) {
	tr, _ := newTranslation(t, reviewing)
	if tr.Outdated(1) {
		t.Error("translation of revision 1 is outdated at revision 1")
	}
	if !tr.Outdated(2) {
		t.Error("translation of revision 1 is current at revision 2")
	}
	// Confirming the same text against revision 2 is a content revision
	// and makes it current.
	rev, changed, err := tr.Revise(write(t, "Jetzt bezahlen", 2), reviewing, false, true, t0)
	if err != nil || !changed || rev.Kind != domain.KindContent || rev.SourceRevision != 2 || tr.Outdated(2) {
		t.Errorf("confirm against r2: changed=%v err=%v rev=%+v", changed, err, rev)
	}
}

func TestRevisionNumbering(t *testing.T) {
	tr, _ := newTranslation(t, trusting)
	for i, text := range []string{"Bezahlen", "Zahlen", "Kaufen"} {
		rev, changed, err := tr.Revise(write(t, text, 1), trusting, false, true, t0)
		if err != nil || !changed || rev.Number != i+2 || tr.Revision != i+2 {
			t.Fatalf("revision %d: %+v %v %v", i+2, rev, changed, err)
		}
	}
	// Same text, same source revision, no state request: nothing.
	if _, changed, _ := tr.Revise(write(t, "Kaufen", 1), trusting, false, true, t0); changed || tr.Revision != 4 {
		t.Error("an identical write appended a revision")
	}
}

// New text invalidates an earlier approval.
func TestNewTextResetsApproval(t *testing.T) {
	tr, _ := newTranslation(t, reviewing)
	if _, err := tr.Review(domain.StateApproved, "person:rev", reviewing.Flow, true, true, t0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tr.Revise(write(t, "Bezahlen", 1), reviewing, false, true, t0); err != nil {
		t.Fatal(err)
	}
	if tr.State != domain.StateNeedsReview {
		t.Errorf("state after new text = %s, want needs_review", tr.State)
	}
}

func TestReview(t *testing.T) {
	tr, _ := newTranslation(t, reviewing)
	if _, err := tr.Review(domain.StateApproved, "person:tr", reviewing.Flow, false, true, t0); !errors.Is(err, domain.ErrReviewForbidden) {
		t.Errorf("translator approving: %v", err)
	}
	rev, err := tr.Review(domain.StateApproved, "person:rev", reviewing.Flow, true, true, t0)
	if err != nil || rev.Kind != domain.KindReview || rev.Number != 2 || tr.State != domain.StateApproved {
		t.Fatalf("review: %+v %v", rev, err)
	}
	// The text's provenance stays with the text; the log names the reviewer.
	if tr.Origin != domain.OriginHuman || tr.By != "person:1" || rev.Provenance.By != "person:rev" {
		t.Errorf("provenance after review: %+v / %+v", tr, rev.Provenance)
	}
	if _, err := tr.Review(domain.StateApproved, "person:rev", reviewing.Flow, true, true, t0); !errors.Is(err, domain.ErrTransition) {
		t.Errorf("approving twice: %v", err)
	}
	// A state change requested through a write of the same text is a review.
	rev, changed, err := tr.Revise(domain.Write{
		Content: tr.Content, Provenance: human(t), SourceRevision: 1, State: state(domain.StateDraft),
	}, reviewing, false, true, t0)
	if err != nil || !changed || rev.Kind != domain.KindReview || tr.State != domain.StateDraft {
		t.Errorf("state change via write: %+v %v %v", rev, changed, err)
	}
}

func TestProvenanceDetail(t *testing.T) {
	p, err := domain.NewProvenance(domain.OriginAI, json.RawMessage(`{"model":"x","prompt_version":"3"}`), "token:1")
	if err != nil || string(p.Detail) == "{}" {
		t.Errorf("detail: %v %s", err, p.Detail)
	}
	for _, bad := range []string{`[]`, `"x"`, `null`, `{`} {
		if _, err := domain.NewProvenance(domain.OriginAI, json.RawMessage(bad), "x"); !errors.Is(err, domain.ErrInvalidOriginInfo) {
			t.Errorf("detail %s: %v", bad, err)
		}
	}
	if _, err := domain.ParseOrigin("crowd", domain.OriginHuman); !errors.Is(err, domain.ErrInvalidOrigin) {
		t.Errorf("origin crowd: %v", err)
	}
	if o, _ := domain.ParseOrigin("", domain.OriginImport); o != domain.OriginImport {
		t.Errorf("default origin = %s", o)
	}
}

// TestAgentIsAnOriginOfItsOwn: an autonomous agent writing through a
// long-lived token (RFC 0005 §7.3) is not a person asking for a machine
// translation, so it gets an origin of its own rather than `ai` plus a
// JSON detail nobody can filter on. Provenance is state that matters,
// and state that matters is legible.
func TestAgentIsAnOriginOfItsOwn(t *testing.T) {
	o, err := domain.ParseOrigin("agent", domain.OriginHuman)
	if err != nil {
		t.Fatalf(`ParseOrigin("agent"): %v`, err)
	}
	if o != domain.OriginAgent {
		t.Fatalf(`ParseOrigin("agent") = %q, want %q`, o, domain.OriginAgent)
	}
	if string(domain.OriginAgent) != "agent" || string(domain.OriginAI) != "ai" {
		t.Fatalf("agent = %q, ai = %q: the two must stay distinct on the wire",
			domain.OriginAgent, domain.OriginAI)
	}
	p, err := domain.NewProvenance(domain.OriginAgent,
		json.RawMessage(`{"via":"mcp","tool":"translation_propose"}`), "token:1")
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if p.Origin != domain.OriginAgent {
		t.Errorf("provenance origin = %q, want agent", p.Origin)
	}
}

// The structural QA gate: error findings reject, warnings are kept.
func TestStructuralQAGate(t *testing.T) {
	source := parse(t, en, mfcontent.MF1, "{count, plural, one {# item for {name}} other {# items for {name}}}")

	good := parse(t, de, mfcontent.MF1, "{count, plural, one {# Artikel für {name}} other {# Artikel für {name}}}")
	if r := domain.CheckStructure(source, good, de, nil); len(r.Errors) != 0 || r.Gate() != nil {
		t.Errorf("good translation: %+v", r)
	}

	missing := parse(t, de, mfcontent.MF1, "{count, plural, one {# Artikel} other {# Artikel}}")
	r := domain.CheckStructure(source, missing, de, nil)
	var qa *domain.QAError
	if !errors.As(r.Gate(), &qa) || !hasCode(qa.Findings, mf.FindingMissingArgument) {
		t.Errorf("missing argument: %+v", r)
	}
	if _, _, err := domain.NewTranslation(uuid.New(), uuid.New(), de,
		domain.Write{Content: missing, Provenance: human(t), SourceRevision: 1, QA: r}, reviewing, true, true, t0); !errors.As(err, &qa) {
		t.Errorf("NewTranslation accepted an incompatible text: %v", err)
	}

	limit := 5
	long := parse(t, de, mfcontent.MF1, "{count, plural, one {# Artikel für {name}} other {# Artikel für {name}}}")
	r = domain.CheckStructure(source, long, de, &limit)
	if r.Gate() != nil || !hasCode(r.Warnings, domain.FindingMaxLengthExceeded) {
		t.Errorf("max length: %+v", r)
	}
	tr, rev, err := domain.NewTranslation(uuid.New(), uuid.New(), de,
		domain.Write{Content: long, Provenance: human(t), SourceRevision: 1, QA: r}, reviewing, false, true, t0)
	if err != nil || len(tr.Warnings) != 1 || len(rev.Findings) != 1 {
		t.Errorf("warnings not kept: %v %+v", err, tr.Warnings)
	}
}

func hasCode(fs []mf.Finding, code mf.FindingCode) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}
