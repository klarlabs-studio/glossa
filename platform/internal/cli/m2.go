package cli

import (
	"errors"
	"flag"
	"fmt"
	"regexp"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Helpers shared by the Knowledge and Intelligence commands (tm, terms,
// style, translate, review, ai; RFC 0003 §6).

// listFlag is a repeatable string flag.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

func (l *listFlag) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// idPattern matches the server's IDs (UUIDs).
var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// normalizeLocale parses a locale flag; flag names it in errors.
func normalizeLocale(inv *invocation, flag, v string) (string, error) {
	tag, err := bcp47.Parse(strings.TrimSpace(v))
	if err != nil {
		return "", usageError(inv.name, "%s %q is not a locale (BCP 47, e.g. de or pt-BR)", flag, v)
	}
	return tag.String(), nil
}

// normalizeLocales parses locale flags given repeatedly or
// comma-separated.
func normalizeLocales(inv *invocation, flag string, vs []string) ([]string, error) {
	var out []string
	for _, v := range vs {
		for _, part := range strings.Split(v, ",") {
			if strings.TrimSpace(part) == "" {
				continue
			}
			l, err := normalizeLocale(inv, flag, part)
			if err != nil {
				return nil, err
			}
			if !contains(out, l) {
				out = append(out, l)
			}
		}
	}
	return out, nil
}

// localeText parses "<locale>=<text>" (e.g. en=shopping cart).
func localeText(inv *invocation, flag, v string) (string, string, error) {
	loc, text, ok := strings.Cut(v, "=")
	text = strings.TrimSpace(text)
	if !ok || text == "" {
		return "", "", usageError(inv.name, "%s takes <locale>=<text>, e.g. en=shopping cart; got %q", flag, v)
	}
	l, err := normalizeLocale(inv, flag, loc)
	return l, text, err
}

// joinArgs is the positional arguments as one text, so quoting is only
// needed for shell syntax.
func joinArgs(pos []string) string { return strings.TrimSpace(strings.Join(pos, " ")) }

// m2Fixes explains the Knowledge and Intelligence APIs' problem codes.
var m2Fixes = map[string]string{
	"invalid_locale":          "use a BCP 47 locale the project has (`glossa locales` lists them)",
	"locale_not_found":        "add the locale to the project first (Studio), or check --locale (`glossa locales` lists them)",
	"invalid_syntax":          "pass --syntax mf1 (ICU) or mf2",
	"invalid_message":         "fix the message syntax; `glossa check --offline` shows the kernel's findings for local catalogs",
	"message_too_long":        "shorten the text",
	"duplicate_term":          "a concept lists each locale's term once; `glossa terms show` shows its terms",
	"invalid_term_status":     "use preferred, admitted, deprecated or forbidden",
	"invalid_part_of_speech":  "use noun, proper_noun, verb, adjective, adverb, phrase or other",
	"unknown_project":         "check project in glossa.yaml",
	"invalid_style_guide":     "fix the style file; the README documents its fields",
	"invalid_style_rule":      "every rule needs an id (lowercase, e.g. no-exclamation); good and bad are lists of examples",
	"namespace_needs_project": "a namespace guide belongs to a project: drop --tenant-wide",
	"style_guide_exists":      "someone added the guide a moment ago; run `glossa style edit` again to update it",
	"precondition_failed":     "it changed since it was read; run the command again",
	"too_many_locales":        "translate at most 20 locales at a time",
	"too_many_keys":           "narrow the fill with --namespace or --key-prefix",
	"suggestion_decided":      "someone already decided it; `glossa review list` shows what is still pending",
	"suggestion_outdated":     "the source changed since the suggestion was made; run `glossa translate` again for a new one",
	"translation_conflict":    "the translation changed meanwhile; review it in Studio or run `glossa translate` again",
	"translation_rejected":    "the text doesn't fit the source's structure; fix --text (`glossa check` explains the rules)",
	"invalid_reason":          "shorten --reason",
}

// m2Error explains a failed Knowledge or Intelligence request: invalid
// input is a usage error (exit 2), the server refusing the operation a
// network error (exit 3), both with the server's detail as the reason.
func (inv *invocation) m2Error(err error, what string) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	e := asError(inv.apiError(err, what))
	if fix, ok := m2Fixes[ae.Code]; ok {
		e.Fix = fix
	}
	switch {
	case ae.Status == 400 || ae.Code == "locale_not_found":
		e.Exit = ExitUsage
		if ae.Detail != "" {
			e.Why = ae.Detail
		}
	case ae.Status == 409 || ae.Status == 412 || ae.Status == 422:
		if ae.Detail != "" {
			e.Why = ae.Detail
		}
	case ae.Status == 404:
		e.Fix = "check the ID: `glossa tm units`, `glossa terms list` and `glossa review list` show them"
	case ae.Status == 403:
		e.Fix = "use a token with the needed scope, created in Studio: read for searching and listing, write for changing the termbase, style guides and translations and for `glossa translate`"
	}
	return e
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefList(s *[]string) []string {
	if s == nil {
		return []string{}
	}
	return *s
}

func nonNilList[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// shortID shortens an ID for human output (--json has it in full).
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

// usd formats micro-USD.
func usd(micro int64) string {
	return fmt.Sprintf("$%.2f", float64(micro)/1e6)
}

// isSet reports whether the command line set the flag.
func isSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}
