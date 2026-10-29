package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// TermFindingCode is a stable terminology QA code (intent §29.3).
type TermFindingCode string

// Terminology QA codes.
const (
	// FindingTermMissing: a concept was recognized in the source, but the
	// translation uses none of the target locale's preferred or admitted
	// terms for it.
	FindingTermMissing TermFindingCode = "term_missing"
	// FindingTermForbidden: the translation uses a forbidden or
	// deprecated target term.
	FindingTermForbidden TermFindingCode = "term_forbidden"
)

// Severity is how much a finding matters.
type Severity string

// Severities. term_forbidden is an error for forbidden terms and a
// warning for deprecated ones; term_missing is a warning, because
// recognition tolerates inflection but not rephrasing, so a correct
// translation can still be flagged.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Side says which text a finding's span points into.
type Side string

// Sides.
const (
	SideSource Side = "source"
	SideTarget Side = "target"
)

// TermFinding is one terminology QA finding.
type TermFinding struct {
	Code      TermFindingCode
	Severity  Severity
	ConceptID uuid.UUID
	// TermID is the term recognized: the source term for term_missing,
	// the offending target term for term_forbidden.
	TermID uuid.UUID
	Side   Side
	// Start and End delimit Text in the source or target (bytes).
	Start int
	End   int
	Text  string
	// Suggestions are the concept's allowed target terms, preferred
	// first.
	Suggestions []string
	Message     string
}

// CheckTerminology checks a translation against the termbase
// (RFC 0003 §2.2): every concept recognized in the source needs one of
// its allowed target terms in the target (term_missing, one finding per
// concept, at its first occurrence), and every forbidden or deprecated
// target term used is reported (term_forbidden) — unless the same words
// are an allowed term of a concept the source mentions (a homonym used
// correctly). A concept without allowed terms in the target locale
// requires nothing. Findings come source first, then target, by
// position. The check is deterministic.
func CheckTerminology(tb *Termbase, source string, sourceLocale bcp47.Tag, target string, targetLocale bcp47.Tag) []TermFinding {
	var out []TermFinding
	inSource := map[uuid.UUID]bool{}
	for _, h := range tb.Recognize(source, sourceLocale) {
		if inSource[h.ConceptID] {
			continue
		}
		inSource[h.ConceptID] = true
		c, _ := tb.Concept(h.ConceptID)
		allowed := allowedTerms(c, targetLocale)
		if len(allowed) == 0 {
			continue
		}
		used := tb.recognize(target, targetLocale, func(t Term) bool { return t.Status.Allowed() && hasTerm(allowed, t) })
		if len(used) > 0 {
			continue
		}
		out = append(out, TermFinding{
			Code: FindingTermMissing, Severity: SeverityWarning, ConceptID: c.ID, TermID: h.Term.ID,
			Side: SideSource, Start: h.Start, End: h.End, Text: h.Text, Suggestions: texts(allowed),
			Message: fmt.Sprintf("%q is a termbase concept; translate it as %s", h.Text, quoteAll(texts(allowed))),
		})
	}
	hits := tb.Recognize(target, targetLocale)
	for _, h := range hits {
		if h.Term.Status.Allowed() || correctHomonym(hits, h, inSource) {
			continue
		}
		c, _ := tb.Concept(h.ConceptID)
		allowed := texts(allowedTerms(c, targetLocale))
		sev, what := SeverityError, "forbidden"
		if h.Term.Status == TermDeprecated {
			sev, what = SeverityWarning, "deprecated"
		}
		msg := fmt.Sprintf("%q is a %s term", h.Text, what)
		if len(allowed) > 0 {
			msg += "; use " + quoteAll(allowed)
		}
		out = append(out, TermFinding{
			Code: FindingTermForbidden, Severity: sev, ConceptID: c.ID, TermID: h.Term.ID,
			Side: SideTarget, Start: h.Start, End: h.End, Text: h.Text, Suggestions: allowed, Message: msg,
		})
	}
	return out
}

// correctHomonym reports whether another hit on h's span is an allowed
// term of a concept the source mentions.
func correctHomonym(hits []TermHit, h TermHit, inSource map[uuid.UUID]bool) bool {
	for _, o := range hits {
		if o.Start == h.Start && o.End == h.End && o.Term.Status.Allowed() && inSource[o.ConceptID] {
			return true
		}
	}
	return false
}

// allowedTerms returns c's preferred, then admitted terms for locale, in
// termbase order.
func allowedTerms(c Concept, locale bcp47.Tag) []Term {
	var preferred, admitted []Term
	for _, t := range c.TermsIn(locale) {
		switch t.Status {
		case TermPreferred:
			preferred = append(preferred, t)
		case TermAdmitted:
			admitted = append(admitted, t)
		}
	}
	return append(preferred, admitted...)
}

func hasTerm(ts []Term, t Term) bool {
	for _, x := range ts {
		if x.ID == t.ID {
			return true
		}
	}
	return false
}

func texts(ts []Term) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Text
	}
	return out
}

func quoteAll(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, " or ")
}
