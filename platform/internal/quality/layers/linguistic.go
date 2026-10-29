package layers

import (
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The linguistic layer (RFC 0005 §3.8): the one layer a model decides.
//
// Like the visual layer, it computes nothing here. A model call is a
// job — explicit or over a batch, never a check (RFC 0005 §14
// decision 2) — and what is left in this package is the part that must
// not live wherever the call happens: the identity of a finding, its
// locus, its vocabulary, and the one rule the layer exists to keep.
//
// That rule is that a finding here is **advisory**. It is a warning,
// always, and a policy may not raise it (checkpolicy.AdvisoryLayers
// refuses the rule, and checkpolicy.Target clamps a decision that got
// through anyway). This layer's own contribution is narrower and
// blunter: it never mints anything but a warning, so no caller can hand
// the grader an error to clamp in the first place. A build does not
// fail on an opinion, so an opinion never arrives dressed as a fact.
//
// `meaning-divergence` folds in what intent §29 calls semantic QA. RFC
// 0005 §13 defers splitting the two into separate layers until there is
// eval evidence for the split, so there is one model-backed layer here
// and not two.

// The layer's codes. They are the whole vocabulary: the prompt lists
// exactly these, the answer schema constrains the model to them, and a
// note with any other code is a malformed answer, not a new code.
const (
	// CodeMeaningDivergence: the translation says something the source
	// does not, or leaves out something it does. It carries semantic QA.
	CodeMeaningDivergence = "meaning-divergence"
	// CodeToneMismatch: the register, form of address or tone
	// contradicts the style guide or the source.
	CodeToneMismatch = "tone-mismatch"
	// CodeGrammarSuspected: the target text is ungrammatical or reads as
	// a machine rendering rather than as the language.
	CodeGrammarSuspected = "grammar-suspected"
	// CodeInconsistentPhrasing: the same idea is worded differently from
	// how this product words it elsewhere.
	CodeInconsistentPhrasing = "inconsistent-phrasing"
)

// LinguisticCode is one code with what it means. The meaning is prompt
// material: it is what the model is told the code covers, so the
// vocabulary the layer grades and the vocabulary the model is given
// cannot drift apart.
type LinguisticCode struct {
	Code    string
	Meaning string
}

// LinguisticCodes are the four codes of RFC 0005 §3.8, in report order.
var LinguisticCodes = []LinguisticCode{
	{
		Code: CodeMeaningDivergence,
		Meaning: "the translation states something the source does not, omits something the source states, " +
			"or changes what the message asks the reader to do — including a subtly different meaning that reads fluently",
	},
	{
		Code:    CodeToneMismatch,
		Meaning: "the register, form of address or tone contradicts the style guide, or is markedly different from the source's",
	},
	{
		Code:    CodeGrammarSuspected,
		Meaning: "the target text is ungrammatical, mis-agreeing or unidiomatic in a way a native speaker would not write",
	},
	{
		Code:    CodeInconsistentPhrasing,
		Meaning: "the same idea is worded differently here from the way the rest of this product words it",
	},
}

// KnownLinguisticCode reports whether code is one of the four.
func KnownLinguisticCode(code string) bool {
	for _, c := range LinguisticCodes {
		if c.Code == code {
			return true
		}
	}
	return false
}

// Reviewed is one note a model returned about one translation, as the
// caller that made the call parsed it: the code, where in the text it
// points, what it says and what it would write instead.
//
// It is the linguistic layer's equivalent of Probed. A model no more
// computes a fingerprint than a browser does — it holds a message and
// not the catalog — so the locus arrives as far as the caller could
// fill it and NewLinguistic mints the identity from it. The caller
// resolves the message ID before it seals, for the reason Probed gives:
// the fingerprint is hashed over it, and a fingerprint over a key is
// not the one another surface computes for the same finding.
type Reviewed struct {
	// Locus locates the note. Message, Key, Locale, Namespace, Revision
	// and Span are the caller's; the context fields are Context's, at
	// report time, like every other layer's.
	Locus domain.Locus
	// Code is one of LinguisticCodes.
	Code string
	// Quote is the text the span covers, verbatim. It is the finding's
	// subject, so two tone slips in one message are two findings.
	Quote string
	// Explanation is the model's sentence about what is wrong.
	Explanation string
	// Suggestion is the text the model would write instead; empty when
	// it offered none.
	Suggestion string
	// SourceRevision is the source revision the review ran against.
	SourceRevision *int
	// Evidence is what the run recorded about the call: the prompt
	// version, the provider and the model (see the Evidence* keys).
	Evidence map[string]any
}

// The evidence keys a linguistic finding carries. They say which prompt
// and which model produced the opinion, which is what makes a stored
// finding re-readable after the prompt moves on: a finding from
// `linguistic/v1` is not silently re-attributed to `v2`.
const (
	EvidencePromptVersion = "prompt_version"
	EvidenceProvider      = "provider"
	EvidenceModel         = "model"
)

// Linguistic is the linguistic layer as a Checker: findings a job
// computed and stored, reported with everything else. A check reports
// them and never computes them (RFC 0005 §14 decision 2), which is what
// keeps `glossa check` offline, deterministic and free.
type Linguistic struct {
	Findings []domain.Finding
}

// Layer implements Checker.
func (Linguistic) Layer() domain.Layer { return domain.LayerLinguistic }

// Check implements Checker. It reads neither the project nor the
// policy: the opinions are the model's and the grading is the policy's.
func (l Linguistic) Check(*Project, checkpolicy.Policy) []domain.Finding { return l.Findings }

// NewLinguistic seals reviewed notes into the layer.
//
// Every finding comes out a warning, whatever the caller put in the
// note, because this is the layer that may never fail a build. A
// suggestion becomes a Fix hint, which nothing applies without a person
// or an explicit --fix.
//
// A note whose code is not one of the four is dropped rather than
// emitted under a code no policy can select and no dashboard can name.
// The caller that parsed the answer has already refused such a note —
// an unknown code is a malformed answer there — so this is the second
// lock, for notes that come back from storage written by a prompt
// version that has since been retired.
//
// Two notes that seal to one fingerprint are one finding: a model that
// says the same thing about the same words twice has said it once.
func NewLinguistic(rs []Reviewed) Linguistic {
	out := make([]domain.Finding, 0, len(rs))
	seen := map[string]bool{}
	for _, r := range rs {
		if !KnownLinguisticCode(r.Code) {
			continue
		}
		f := domain.New(domain.Finding{
			Layer:          domain.LayerLinguistic,
			Code:           r.Code,
			Severity:       domain.Warning,
			Locus:          r.Locus,
			Message:        r.Explanation,
			Subject:        r.Quote,
			Evidence:       r.Evidence,
			SourceRevision: r.SourceRevision,
		})
		if r.Suggestion != "" {
			f.Fix = &domain.Fix{Kind: domain.FixReplace, Hint: r.Suggestion}
		}
		if seen[f.Fingerprint] {
			continue
		}
		seen[f.Fingerprint] = true
		out = append(out, f)
	}
	return Linguistic{Findings: out}
}
