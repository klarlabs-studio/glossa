package domain

import (
	"fmt"
	"slices"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Options shape how a job reads or writes its file. Which fields apply
// depends on the direction and format; Normalize refuses the others, so
// a job never carries an option that silently did nothing.
type Options struct {
	// Locale (import): the locale of a JSON file (default: the project's
	// source locale, so the file holds source messages), the target
	// locale of a PO file (default: its Language header) or of an XLIFF
	// file (default: its trgLang; the option names the locale of a file
	// without one, or imports a file as another locale than it names).
	Locale string `json:"locale,omitempty"`
	// Namespace (import, JSON and PO): the namespace of every message
	// (default "default").
	Namespace string `json:"namespace,omitempty"`
	// Syntax: JSON messages in mf1 (default) or mf2; for XLIFF imports,
	// how to read units from other tools that carry neither Glossa's
	// MF2 marker nor inline codes: literally (default) or as mf1.
	Syntax string `json:"syntax,omitempty"`
	// State (import): the review state of translations the file doesn't
	// state — JSON (default needs_review) and PO entries without the
	// fuzzy flag (default approved).
	State string `json:"state,omitempty"`
	// PluralVariable (PO import): the count variable of plurals
	// (default "count").
	PluralVariable string `json:"plural_variable,omitempty"`

	// Locales (export): catalog exports write one file per locale
	// (several make a zip); an XLIFF export without locales carries the
	// source only; JSON exports default to the source locale. TMX
	// exports keep units whose target is one of them (default all).
	Locales []string `json:"locales,omitempty"`
	// SourceLocale (TMX export): keep units with this source locale.
	SourceLocale string `json:"source_locale,omitempty"`
	// Namespaces (catalog export): only messages in these (default all).
	Namespaces []string `json:"namespaces,omitempty"`
	// States (catalog export): translations in these review states
	// (default approved).
	States []string `json:"states,omitempty"`
	// Layout (JSON export): flat (default) or nested.
	Layout string `json:"layout,omitempty"`
}

// Export and import limits.
const (
	MaxExportLocales = 50
	MaxNamespaces    = 100
)

func optionErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOptions, fmt.Sprintf(format, args...))
}

// NormalizeImport validates o for an import of format and canonicalizes
// its values.
func (o Options) NormalizeImport(f Format) (Options, error) {
	if len(o.Locales) > 0 || o.SourceLocale != "" || len(o.Namespaces) > 0 || len(o.States) > 0 || o.Layout != "" {
		return Options{}, optionErr("locales, source_locale, namespaces, states and layout apply to exports")
	}
	allowed := map[Format][]string{
		FormatJSON:  {"locale", "namespace", "syntax", "state"},
		FormatPO:    {"locale", "namespace", "state", "plural_variable"},
		FormatXLIFF: {"locale", "syntax"},
	}[f]
	for name, set := range map[string]bool{
		"locale": o.Locale != "", "namespace": o.Namespace != "", "syntax": o.Syntax != "",
		"state": o.State != "", "plural_variable": o.PluralVariable != "",
	} {
		if set && !slices.Contains(allowed, name) {
			return Options{}, optionErr("%s doesn't apply to %s imports", name, f)
		}
	}
	var err error
	if o.Locale, err = canonicalLocale("locale", o.Locale); err != nil {
		return Options{}, err
	}
	if o.Syntax != "" && o.Syntax != "mf1" && (o.Syntax != "mf2" || f == FormatXLIFF) {
		return Options{}, optionErr("syntax must be mf1%s", map[bool]string{true: "", false: " or mf2"}[f == FormatXLIFF])
	}
	if o.State != "" && !formats.State(o.State).Valid() {
		return Options{}, optionErr("state must be draft, needs_review, approved or rejected")
	}
	if o.PluralVariable != "" && !identifier(o.PluralVariable) {
		return Options{}, optionErr("plural_variable must be a variable name")
	}
	return o, nil
}

// CheckImportLocale checks an import's canonical locale option against
// the project it imports into: a translation locale must be one of its
// targets (ErrLocaleNotInProject), and only a JSON file may be in the
// source locale — it is a source catalog then; XLIFF and PO carry the
// source beside their translations (ErrInvalidOptions).
func CheckImportLocale(f Format, locale string, source bcp47.Tag, targets []bcp47.Tag) error {
	if locale == "" || f.Kind() != KindCatalog {
		return nil
	}
	if locale == source.String() {
		if f == FormatJSON {
			return nil
		}
		return optionErr("locale %s is the project's source locale; a %s file's translations are in a target locale", locale, f)
	}
	if !slices.ContainsFunc(targets, func(t bcp47.Tag) bool { return t.String() == locale }) {
		return fmt.Errorf("%w: %s", ErrLocaleNotInProject, locale)
	}
	return nil
}

// UnknownTargetLocales lists, once each and in order, the locales of a
// file's translations that aren't among the project's targets: an
// import of them fails target_locale_mismatch instead of reporting
// every translation invalid.
func UnknownTargetLocales(file, targets []bcp47.Tag) []bcp47.Tag {
	var out []bcp47.Tag
	for _, l := range file {
		if !slices.Contains(targets, l) && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}

// NormalizeExport validates o for an export of format and canonicalizes
// its values.
func (o Options) NormalizeExport(f Format) (Options, error) {
	if !f.Exportable() {
		return Options{}, ErrNotExportable
	}
	if o.Locale != "" || o.Namespace != "" || o.State != "" || o.PluralVariable != "" {
		return Options{}, optionErr("locale, namespace, state and plural_variable apply to imports; exports take locales")
	}
	allowed := map[Format][]string{
		FormatJSON:  {"locales", "namespaces", "states", "layout", "syntax"},
		FormatXLIFF: {"locales", "namespaces", "states"},
		FormatTMX:   {"locales", "source_locale"},
		FormatTBX:   {},
	}[f]
	for name, set := range map[string]bool{
		"locales": len(o.Locales) > 0, "namespaces": len(o.Namespaces) > 0, "states": len(o.States) > 0,
		"layout": o.Layout != "", "syntax": o.Syntax != "", "source_locale": o.SourceLocale != "",
	} {
		if set && !slices.Contains(allowed, name) {
			return Options{}, optionErr("%s doesn't apply to %s exports", name, f)
		}
	}
	if len(o.Locales) > MaxExportLocales {
		return Options{}, optionErr("at most %d locales", MaxExportLocales)
	}
	locales := make([]string, 0, len(o.Locales))
	for _, l := range o.Locales {
		c, err := canonicalLocale("locales", l)
		if err != nil {
			return Options{}, err
		}
		if !slices.Contains(locales, c) {
			locales = append(locales, c)
		}
	}
	o.Locales = nilIfEmpty(locales)
	var err error
	if o.SourceLocale, err = canonicalLocale("source_locale", o.SourceLocale); err != nil {
		return Options{}, err
	}
	if len(o.Namespaces) > MaxNamespaces {
		return Options{}, optionErr("at most %d namespaces", MaxNamespaces)
	}
	o.Namespaces = nilIfEmpty(sortedUnique(o.Namespaces))
	for _, s := range o.States {
		if !formats.State(s).Valid() {
			return Options{}, optionErr("states must be draft, needs_review, approved or rejected")
		}
	}
	o.States = nilIfEmpty(sortedUnique(o.States))
	if o.Layout != "" && o.Layout != "flat" && o.Layout != "nested" {
		return Options{}, optionErr("layout must be flat or nested")
	}
	if o.Syntax != "" && o.Syntax != "mf1" && o.Syntax != "mf2" {
		return Options{}, optionErr("syntax must be mf1 or mf2")
	}
	return o, nil
}

// ExportStates returns the review states a catalog export includes.
func (o Options) ExportStates() []string {
	if len(o.States) == 0 {
		return []string{string(formats.StateApproved)}
	}
	return o.States
}

func canonicalLocale(name, s string) (string, error) {
	if s == "" {
		return "", nil
	}
	t, err := bcp47.Parse(s)
	if err != nil {
		return "", optionErr("%s: %v", name, err)
	}
	return t.String(), nil
}

func identifier(s string) bool {
	for i, r := range s {
		ok := r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '.')
		if !ok {
			return false
		}
	}
	return s != "" && len(s) <= 64
}

func sortedUnique(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return slices.Compact(out)
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// Access is what a job's requester may do, snapshotted when they asked:
// the worker acts within it later, whatever principal it runs as.
type Access struct {
	// Actor is the requester ("person:…", "token:…").
	Actor string `json:"actor"`
	// Import is where they hold integration.import and
	// translations.write: the locales they may import translations for.
	Import authz.Scope `json:"import"`
	// Review is where they hold translations.review: where a file's
	// approvals (and rejections) are kept rather than capped.
	Review authz.Scope `json:"review"`
	// Manage: integration.manage with catalog.write (catalogs) or
	// knowledge.write (TM, termbases) — creating and revising messages,
	// TM and termbase imports, overwrite mode.
	Manage bool `json:"manage"`
}

// Intersect is where both scopes grant their permissions.
func Intersect(a, b authz.Scope) authz.Scope {
	switch {
	case !a.Granted || !b.Granted:
		return authz.Scope{}
	case a.All():
		return b
	case b.All():
		return a
	}
	var out []string
	for _, l := range a.Locales {
		if tag, err := authz.ParseLocale(l); err == nil && b.Covers(tag) {
			out = append(out, l)
		}
	}
	for _, l := range b.Locales {
		if tag, err := authz.ParseLocale(l); err == nil && a.Covers(tag) && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return authz.Scope{}
	}
	slices.Sort(out)
	return authz.Scope{Granted: true, Locales: out}
}

// Covers reports whether s grants its permission for the locale tag.
func Covers(s authz.Scope, locale bcp47.Tag) bool {
	tag, err := authz.ParseLocale(locale.String())
	return err == nil && s.Covers(tag)
}
