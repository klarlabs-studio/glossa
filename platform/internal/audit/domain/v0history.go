package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// v0.3's history, imported (RFC 0006 §7.2, amended in wave 4). Each row
// of v0.3's audit_log becomes one entry with SourceImport: who changed
// which translation when, and digests of the text before and after —
// never the text itself, which §6.1 keeps out of every entry. Whoever
// holds the archived dump (§7.2's record of last resort) can prove which
// text a digest stands for; an audit export cannot leak it.

// ActionV0TranslationChanged is the action of an imported v0.3 row.
const ActionV0TranslationChanged = "v0.translation.changed"

// AggregateV0AuditLog is the aggregate of an imported entry: the v0.3
// audit_log row it records, by its id. The translation it changed is
// named by the entry's project and locale and its summary's key.
const AggregateV0AuditLog = "v0.audit_log"

// v0EventNamespace derives an imported entry's EventID from its v0.3 row
// id (UUID v5), so the same row always becomes the same event and a
// second import of a tenant — or one import per project, each of which
// carries the rows whose translation is gone — records it once. It never
// changes: changing it would record every row again.
var v0EventNamespace = uuid.MustParse("6f0b3c1e-5a52-4b8e-9d0c-0e3d6b7a0c35")

// V0EventID is the EventID of the entry that records v0.3 audit_log row
// v0ID. v0.3's ids are unique within one v0.3 deployment, and there is
// one (RFC 0006 §7.1); importing a second deployment's history into the
// same tenant would need its own namespace.
func V0EventID(v0ID string) uuid.UUID {
	return uuid.NewSHA1(v0EventNamespace, []byte("glossa.v0.audit_log:"+v0ID))
}

var (
	v0ActorPattern = regexp.MustCompile(
		`^v0:(unknown|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|(ai|system)(:[^\x00-\x1f\x7f]{1,100})?)$`)
	v0IDPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reasonPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	errNoV0Location = errors.New("a row names a key and a locale, or says why it cannot (unresolved), not both or neither")
)

// maxV0ActorRunes bounds a v0.3 actor: "v0:system:" and v0.3's
// 100-character actor_label.
const maxV0ActorRunes = 110

// ValidV0Actor reports whether a is a v0.3 actor as the importer spells
// them: v0:<user uuid>, v0:ai[:<label>], v0:system[:<label>] or
// v0:unknown. The label is v0.3's actor_label (at most 100 characters,
// printable).
func ValidV0Actor(a string) bool {
	return utf8.ValidString(a) && utf8.RuneCountInString(a) <= maxV0ActorRunes && v0ActorPattern.MatchString(a)
}

// V0Change is one v0.3 audit_log row as the import records it: content
// free by construction — it has no field a text could go in.
type V0Change struct {
	// V0ID is v0.3's audit_log id.
	V0ID       string
	Actor      string
	OccurredAt time.Time
	// Project is the platform project the history was imported into.
	Project uuid.UUID
	// Key and Locale name the translation; Unresolved says why a row
	// names none (its v0.3 translation is gone). Unresolved is recorded
	// verbatim when it is a code ("translation_deleted"), otherwise as
	// its shape.
	Key, Locale, Unresolved string
	// BeforeSHA256 and AfterSHA256 are hex SHA-256 digests of v0.3's
	// UTF-8 text before and after the change, "" for none.
	BeforeSHA256, AfterSHA256 string
	// Restore and RestoreSHA256 name the dump the row was read from, as
	// the restore marker records it.
	Restore, RestoreSHA256 string
}

// Draft returns the entry the row becomes, or an error wrapping
// ErrInvalidEntry naming what is wrong with it.
func (c V0Change) Draft() (Draft, error) {
	var errs []error
	if !v0IDPattern.MatchString(c.V0ID) {
		errs = append(errs, fmt.Errorf("v0 id %q", c.V0ID))
	}
	if c.Project == uuid.Nil {
		errs = append(errs, errors.New("no project"))
	}
	resolved := c.Key != "" && c.Locale != "" && c.Unresolved == ""
	unresolved := c.Key == "" && c.Locale == "" && c.Unresolved != ""
	if !resolved && !unresolved {
		errs = append(errs, errNoV0Location)
	}
	if c.Key != "" && (!utf8.ValidString(c.Key) || utf8.RuneCountInString(c.Key) > maxNameLen || hasControl(c.Key)) {
		errs = append(errs, errors.New("key is not a key"))
	}
	before, err := digest("before", c.BeforeSHA256)
	errs = append(errs, err)
	after, err := digest("after", c.AfterSHA256)
	errs = append(errs, err)
	if !sha256Pattern.MatchString(c.RestoreSHA256) {
		errs = append(errs, errors.New("restore_sha256 is not a hex SHA-256 digest"))
	}
	if c.Restore == "" || !verbatimRestore(c.Restore) {
		errs = append(errs, errors.New("restore names no dump"))
	}
	if err := errors.Join(errs...); err != nil {
		return Draft{}, fmt.Errorf("%w: v0.3 row %q: %w", ErrInvalidEntry, c.V0ID, err)
	}
	summary := map[string]any{
		"v0_id": c.V0ID, "key": nil, "unresolved": nil,
		"before_sha256": before, "after_sha256": after,
		"restore": c.Restore, "restore_sha256": c.RestoreSHA256,
	}
	if c.Key != "" {
		summary["key"] = identifier(c.Key)
	}
	if c.Unresolved != "" {
		summary["unresolved"] = reason(c.Unresolved)
	}
	d := Draft{
		EventID: V0EventID(c.V0ID), Source: SourceImport, Action: ActionV0TranslationChanged,
		Actor: c.Actor, OccurredAt: Instant(c.OccurredAt),
		AggregateType: AggregateV0AuditLog, AggregateID: c.V0ID,
		Project: uuid.NullUUID{UUID: c.Project, Valid: true}, Locale: c.Locale,
		Summary: mustJSON(summary),
	}
	return d, d.Validate()
}

// digest checks a hex digest; "" is none (null).
func digest(name, hex string) (any, error) {
	if hex == "" {
		return nil, nil
	}
	if !sha256Pattern.MatchString(hex) {
		return nil, fmt.Errorf("%s_sha256 is not a lowercase hex SHA-256 digest", name)
	}
	return hex, nil
}

// identifier keeps a key verbatim when a summary may hold it, and
// records its shape otherwise.
func identifier(s string) string {
	if utf8.RuneCountInString(s) <= maxSummaryString && !hasControl(s) {
		return s
	}
	return fmt.Sprintf("string(len=%d)", utf8.RuneCountInString(s))
}

// reason keeps a code verbatim; a sentence is recorded as its shape, so
// nothing a caller writes as prose reaches the trail.
func reason(s string) string {
	if reasonPattern.MatchString(s) {
		return s
	}
	return fmt.Sprintf("string(len=%d)", utf8.RuneCountInString(s))
}

func verbatimRestore(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= maxSummaryString && !hasControl(s) &&
		!strings.ContainsAny(s, "/\\")
}
