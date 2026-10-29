package domain

import (
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Match scores (RFC 0003 §2.1).
const (
	// ScoreContext is an exact match whose context matches too: the unit
	// was approved for the same message key in the same namespace.
	ScoreContext = 101
	// ScoreExact is the same normalized text with the same placeholder
	// signature.
	ScoreExact = 100
	// MaxFuzzyScore caps fuzzy matches below exact ones: a fuzzy match
	// with identical text differs in its placeholders' types.
	MaxFuzzyScore = 99
	// MinFuzzyScore is the lowest fuzzy score worth returning.
	MinFuzzyScore = 50
)

// MatchKind says how a TM unit matched.
type MatchKind string

// Match kinds.
const (
	MatchContext MatchKind = "context"
	MatchExact   MatchKind = "exact"
	MatchFuzzy   MatchKind = "fuzzy"
)

// FuzzyScore turns a pg_trgm similarity in [0, 1] into a score: its
// percentage, rounded down, at most MaxFuzzyScore.
func FuzzyScore(similarity float64) int {
	return min(MaxFuzzyScore, int(math.Floor(similarity*100+1e-9)))
}

// UnitOrigin says where a TM unit came from.
type UnitOrigin string

// Unit origins. Units are derived from approved translations; imports
// (TMX, RFC 0003 §2.1) arrive with the import jobs.
const (
	OriginTranslation UnitOrigin = "translation"
	OriginImport      UnitOrigin = "import"
)

// RetireReason says why a unit stopped matching. Retired units are
// kept: they are the TM's history.
type RetireReason string

// Retire reasons.
const (
	// RetireSuperseded: its translation was approved with other text (or
	// against another source).
	RetireSuperseded RetireReason = "superseded"
	// RetireUnapproved: its translation's approval was taken back.
	RetireUnapproved RetireReason = "unapproved"
	// RetireOverwritten: its translation got new text that isn't
	// approved (yet).
	RetireOverwritten RetireReason = "overwritten"
	// RetireDeleted: someone retired the unit by hand.
	RetireDeleted RetireReason = "deleted"
)

// TMUnit is one translation-memory entry: a source and target text in a
// locale pair, normalized for matching, with its provenance, scope and
// usage.
type TMUnit struct {
	ID uuid.UUID
	// ProjectID scopes the unit to one project; nil means tenant-wide.
	ProjectID *uuid.UUID
	Origin    UnitOrigin
	// Provenance of a derived unit: the approved translation and its
	// revision, the message it translates and that message's key and
	// namespace (the match context).
	TranslationID       *uuid.UUID
	TranslationRevision int
	MessageID           *uuid.UUID
	MessageKey          string
	Namespace           string

	SourceLocale bcp47.Tag
	TargetLocale bcp47.Tag
	// SourceMF2 and TargetMF2 are the canonical MF2 syntax of each side;
	// Target is the target's data model, reused structurally.
	SourceMF2  string
	TargetMF2  string
	Target     mf.Message
	SourceNorm Normalized
	TargetNorm Normalized

	HitCount  int
	LastHitAt *time.Time

	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time

	RetiredAt     *time.Time
	RetiredReason RetireReason
	RetiredBy     string
}

// Active reports whether the unit still matches.
func (u TMUnit) Active() bool { return u.RetiredAt == nil }

// Retire stops the unit from matching and records why; the unit stays
// as history.
func (u *TMUnit) Retire(reason RetireReason, by string, now time.Time) error {
	if !u.Active() {
		return ErrUnitRetired
	}
	u.RetiredAt, u.RetiredReason, u.RetiredBy, u.UpdatedAt = &now, reason, by, now
	return nil
}

// MaxUnitTextBytes bounds each side's MF2 syntax (the table's check).
const MaxUnitTextBytes = 100_000

// ImportedText is a unit read from a translation memory file (TMX).
type ImportedText struct {
	// ProjectID scopes the unit to one project; nil means tenant-wide.
	ProjectID    *uuid.UUID
	SourceLocale bcp47.Tag
	TargetLocale bcp47.Tag
	Source       mf.Message
	Target       mf.Message
}

// NewImportedUnit makes an import's unit: origin import, no translation
// behind it, both sides non-empty in two different locales.
func NewImportedUnit(in ImportedText, by string, now time.Time) (TMUnit, error) {
	if in.SourceLocale.IsZero() || in.TargetLocale.IsZero() || in.SourceLocale == in.TargetLocale {
		return TMUnit{}, fmt.Errorf("%w: a unit needs two different locales", ErrInvalidUnit)
	}
	sourceMF2, err := mf.Stringify(in.Source)
	if err != nil {
		return TMUnit{}, fmt.Errorf("%w: source: %v", ErrInvalidUnit, err)
	}
	targetMF2, err := mf.Stringify(in.Target)
	if err != nil {
		return TMUnit{}, fmt.Errorf("%w: target: %v", ErrInvalidUnit, err)
	}
	switch {
	case sourceMF2 == "" || targetMF2 == "":
		return TMUnit{}, fmt.Errorf("%w: both sides need text", ErrInvalidUnit)
	case len(sourceMF2) > MaxUnitTextBytes || len(targetMF2) > MaxUnitTextBytes:
		return TMUnit{}, fmt.Errorf("%w: a side is longer than %d bytes", ErrInvalidUnit, MaxUnitTextBytes)
	}
	return TMUnit{
		ID: uuid.Must(uuid.NewV7()), ProjectID: in.ProjectID, Origin: OriginImport,
		SourceLocale: in.SourceLocale, TargetLocale: in.TargetLocale,
		SourceMF2: sourceMF2, TargetMF2: targetMF2, Target: in.Target,
		SourceNorm: Normalize(in.Source), TargetNorm: Normalize(in.Target),
		CreatedBy: by, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// ApprovedText is what Localization says about a translation right now:
// its latest revision's source and target, and where it belongs.
type ApprovedText struct {
	TranslationID uuid.UUID
	ProjectID     uuid.UUID
	MessageID     uuid.UUID
	Revision      int
	MessageKey    string
	Namespace     string
	SourceLocale  bcp47.Tag
	TargetLocale  bcp47.Tag
	Source        mf.Message
	Target        mf.Message
	// By is who wrote or approved the text.
	By string
}

// Derivation is what a translation's current state means for its
// translation memory: create a unit, retire the active one, or just
// record that the active one is still right (Touch).
type Derivation struct {
	Create *TMUnit
	Retire RetireReason
	Touch  bool
}

// Reconcile decides, from a translation's current text and whether it
// is approved, what happens to the unit derived from it (active, nil if
// none). Approval creates a unit or keeps the one with the same text;
// other approved text supersedes it; a translation that is no longer
// approved retires it — unapproved when its text is unchanged,
// overwritten otherwise. It depends on the current state only, so
// applying it again, or for an older event, changes nothing.
func Reconcile(active *TMUnit, cur ApprovedText, isApproved bool, now time.Time) (Derivation, error) {
	source, target := Normalize(cur.Source), Normalize(cur.Target)
	sourceMF2, err := mf.Stringify(cur.Source)
	if err != nil {
		return Derivation{}, fmt.Errorf("knowledge: source of translation %s: %w", cur.TranslationID, err)
	}
	targetMF2, err := mf.Stringify(cur.Target)
	if err != nil {
		return Derivation{}, fmt.Errorf("knowledge: translation %s: %w", cur.TranslationID, err)
	}
	same := active != nil && active.SourceMF2 == sourceMF2 && active.TargetMF2 == targetMF2
	switch {
	case !isApproved && active == nil:
		return Derivation{}, nil
	case !isApproved && same:
		return Derivation{Retire: RetireUnapproved}, nil
	case !isApproved:
		return Derivation{Retire: RetireOverwritten}, nil
	case same:
		return Derivation{Touch: true}, nil
	}
	project, translation, message := cur.ProjectID, cur.TranslationID, cur.MessageID
	unit := &TMUnit{
		ID: uuid.Must(uuid.NewV7()), ProjectID: &project, Origin: OriginTranslation,
		TranslationID: &translation, TranslationRevision: cur.Revision, MessageID: &message,
		MessageKey: cur.MessageKey, Namespace: cur.Namespace,
		SourceLocale: cur.SourceLocale, TargetLocale: cur.TargetLocale,
		SourceMF2: sourceMF2, TargetMF2: targetMF2, Target: cur.Target, SourceNorm: source, TargetNorm: target,
		CreatedBy: cur.By, CreatedAt: now, UpdatedAt: now,
	}
	if active == nil {
		return Derivation{Create: unit}, nil
	}
	return Derivation{Create: unit, Retire: RetireSuperseded}, nil
}
