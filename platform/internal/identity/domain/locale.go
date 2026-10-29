package domain

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/text/language"
)

// Locale is a canonical BCP 47 language tag naming something people
// translate into: "de", "pt-BR", "zh-Hant-TW".
type Locale struct{ tag language.Tag }

// maxLocaleLen bounds attacker-controlled input before parsing.
const maxLocaleLen = 64

// ParseLocale parses and canonicalizes s under BCP 47 rules (case,
// deprecated and grandfathered codes, suppressed scripts): "EN_us" →
// "en-US", "iw" → "he". It refuses tags that can't name a translation
// target: "und", private-use-only tags, and tags with extensions.
func ParseLocale(s string) (Locale, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxLocaleLen {
		return Locale{}, fmt.Errorf("%w: %q", ErrInvalidLocale, s)
	}
	tag, err := language.BCP47.Parse(s)
	if err != nil {
		return Locale{}, fmt.Errorf("%w: %q: %v", ErrInvalidLocale, s, err)
	}
	if _, conf := tag.Base(); conf != language.Exact {
		return Locale{}, fmt.Errorf("%w: %q names no language", ErrInvalidLocale, s)
	}
	if len(tag.Extensions()) > 0 {
		return Locale{}, fmt.Errorf("%w: %q: extensions are not allowed", ErrInvalidLocale, s)
	}
	return Locale{tag: tag}, nil
}

// String returns the canonical tag.
func (l Locale) String() string { return l.tag.String() }

// Tag returns the underlying language tag.
func (l Locale) Tag() language.Tag { return l.tag }

// LocaleScope limits locale-scoped permissions (translate, review) to
// some locales. The zero value is unrestricted: every locale.
type LocaleScope struct{ locales []Locale }

// ParseLocaleScope canonicalizes, sorts and de-duplicates tags. An empty
// list is the unrestricted scope.
func ParseLocaleScope(tags []string) (LocaleScope, error) {
	seen := map[string]bool{}
	var out []Locale
	for _, s := range tags {
		l, err := ParseLocale(s)
		if err != nil {
			return LocaleScope{}, err
		}
		if !seen[l.String()] {
			seen[l.String()] = true
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b Locale) int { return strings.Compare(a.String(), b.String()) })
	return LocaleScope{locales: out}, nil
}

// All reports whether the scope is unrestricted.
func (s LocaleScope) All() bool { return len(s.locales) == 0 }

// Strings returns the canonical tags, sorted; empty when unrestricted.
func (s LocaleScope) Strings() []string {
	out := make([]string, len(s.locales))
	for i, l := range s.locales {
		out[i] = l.String()
	}
	return out
}

// Covers reports whether l falls in the scope: the scope names l itself
// or one of its CLDR ancestors. So "de" covers "de-AT", "es-419" covers
// "es-AR", and "pt-PT" covers "pt-AO" — the locales that inherit their
// translations — but "es-419" doesn't cover "es", and "sr" doesn't cover
// "sr-Latn" (a different script is a different translation).
func (s LocaleScope) Covers(l Locale) bool {
	if s.All() {
		return true
	}
	for t := l.tag; ; t = t.Parent() {
		for _, c := range s.locales {
			if c.tag == t {
				return true
			}
		}
		if t.IsRoot() {
			return false
		}
	}
}
