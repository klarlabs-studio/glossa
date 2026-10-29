package app

import (
	"errors"
	"fmt"
	"slices"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// FindingMaxLengthExceeded matches Localization's own finding: the
// literal text alone is longer than max_length. A warning.
const FindingMaxLengthExceeded mf.FindingCode = "max-length-exceeded"

// FindingAnswerFormat: the model's answer was not the requested JSON.
const FindingAnswerFormat mf.FindingCode = "answer-format"

// Structure is the structural check of a candidate translation (RFC 0002
// §10: a hard gate). Errors block; warnings lower confidence.
type Structure struct {
	Canonical  string       `json:"canonical,omitempty"`
	Errors     []mf.Finding `json:"errors,omitempty"`
	Warnings   []mf.Finding `json:"warnings,omitempty"`
	TextLength int          `json:"text_length"`
}

// Valid reports whether the candidate passes the gate.
func (s Structure) Valid() bool { return len(s.Errors) == 0 }

// MissingPluralCategories lists the target locale's CLDR plural
// categories the candidate only reaches through its catch-all variant
// (CheckCompat's missing-plural-category warnings), without duplicates.
func (s Structure) MissingPluralCategories() []string {
	var out []string
	for _, f := range s.Warnings {
		if f.Code == mf.FindingMissingPluralCategory && f.Detail != "" && !slices.Contains(out, f.Detail) {
			out = append(out, f.Detail)
		}
	}
	return out
}

// NeedsRepair reports whether the candidate goes back to the model: it
// fails the gate, or — policy, though CheckCompat calls it a warning —
// it lacks plural categories the target locale requires (a Polish
// translation with only one/other).
func (s Structure) NeedsRepair() bool { return !s.Valid() || len(s.MissingPluralCategories()) > 0 }

// CheckStructure parses candidate as MF2 and checks it against source for
// the target locale: the kernel's CheckCompat plus max_length.
func CheckStructure(source mf.Message, candidate, targetLocale string, maxLength *int) (Structure, mf.Message) {
	msg, canonical, err := domain.ParseMessage(candidate)
	if err != nil {
		code := mf.CodeSyntaxError
		var mfErr *mf.Error
		if errors.As(err, &mfErr) {
			code = mfErr.Code
		}
		return Structure{Errors: []mf.Finding{{
			Code: mf.FindingInvalidMessage, Severity: mf.SeverityError, Detail: string(code),
			Message: fmt.Sprintf("the translation is not valid MessageFormat 2: %v", err),
		}}}, mf.Message{}
	}
	s := Structure{Canonical: canonical, TextLength: domain.TextLength(msg)}
	for _, f := range mf.CheckCompat(source, msg, targetLocale) {
		if f.Severity == mf.SeverityError {
			s.Errors = append(s.Errors, f)
		} else {
			s.Warnings = append(s.Warnings, f)
		}
	}
	if maxLength != nil && s.TextLength > *maxLength {
		s.Warnings = append(s.Warnings, mf.Finding{
			Code: FindingMaxLengthExceeded, Severity: mf.SeverityWarning,
			Detail:  fmt.Sprintf("%d>%d", s.TextLength, *maxLength),
			Message: fmt.Sprintf("the text is %d characters long before placeholders; the limit is %d", s.TextLength, *maxLength),
		})
	}
	return s, msg
}
