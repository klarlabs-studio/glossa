package glossa

import (
	"strings"

	"golang.org/x/text/language"
)

// Locale identity and negotiation (runtimes/SPEC.md §4.1).

// Direction is the base text direction of a locale, matching the HTML dir
// attribute.
type Direction string

// Text directions.
const (
	LTR Direction = "ltr"
	RTL Direction = "rtl"
)

// canonicalize turns a requested tag into the canonical BCP 47 form the
// platform stores (RFC 5646 §4.5: deprecated subtags replaced, implied
// scripts suppressed, conventional casing; `_` accepted as a separator).
// Extensions and private use are dropped: they carry formatting
// preferences, not a distinct body of translations. It reports false for
// tags that are malformed or have no language.
func canonicalize(tag string) (string, bool) {
	t, err := language.BCP47.Parse(strings.TrimSpace(tag))
	if err != nil {
		return "", false
	}
	if base, _, _ := t.Raw(); base.String() == "und" {
		return "", false
	}
	return stripExtensions(t.String()), true
}

// stripExtensions cuts a canonical tag at its first singleton subtag,
// where extensions and private use begin.
func stripExtensions(tag string) string {
	subtags := strings.Split(tag, "-")
	for i, s := range subtags {
		if i > 0 && len(s) == 1 {
			return strings.Join(subtags[:i], "-")
		}
	}
	return tag
}

// canonicalizeAll canonicalizes tags in order, dropping malformed ones and
// duplicates.
func canonicalizeAll(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		c, ok := canonicalize(tag)
		if !ok || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// truncations returns the RFC 4647 §3.4 lookup fallbacks of tag, most
// specific first, excluding tag itself: `zh-Hant-TW` → `zh-Hant`, `zh`. A
// trailing single-character subtag is dropped together with the subtag
// before it.
func truncations(tag string) []string {
	var out []string
	subtags := strings.Split(tag, "-")
	for n := len(subtags) - 1; n > 0; n-- {
		if len(subtags[n-1]) == 1 {
			continue
		}
		out = append(out, strings.Join(subtags[:n], "-"))
	}
	return out
}

// lookup is RFC 4647 §3.4 Lookup of canonical requested tags over the
// available ones: each requested tag in order, then its truncations. It
// reports false when nothing matches.
func lookup(requested, available []string) (string, bool) {
	set := make(map[string]bool, len(available))
	for _, a := range available {
		set[a] = true
	}
	for _, r := range requested {
		for _, candidate := range append([]string{r}, truncations(r)...) {
			if set[candidate] {
				return candidate, true
			}
		}
	}
	return "", false
}

// rtlScripts lists the ISO 15924 scripts whose characters are
// right-to-left (Unicode bidi class R or AL). Keep in sync with the
// platform's locale package and the retired v0.3 SDK's locale.ts.
var rtlScripts = map[string]bool{
	"Adlm": true, "Arab": true, "Aran": true, "Armi": true, "Avst": true,
	"Chrs": true, "Cprt": true, "Elym": true, "Hatr": true, "Hebr": true,
	"Hung": true, "Khar": true, "Lydi": true, "Mand": true, "Mani": true,
	"Mend": true, "Merc": true, "Mero": true, "Narb": true, "Nbat": true,
	"Nkoo": true, "Orkh": true, "Ougr": true, "Palm": true, "Phli": true,
	"Phlp": true, "Phnx": true, "Prti": true, "Rohg": true, "Samr": true,
	"Sarb": true, "Sogd": true, "Sogo": true, "Syrc": true, "Thaa": true,
	"Yezi": true,
}

// scriptDirection derives a direction from the explicit or CLDR likely
// script of tag. It is the fallback when no manifest names the direction.
func scriptDirection(tag string) Direction {
	t, err := language.Parse(tag)
	if err != nil {
		return LTR
	}
	script, confidence := t.Script()
	if confidence != language.No && rtlScripts[script.String()] {
		return RTL
	}
	return LTR
}
