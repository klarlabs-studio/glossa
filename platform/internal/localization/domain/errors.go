package domain

import (
	"errors"
	"fmt"

	mf "go.klarlabs.de/glossa/messageformat"
)

// Domain errors. The HTTP adapter maps each to a problem code.
var (
	ErrInvalidID          = errors.New("localization: invalid id")
	ErrSourceLocale       = errors.New("localization: the source locale is written through the catalog, not translated")
	ErrInvalidOrigin      = errors.New("localization: origin must be human, ai, agent, translation_memory, machine_translation, import or adaptation")
	ErrInvalidOriginInfo  = errors.New("localization: origin_detail must be a JSON object of at most 16 KiB")
	ErrInvalidReviewState = errors.New("localization: state must be draft, needs_review, approved or rejected")
	ErrWriteCannotReject  = errors.New("localization: a write can't reject; review the translation instead")
	ErrReviewForbidden    = errors.New("localization: approving or rejecting needs the review permission for this locale")
	ErrOwnText            = errors.New("localization: the author of a text cannot approve or reject it; ask another reviewer")
	ErrTransition         = errors.New("localization: that review state change is not allowed")
	ErrInvalidSourceRev   = errors.New("localization: source_revision must be between 1 and the message's current revision")
)

// FallbackError explains why a fallback graph was refused. Code is
// stable (fallback_unknown_locale, fallback_self_reference,
// fallback_duplicate, fallback_cycle, fallback_invalid_locale).
type FallbackError struct {
	Code   string
	Locale string
	Detail string
}

func (e *FallbackError) Error() string {
	return fmt.Sprintf("localization: fallback graph: %s (%s): %s", e.Code, e.Locale, e.Detail)
}

// QAError rejects a translation with error-severity structural findings
// (intent §29.1). Findings holds every finding, warnings included.
type QAError struct {
	Findings []mf.Finding
}

func (e *QAError) Error() string {
	return fmt.Sprintf("localization: translation is structurally incompatible with its source (%d findings)", len(e.Findings))
}
