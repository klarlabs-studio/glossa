package v0

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	mf "go.klarlabs.de/glossa/messageformat"
)

// `glossa import --from v0 --verify` (RFC 0006 §7.3): every key in every
// locale is rendered by v0.3's own formatter (@felixgeelhaar/glossa-format)
// over v0.3's text and by @klarlabs-studio/glossa over the release the edge
// serves, with the same arguments, and the two outputs are compared.
// This file is the Go half: which renderings to ask for, and what a
// difference means. verify.mjs is the Node half that renders.

// The categories of one rendering.
const (
	// VerdictMatch: both sides said the same thing.
	VerdictMatch = "match"
	// VerdictKnownDefect: they differ only because v0.3's formatter
	// mis-reads an apostrophe (DefectBareApostrophe). Reported with every
	// key and counted; never silently passed.
	VerdictKnownDefect = "known_defect"
	// VerdictMismatch: any other difference, or either side failing.
	VerdictMismatch = "mismatch"
)

// DefectBareApostrophe names v0.3's known formatter defect: a bare
// apostrophe starts a quoted run that lasts to the next apostrophe or
// the end of the message, so "Geht's gut, {name}?" renders as
// "Gehts gut, {name}?" — the apostrophe dropped and the argument
// swallowed into the quoted run. ICU MessageFormat (and the platform's
// MF1 → MF2 converter) quotes only at an apostrophe before a syntax
// character, so the runtime's "Geht's gut, Ada?" is the right one.
const DefectBareApostrophe = "v0_bare_apostrophe"

// Rendering is one key in one locale with one argument set, both ways.
// V0Requoted is v0.3's formatter over RequoteForV0(text): the same
// formatter, given the apostrophes the way ICU reads them.
type Rendering struct {
	V0, Runtime, V0Requoted *string
	V0Error, RuntimeError   string
	RequotedError           string
	// Requoted reports whether RequoteForV0 changed the text at all.
	Requoted bool
}

// Classify says what a rendering's difference is. The rule for the
// known defect is strict: the runtime rendered; v0.3's output differs
// from it (or v0.3 failed); and v0.3's own formatter, handed the same
// text with only its apostrophes rewritten to mean what ICU means
// (RequoteForV0), renders exactly the runtime's output. Everything else
// that differs — a changed word beside an apostrophe, an argument
// formatted differently, a fallback, an error on either side — is a
// mismatch.
func Classify(r Rendering) (verdict, defect string) {
	if r.RuntimeError != "" || r.Runtime == nil {
		return VerdictMismatch, ""
	}
	if r.V0Error == "" && r.V0 != nil && *r.V0 == *r.Runtime {
		return VerdictMatch, ""
	}
	if r.Requoted && r.RequotedError == "" && r.V0Requoted != nil && *r.V0Requoted == *r.Runtime {
		return VerdictKnownDefect, DefectBareApostrophe
	}
	return VerdictMismatch, ""
}

// The platform's MF1 lexer (github.com/kaptinlin/messageformat-go/mf1,
// a port of @messageformat/parser) reads an apostrophe in message text
// three ways, tried in this order:
//
//   - a doubled apostrophe is one literal apostrophe;
//   - an apostrophe before "{", "}" or "#" opens a quoted run to the
//     next lone apostrophe, whose content is literal (a doubled
//     apostrophe inside it is one apostrophe) — when that closing
//     apostrophe is followed by another, it is no quoted run;
//   - any other apostrophe is literal.
//
// v0.3's parser has one rule beyond the doubled apostrophe: any other
// apostrophe opens a quoted run to the next apostrophe or the end, its
// content literal and the apostrophes dropped.
var (
	mf1DoubleApos = regexp.MustCompile(`^''`)
	mf1Quoted     = regexp.MustCompile(`^'[{}#](?:[^']|'')*'`)
)

// RequoteForV0 rewrites text so that v0.3's parser reads every
// apostrophe as the platform's MF1 lexer does, and changes nothing
// else: a literal apostrophe is doubled, and a quoted run becomes its
// literal characters, each syntax character quoted alone ("'{'") and
// each apostrophe doubled. Text without a bare apostrophe comes back
// unchanged.
func RequoteForV0(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\'' {
			b.WriteByte(text[i])
			i++
			continue
		}
		rest := text[i:]
		if mf1DoubleApos.MatchString(rest) {
			b.WriteString("''")
			i += 2
			continue
		}
		if m := mf1Quoted.FindString(rest); m != "" && !strings.HasPrefix(rest[len(m):], "'") {
			for _, r := range strings.ReplaceAll(m[1:len(m)-1], "''", "'") {
				switch r {
				case '\'':
					b.WriteString("''")
				case '{', '}', '#':
					b.WriteString("'" + string(r) + "'")
				default:
					b.WriteRune(r)
				}
			}
			i += len(m)
			continue
		}
		b.WriteString("''")
		i++
	}
	return b.String()
}

// maxArgSets bounds the argument sets one key is rendered with.
const maxArgSets = 64

// selectorNumbers are the counts every plural is rendered with: zero,
// one, two, a few and a many-form number, none with a group separator
// (v0.3 prints "#" without the locale's number formatting, a documented
// v0.3 simplification that would otherwise be a difference at 1,000).
var selectorNumbers = []any{0, 1, 2, 5, 21}

// fallbackValue is what a string selector is given to reach its
// catch-all.
const fallbackValue = "zz_other"

// GenerateArgs returns the argument sets a key is rendered with,
// generated from the arguments its text declares in every locale (the
// platform's own argument metadata, messageformat.Arguments, over the
// MF1 source): every plural is tried with selectorNumbers and its exact
// keys, every select with each of its keys and one that reaches its
// catch-all, and every other argument with one value. The sets are the
// product of the selectors' values, or — past maxArgSets — each
// selector varied alone against the first value of the others.
func GenerateArgs(texts map[string]string) []map[string]any {
	values := map[string][]any{}
	locales := make([]string, 0, len(texts))
	for l := range texts {
		locales = append(locales, l)
	}
	sort.Strings(locales)
	for _, l := range locales {
		msg, err := mf.ParseMF1(texts[l], l)
		if err != nil {
			continue
		}
		for _, a := range mf.Arguments(msg) {
			values[a.Name] = mergeValues(values[a.Name], argValues(a))
		}
	}
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	product := 1
	for _, n := range names {
		product *= len(values[n])
		if product > maxArgSets {
			break
		}
	}
	if product <= maxArgSets {
		sets := []map[string]any{{}}
		for _, n := range names {
			var next []map[string]any
			for _, s := range sets {
				for _, v := range values[n] {
					c := make(map[string]any, len(s)+1)
					for k, x := range s {
						c[k] = x
					}
					c[n] = v
					next = append(next, c)
				}
			}
			sets = next
		}
		return sets
	}
	base := map[string]any{}
	for _, n := range names {
		base[n] = values[n][0]
	}
	sets := []map[string]any{base}
	for _, n := range names {
		for _, v := range values[n][1:] {
			c := make(map[string]any, len(base))
			for k, x := range base {
				c[k] = x
			}
			c[n] = v
			sets = append(sets, c)
		}
	}
	return sets
}

func argValues(a mf.Argument) []any {
	if a.Selector != nil {
		switch a.Selector.Kind {
		case mf.SelectPlural, mf.SelectOrdinal, mf.SelectExact:
			out := slices.Clone(selectorNumbers)
			for _, k := range a.Selector.Keys {
				if n, err := strconv.Atoi(k); err == nil {
					out = mergeValues(out, []any{n})
				}
			}
			return out
		default:
			out := make([]any, 0, len(a.Selector.Keys)+1)
			for _, k := range a.Selector.Keys {
				out = append(out, k)
			}
			return append(out, fallbackValue)
		}
	}
	switch a.Type {
	case mf.ArgNumber, mf.ArgInteger, mf.ArgPercent:
		return []any{21}
	}
	return []any{"Ada"}
}

func mergeValues(into, more []any) []any {
	for _, v := range more {
		if !slices.Contains(into, v) {
			into = append(into, v)
		}
	}
	return into
}
