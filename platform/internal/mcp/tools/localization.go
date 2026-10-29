package tools

import (
	"context"
	"encoding/json"
	"fmt"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// TranslationGetName is the translation read tool's wire name.
const TranslationGetName = "translation_get"

type translationGetArgs struct {
	Project string `json:"project"`
	Key     string `json:"key"`
	Locale  string `json:"locale"`
}

// translationGet reads one locale's text for a message, with its
// provenance, review state and outdated flag (RFC 0005 §7.3).
func translationGet(t Translations) app.Tool {
	return app.Tool{
		Name:  TranslationGetName,
		Title: "Read a translation",
		Description: "Read a message's translation in one locale: the text, its review state, who " +
			"and what wrote it (human, ai, translation-memory, import), the source revision it was " +
			"made against, and whether the source has moved on since (outdated).",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermTranslationsRead,
		Selectors:   selectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(translationGetSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a translationGetArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if err := required("key", a.Key); err != nil {
				return app.Result{}, err
			}
			if err := required("locale", a.Locale); err != nil {
				return app.Result{}, err
			}
			tr, err := t.Translation(ctx, project, a.Key, a.Locale)
			if err != nil {
				return app.Result{}, err
			}
			state := tr.State
			if tr.Outdated {
				state += ", outdated"
			}
			return app.Result{
				Explanation: fmt.Sprintf("%s in %s is %s (written by %s).", tr.Key, tr.Locale, state, tr.Origin),
				Data:        tr,
			}, nil
		},
	}
}

const translationGetSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "key": {"type": "string", "maxLength": 200, "description": "The message key."},
    "locale": {"type": "string", "maxLength": 35, "description": "A BCP 47 locale, e.g. \"de-AT\"."}
  },
  "required": ["project", "key", "locale"],
  "additionalProperties": false
}`

// TranslationProposeName is the translation write tool's wire name.
const TranslationProposeName = "translation_propose"

type translationProposeArgs struct {
	Project        string `json:"project"`
	Key            string `json:"key"`
	Locale         string `json:"locale"`
	Text           string `json:"text"`
	Syntax         string `json:"syntax"`
	SourceRevision *int   `json:"source_revision"`
	BaseRevision   *int   `json:"base_revision"`
}

// translationPropose writes a translation revision that always enters
// review (RFC 0005 §7.3, §7.4).
//
// The invariant is structural rather than conditional. The tool takes
// no review state, the port carries none, and the adapter asks
// Localization for `needs_review` on every write — so there is no
// argument, no project setting and no routing policy that can land an
// agent's text as an approved revision. It holds for the same reason a
// CI token cannot approve one: `translations.review` is in no scope's
// permission set, because review is a human decision.
func translationPropose(t TranslationWriter) app.Tool {
	return app.Tool{
		Name:  TranslationProposeName,
		Title: "Propose a translation",
		Description: "Write a translation for a message in one locale. It is always recorded as a " +
			"proposal waiting for review, never as an approved translation, whatever the project's " +
			"review settings say — an agent proposes and a person decides. The revision keeps the " +
			"token as its author. Read the message with message_get and the current text with " +
			"translation_get first; pass that translation's revision as base_revision to be told, " +
			"rather than to overwrite, when someone changed it meanwhile. Needs a write session on " +
			"a token carrying the write scope.",
		Toolset:     domain.ToolsetWrite,
		Permission:  identity.PermTranslationsWrite,
		Selectors:   selectors,
		InputSchema: json.RawMessage(translationProposeSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a translationProposeArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if err := required("key", a.Key); err != nil {
				return app.Result{}, err
			}
			if err := required("locale", a.Locale); err != nil {
				return app.Result{}, err
			}
			if err := boundedText("text", a.Text); err != nil {
				return app.Result{}, err
			}
			if a.SourceRevision != nil && *a.SourceRevision < 1 {
				return app.Result{}, invalid("source_revision", "is a source revision, which starts at 1")
			}
			if a.BaseRevision != nil && *a.BaseRevision < 1 {
				return app.Result{}, invalid("base_revision", "is a translation revision, which starts at 1")
			}
			tr, err := t.ProposeTranslation(ctx, project, TranslationProposal{
				Key: a.Key, Locale: a.Locale, Text: a.Text, Syntax: a.Syntax,
				SourceRevision: a.SourceRevision, BaseRevision: a.BaseRevision,
			})
			if err != nil {
				return app.Result{}, err
			}
			return app.Result{
				Explanation: fmt.Sprintf(
					"%s in %s is %s at revision %d, waiting for a reviewer; it was not approved.",
					tr.Key, tr.Locale, tr.State, tr.Revision),
				Data:     tr,
				Affected: []string{tr.MessageID},
			}, nil
		},
	}
}

const translationProposeSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "key": {"type": "string", "maxLength": 200, "description": "The message key."},
    "locale": {"type": "string", "maxLength": 35, "description": "A BCP 47 locale the project translates into; add it with locale_add first."},
    "text": {"type": "string", "maxLength": 4000, "description": "The translated text, in the project's syntax."},
    "syntax": {"type": "string", "enum": ["mf2", "icu"], "description": "The text's syntax; the project's default when absent."},
    "source_revision": {"type": "integer", "minimum": 1, "description": "The source revision this text translates; the current one when absent."},
    "base_revision": {"type": "integer", "minimum": 1, "description": "The translation revision read before proposing; the write is refused if it moved on."}
  },
  "required": ["project", "key", "locale", "text"],
  "additionalProperties": false
}`

// LocaleAddName is the locale write tool's wire name.
const LocaleAddName = "locale_add"

type localeAddArgs struct {
	Project string `json:"project"`
	Locale  string `json:"locale"`
}

// localeAdd adds a target locale to a project (RFC 0005 §7.3). There is
// no locale_remove: removing a locale destroys its translations, and no
// tool of M4 destroys data (RFC 0005 §7.2).
func localeAdd(l LocaleWriter) app.Tool {
	return app.Tool{
		Name:  LocaleAddName,
		Title: "Add a locale",
		Description: "Add a target locale to a project, so translations can be written for it. " +
			"Adding a locale the project already has changes nothing and is not an error. The " +
			"source locale is the project's and cannot be changed here, and there is no way to " +
			"remove a locale: that would destroy its translations. Needs a write session on a " +
			"token carrying the write scope.",
		Toolset:     domain.ToolsetWrite,
		Permission:  identity.PermCatalogWrite,
		Selectors:   selectors,
		InputSchema: json.RawMessage(localeAddSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a localeAddArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if err := required("locale", a.Locale); err != nil {
				return app.Result{}, err
			}
			added, err := l.AddLocale(ctx, project, a.Locale)
			if err != nil {
				return app.Result{}, err
			}
			was := "was already one of the project's locales"
			if added.Created {
				was = "is now one of the project's locales"
			}
			return app.Result{
				Explanation: fmt.Sprintf("%s %s.", added.Locale, was),
				Data:        added,
				Affected:    []string{added.Locale},
			}, nil
		},
	}
}

const localeAddSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "locale": {"type": "string", "maxLength": 35, "description": "A BCP 47 locale tag, e.g. \"pt-BR\"."}
  },
  "required": ["project", "locale"],
  "additionalProperties": false
}`
