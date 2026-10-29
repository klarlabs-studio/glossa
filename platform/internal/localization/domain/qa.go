package domain

import (
	"fmt"
	"slices"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// FindingMaxLengthExceeded is Localization's own structural finding: a
// variant's literal text alone is longer than the message's max_length.
// It is a warning, because placeholders and formatting decide the final
// length.
const FindingMaxLengthExceeded mf.FindingCode = "max-length-exceeded"

// QAResult is the structural check of a translation (intent §29.1).
type QAResult struct {
	Errors   []mf.Finding
	Warnings []mf.Finding
}

// CheckStructure checks translation against the source it was made
// from, for locale: the MessageFormat kernel's compatibility rules
// (arguments, selectors, plural categories, markup) plus max_length.
func CheckStructure(source, translation mfcontent.Content, locale bcp47.Tag, maxLength *int) QAResult {
	var r QAResult
	for _, f := range mf.CheckCompat(source.Model, translation.Model, locale.String()) {
		if f.Severity == mf.SeverityError {
			r.Errors = append(r.Errors, f)
		} else {
			r.Warnings = append(r.Warnings, f)
		}
	}
	if maxLength != nil {
		if longest := slices.Max(append(translation.TextLengths(), 0)); longest > *maxLength {
			r.Warnings = append(r.Warnings, mf.Finding{
				Code: FindingMaxLengthExceeded, Severity: mf.SeverityWarning,
				Detail:  fmt.Sprintf("%d>%d", longest, *maxLength),
				Message: fmt.Sprintf("the text is %d characters long before placeholders; the limit is %d", longest, *maxLength),
			})
		}
	}
	return r
}

// Gate refuses a translation with error-severity findings.
func (r QAResult) Gate() error {
	if len(r.Errors) == 0 {
		return nil
	}
	return &QAError{Findings: r.All()}
}

// All returns errors then warnings.
func (r QAResult) All() []mf.Finding { return slices.Concat(r.Errors, r.Warnings) }
