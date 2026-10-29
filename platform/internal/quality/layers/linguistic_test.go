package layers_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The linguistic layer (RFC 0005 §3.8). Its whole job in this package
// is to keep an opinion an opinion: a model's note becomes a warning
// with an identity, and never anything else.

func note(code, quote string) layers.Reviewed {
	return layers.Reviewed{
		Locus: domain.Locus{
			Message: "m_1", Key: "checkout.pay", Locale: "de", Namespace: "checkout",
			Span: &domain.Span{Side: domain.SideTarget, Start: 0, End: len(quote)},
		},
		Code: code, Quote: quote, Explanation: "The register is wrong.",
		Evidence: map[string]any{layers.EvidencePromptVersion: "linguistic/v1"},
	}
}

func TestALinguisticFindingIsAWarningWithAnIdentity(t *testing.T) {
	l := layers.NewLinguistic([]layers.Reviewed{note(layers.CodeToneMismatch, "Zahl jetzt")})
	if l.Layer() != domain.LayerLinguistic {
		t.Fatalf("layer = %q", l.Layer())
	}
	if len(l.Findings) != 1 {
		t.Fatalf("findings = %+v", l.Findings)
	}
	f := l.Findings[0]
	switch {
	case f.Severity != domain.Warning:
		t.Errorf("severity = %q, want warning", f.Severity)
	case f.Fingerprint == "":
		t.Error("the layer left the finding without an identity")
	case f.Schema != domain.Schema:
		t.Errorf("schema = %q", f.Schema)
	case f.Subject != "Zahl jetzt":
		t.Errorf("subject = %q, want the quoted words", f.Subject)
	case f.Message != "The register is wrong.":
		t.Errorf("message = %q", f.Message)
	case f.Locus.Span == nil || f.Locus.Span.Side != domain.SideTarget:
		t.Errorf("span = %+v, want the reviewer's", f.Locus.Span)
	case f.Evidence[layers.EvidencePromptVersion] != "linguistic/v1":
		t.Errorf("evidence = %+v, want the prompt version that produced the opinion", f.Evidence)
	}
	// A check reports what a job computed and computes nothing itself
	// (RFC 0005 §14 decision 2).
	if got := l.Check(&layers.Project{}, checkpolicy.Policy{}); len(got) != 1 || got[0].Fingerprint != f.Fingerprint {
		t.Errorf("Check() = %+v, want the stored findings", got)
	}
}

// RFC 0005 §3.8: a model's opinion is advisory. The kernel refuses a
// policy rule that would raise it, and the layer never hands the grader
// anything to raise in the first place.
func TestTheLayerNeverEmitsAnythingButAWarning(t *testing.T) {
	if !domain.LayerLinguistic.Advisory() {
		t.Fatal("the linguistic layer is not advisory")
	}
	for _, c := range layers.LinguisticCodes {
		l := layers.NewLinguistic([]layers.Reviewed{note(c.Code, "a "+c.Code+" quote")})
		if len(l.Findings) != 1 {
			t.Fatalf("%s: findings = %+v", c.Code, l.Findings)
		}
		if got := l.Findings[0].Severity; got != domain.Warning {
			t.Errorf("%s: severity = %q, want warning", c.Code, got)
		}
		if c.Meaning == "" {
			t.Errorf("%s: no meaning to put in the prompt", c.Code)
		}
	}
	// Even a caller that asks for an error gets a warning: the layer is
	// the last place this can be got wrong quietly.
	r := note(layers.CodeMeaningDivergence, "falsch")
	l := layers.NewLinguistic([]layers.Reviewed{r})
	if got := l.Findings[0].Severity; got != domain.Warning {
		t.Errorf("severity = %q, want warning", got)
	}
}

// The four codes of §3.8, and no fifth. meaning-divergence carries
// semantic QA, which §13 deliberately did not split into a second
// model-backed layer.
func TestTheVocabularyIsTheFourCodes(t *testing.T) {
	want := []string{"meaning-divergence", "tone-mismatch", "grammar-suspected", "inconsistent-phrasing"}
	if len(layers.LinguisticCodes) != len(want) {
		t.Fatalf("codes = %+v, want %v", layers.LinguisticCodes, want)
	}
	for i, w := range want {
		if layers.LinguisticCodes[i].Code != w {
			t.Errorf("code %d = %q, want %q", i, layers.LinguisticCodes[i].Code, w)
		}
		if !layers.KnownLinguisticCode(w) {
			t.Errorf("%q is not known", w)
		}
	}
	for _, no := range []string{"semantic-divergence", "term_missing", "", "Tone-Mismatch"} {
		if layers.KnownLinguisticCode(no) {
			t.Errorf("%q is known, want the vocabulary closed", no)
		}
	}
}

// A note carrying a code the layer does not know is dropped rather than
// emitted under a name no policy can select. The caller has already
// refused it once; this is the lock for a note that comes back from
// storage written by a prompt version since retired.
func TestAnUnknownCodeIsNotEmitted(t *testing.T) {
	l := layers.NewLinguistic([]layers.Reviewed{
		note("semantic-divergence", "etwas"),
		note(layers.CodeGrammarSuspected, "gehen"),
	})
	if len(l.Findings) != 1 || l.Findings[0].Code != layers.CodeGrammarSuspected {
		t.Errorf("findings = %+v, want only the known code", l.Findings)
	}
}

// A suggestion is a hint, never an action (RFC 0005 §9): it rides along
// as a Fix and nothing applies it without a person.
func TestASuggestionBecomesAFixHint(t *testing.T) {
	r := note(layers.CodeToneMismatch, "Zahl jetzt")
	r.Suggestion = "Jetzt bezahlen"
	f := layers.NewLinguistic([]layers.Reviewed{r}).Findings[0]
	if f.Fix == nil || f.Fix.Kind != domain.FixReplace || f.Fix.Hint != "Jetzt bezahlen" {
		t.Errorf("fix = %+v", f.Fix)
	}
	if plain := layers.NewLinguistic([]layers.Reviewed{note(layers.CodeToneMismatch, "Zahl jetzt")}).Findings[0]; plain.Fix != nil {
		t.Errorf("fix = %+v, want none without a suggestion", plain.Fix)
	}
}

// The same opinion about the same words is one finding, however often
// the model repeats it; two opinions about different words are two,
// because the quote is the subject the fingerprint distinguishes on.
func TestRepeatedNotesAreOneFindingAndDistinctWordsAreTwo(t *testing.T) {
	same := layers.NewLinguistic([]layers.Reviewed{
		note(layers.CodeToneMismatch, "Zahl jetzt"),
		note(layers.CodeToneMismatch, "Zahl jetzt"),
	})
	if len(same.Findings) != 1 {
		t.Errorf("findings = %d, want 1", len(same.Findings))
	}
	two := layers.NewLinguistic([]layers.Reviewed{
		note(layers.CodeToneMismatch, "Zahl jetzt"),
		note(layers.CodeToneMismatch, "Du hast"),
	})
	if len(two.Findings) != 2 || two.Findings[0].Fingerprint == two.Findings[1].Fingerprint {
		t.Errorf("findings = %+v, want two identities", two.Findings)
	}
}

// The locus discipline of wave 4: a finding minted over a key is not
// the finding every other surface computes for the same problem, so the
// fingerprint follows the message ID the caller resolved.
func TestTheFingerprintFollowsTheMessageNotTheKey(t *testing.T) {
	byID := layers.NewLinguistic([]layers.Reviewed{note(layers.CodeMeaningDivergence, "falsch")}).Findings[0]
	renamed := note(layers.CodeMeaningDivergence, "falsch")
	renamed.Locus.Key = "checkout.pay_now"
	if got := layers.NewLinguistic([]layers.Reviewed{renamed}).Findings[0]; got.Fingerprint != byID.Fingerprint {
		t.Error("renaming the key changed the fingerprint of a finding that names a message")
	}
	// The span is not part of the identity: an unrelated edit that moves
	// the offending words must not re-open a waiver.
	moved := note(layers.CodeMeaningDivergence, "falsch")
	moved.Locus.Span = &domain.Span{Side: domain.SideTarget, Start: 42, End: 48}
	if got := layers.NewLinguistic([]layers.Reviewed{moved}).Findings[0]; got.Fingerprint != byID.Fingerprint {
		t.Error("moving the span changed the fingerprint")
	}
}
