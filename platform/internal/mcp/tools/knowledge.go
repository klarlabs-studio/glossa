package tools

import (
	"context"
	"encoding/json"
	"fmt"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// Knowledge's own caps. A TM lookup is a similarity scan and a
// recognition pass walks the whole termbase, so both are bounded well
// below MaxLimit.
const (
	maxTMLimit   = 50
	maxTermLimit = 100
)

// TMSearchName is the translation-memory tool's wire name.
const TMSearchName = "tm_search"

type tmSearchArgs struct {
	Project      string `json:"project"`
	Text         string `json:"text"`
	Syntax       string `json:"syntax"`
	SourceLocale string `json:"source_locale"`
	TargetLocale string `json:"target_locale"`
	MessageKey   string `json:"message_key"`
	Namespace    string `json:"namespace"`
	Limit        *int   `json:"limit"`
	MinScore     *int   `json:"min_score"`
}

// TMSearchResult is a translation-memory lookup's matches.
type TMSearchResult struct {
	Matches []TMMatch `json:"matches"`
}

// tmSearch looks a source text up in translation memory.
//
// `text` is the one argument of the read tools that carries message
// text, so it is deliberately not a selector: the audit ledger records
// its length and never its content (RFC 0005 §11).
func tmSearch(k Knowledge) app.Tool {
	return app.Tool{
		Name:  TMSearchName,
		Title: "Search translation memory",
		Description: "Find exact (100, or 101 when the same key in the same namespace was approved) " +
			"and fuzzy (50–99) translation-memory matches for a source text in a locale pair, best " +
			"first. Bounded rather than paginated: a similarity scan returns its best matches, not " +
			"a catalog. Pass message_key and namespace to get in-context scoring.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermKnowledgeRead,
		Selectors:   selectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(tmSearchSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a tmSearchArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if err := boundedText("text", a.Text); err != nil {
				return app.Result{}, err
			}
			if err := required("source_locale", a.SourceLocale); err != nil {
				return app.Result{}, err
			}
			if err := required("target_locale", a.TargetLocale); err != nil {
				return app.Result{}, err
			}
			limit, err := limitOf(a.Limit, maxTMLimit)
			if err != nil {
				return app.Result{}, err
			}
			minScore := 0
			if a.MinScore != nil {
				if *a.MinScore < 50 || *a.MinScore > 101 {
					return app.Result{}, invalid("min_score", "must be 50 to 101")
				}
				minScore = *a.MinScore
			}
			matches, err := k.SearchTM(ctx, TMSearch{
				Project: project, Text: a.Text, Syntax: a.Syntax,
				SourceLocale: a.SourceLocale, TargetLocale: a.TargetLocale,
				MessageKey: a.MessageKey, Namespace: a.Namespace, Limit: limit, MinScore: minScore,
			})
			if err != nil {
				return app.Result{}, err
			}
			if matches == nil {
				matches = []TMMatch{}
			}
			best := ""
			if len(matches) > 0 {
				best = fmt.Sprintf(", best %d%%", matches[0].Score)
			}
			return app.Result{
				Explanation: fmt.Sprintf("%s in %s%s.",
					plural(len(matches), "match", "matches"), a.TargetLocale, best),
				Data: TMSearchResult{Matches: matches},
			}, nil
		},
	}
}

const tmSearchSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id; its units and the tenant-wide ones are searched."},
    "text": {"type": "string", "maxLength": 4000, "description": "The source text to look up."},
    "syntax": {"type": "string", "enum": ["mf1", "mf2"], "description": "How text is written; the project's default when absent."},
    "source_locale": {"type": "string", "maxLength": 35},
    "target_locale": {"type": "string", "maxLength": 35},
    "message_key": {"type": "string", "maxLength": 200, "description": "The message this text belongs to, for in-context scoring."},
    "namespace": {"type": "string", "maxLength": 200},
    "limit": {"type": "integer", "minimum": 1, "maximum": 50, "description": "How many matches; 20 by default."},
    "min_score": {"type": "integer", "minimum": 50, "maximum": 101, "description": "Lowest score returned; 100 asks for exact matches only."}
  },
  "required": ["project", "text", "source_locale", "target_locale"],
  "additionalProperties": false
}`

// TermLookupName is the termbase tool's wire name.
const TermLookupName = "term_lookup"

type termLookupArgs struct {
	Project      string `json:"project"`
	Text         string `json:"text"`
	Locale       string `json:"locale"`
	TargetLocale string `json:"target_locale"`
	Limit        *int   `json:"limit"`
}

// TermLookupResult is the terms recognized in a text.
type TermLookupResult struct {
	Terms []Term `json:"terms"`
	// Truncated says the text held more recognized terms than the limit
	// allowed. Recognition walks the text once and issues no cursor.
	Truncated bool `json:"truncated"`
}

// termLookup recognizes termbase terms in a text (RFC 0005 §7.3).
func termLookup(k Knowledge) app.Tool {
	return app.Tool{
		Name:  TermLookupName,
		Title: "Look terms up",
		Description: "Find the termbase concepts and terms a text contains, with each term's status " +
			"(preferred, allowed, deprecated, forbidden) and the concept's definition. Pass " +
			"target_locale to also get the concept's terms in that locale, allowed ones first — " +
			"which is what tells you the word to use in a translation.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermKnowledgeRead,
		Selectors:   selectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(termLookupSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a termLookupArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if err := boundedText("text", a.Text); err != nil {
				return app.Result{}, err
			}
			if err := required("locale", a.Locale); err != nil {
				return app.Result{}, err
			}
			limit, err := limitOf(a.Limit, maxTermLimit)
			if err != nil {
				return app.Result{}, err
			}
			terms, err := k.LookupTerms(ctx, TermSearch{
				Project: project, Text: a.Text, Locale: a.Locale, TargetLocale: a.TargetLocale,
			})
			if err != nil {
				return app.Result{}, err
			}
			res := TermLookupResult{Terms: terms}
			if res.Terms == nil {
				res.Terms = []Term{}
			}
			if len(res.Terms) > limit {
				res.Terms, res.Truncated = res.Terms[:limit], true
			}
			return app.Result{
				Explanation: fmt.Sprintf("Recognized %s in this %s text.",
					plural(len(res.Terms), "term", "terms"), a.Locale),
				Data: res,
			}, nil
		},
	}
}

const termLookupSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id; its concepts and the tenant-wide ones are searched."},
    "text": {"type": "string", "maxLength": 4000, "description": "The text to recognize terms in."},
    "locale": {"type": "string", "maxLength": 35, "description": "The locale the text is written in."},
    "target_locale": {"type": "string", "maxLength": 35, "description": "Also return each concept's terms in this locale."},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "How many recognized terms; 20 by default."}
  },
  "required": ["project", "text", "locale"],
  "additionalProperties": false
}`

// StyleRulesName is the style tool's wire name.
const StyleRulesName = "style_rules"

type styleRulesArgs struct {
	Project   string `json:"project"`
	Locale    string `json:"locale"`
	Namespace string `json:"namespace"`
}

// styleRules returns the effective style guide for a locale and
// namespace (RFC 0005 §7.3).
func styleRules(k Knowledge) app.Tool {
	return app.Tool{
		Name:  StyleRulesName,
		Title: "The effective style guide",
		Description: "The style that applies to a project, locale and namespace: the mechanical " +
			"fields (formality, tone, punctuation, numbers, dates) and the explicit rules, with " +
			"every applicable guide merged narrowest-last, and the guides it came from. Rules a " +
			"narrower guide switched off are not listed.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermKnowledgeRead,
		Selectors:   selectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(styleRulesSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a styleRulesArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			style, err := k.Style(ctx, project, a.Locale, a.Namespace)
			if err != nil {
				return app.Result{}, err
			}
			if style.Rules == nil {
				style.Rules = []StyleRule{}
			}
			if style.Sources == nil {
				style.Sources = []StyleGuideRef{}
			}
			return app.Result{
				Explanation: fmt.Sprintf("%s from %s.",
					plural(len(style.Rules), "style rule", "style rules"),
					plural(len(style.Sources), "guide", "guides")),
				Data: style,
			}, nil
		},
	}
}

const styleRulesSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "locale": {"type": "string", "maxLength": 35, "description": "Include locale-scoped guides for this locale; without it, none apply."},
    "namespace": {"type": "string", "maxLength": 200, "description": "Include namespace-scoped guides for this namespace."}
  },
  "required": ["project"],
  "additionalProperties": false
}`
