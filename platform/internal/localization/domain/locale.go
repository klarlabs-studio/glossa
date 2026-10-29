// Package domain is the Localization context's model: what each locale
// says (RFC 0002 §4). A project's Locales and its FallbackGraph decide
// what it translates into and how runtimes fall back; a Translation is a
// message's current text in one locale, projected from an append-only
// log of TranslationRevisions that each carry provenance (intent §22,
// §44). Messages themselves belong to Catalog: this context refers to
// them by ID and to the source revision a translation was made against.
package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Locale is a language a project is localized into, or its source
// locale. Its identity is the canonical tag within the project.
type Locale struct {
	ProjectID uuid.UUID
	Code      bcp47.Tag
	// IsSource marks the project's source locale, which exists from the
	// project's creation, is written through the catalog and can't be
	// removed.
	IsSource  bool
	CreatedAt time.Time
}

// Direction is the locale's text direction, derived from its likely
// script.
func (l Locale) Direction() bcp47.Direction { return l.Code.Direction() }

// NewLocale adds code to project.
func NewLocale(project uuid.UUID, code bcp47.Tag, isSource bool, now time.Time) Locale {
	return Locale{ProjectID: project, Code: code, IsSource: isSource, CreatedAt: now}
}

// TranslationID identifies a translation (one message in one locale).
type TranslationID uuid.UUID

// NewTranslationID returns a fresh, time-ordered ID.
func NewTranslationID() TranslationID { return TranslationID(uuid.Must(uuid.NewV7())) }

// ParseTranslationID parses the canonical string form.
func ParseTranslationID(s string) (TranslationID, error) {
	u, err := uuid.Parse(s)
	if err != nil || u == uuid.Nil {
		return TranslationID{}, fmt.Errorf("%w: %q", ErrInvalidID, s)
	}
	return TranslationID(u), nil
}

func (id TranslationID) String() string  { return uuid.UUID(id).String() }
func (id TranslationID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id TranslationID) IsZero() bool    { return uuid.UUID(id) == uuid.Nil }
