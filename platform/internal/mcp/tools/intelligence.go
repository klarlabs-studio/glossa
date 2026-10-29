package tools

import (
	"context"
	"encoding/json"
	"fmt"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// MaxTranslateLocales and MaxTranslateKeys bound one translate call.
// They are smaller than Intelligence's own caps on purpose: a tool
// result is read by a model, and a fill of forty locales is a bill
// rather than a request.
const (
	MaxTranslateLocales = 20
	MaxTranslateKeys    = 200
)

// TranslateName is the fill tool's wire name.
const TranslateName = "translate"

type translateArgs struct {
	Project   string   `json:"project"`
	Locales   []string `json:"locales"`
	Keys      []string `json:"keys"`
	Namespace string   `json:"namespace"`
	KeyPrefix string   `json:"key_prefix"`
	Select    string   `json:"select"`
}

// translate starts an M2 fill job and returns its id (RFC 0005 §7.3).
//
// The job runs server-side under the tenant's own provider
// configuration and budget. No provider key crosses MCP in either
// direction: the client never sees one, cannot set one, and there is no
// tool for `ai-providers` at all (RFC 0005 §7.4). Sensitive namespaces
// are never machine-translated, and Intelligence skips them itself —
// this tool does not re-implement that rule, it reports the skip.
//
// What comes back is a fill id, not a translation: the jobs run
// asynchronously, and what they write enters review like any other
// AI-written text.
func translate(t Translator) app.Tool {
	return app.Tool{
		Name:  TranslateName,
		Title: "Translate with AI",
		Description: "Queue an AI fill for a project's untranslated (or outdated) messages in one " +
			"or more locales, and return the fill's id and job counts. The jobs run on the server " +
			"under the tenant's own provider settings and budget — no provider or API key is " +
			"passed in or returned — and what they write enters review, never an approved " +
			"translation. Messages in a sensitive namespace are skipped and counted. This tool " +
			"starts work and does not wait for it. Needs a write session on a token carrying the " +
			"write scope.",
		Toolset:     domain.ToolsetWrite,
		Permission:  identity.PermIntelligenceTranslate,
		Selectors:   selectors,
		InputSchema: json.RawMessage(translateSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a translateArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if len(a.Locales) == 0 {
				return app.Result{}, invalid("locales", "names at least one locale")
			}
			if len(a.Locales) > MaxTranslateLocales {
				return app.Result{}, invalid("locales",
					fmt.Sprintf("names at most %d locales in one call", MaxTranslateLocales))
			}
			if len(a.Keys) > MaxTranslateKeys {
				return app.Result{}, invalid("keys",
					fmt.Sprintf("names at most %d messages in one call", MaxTranslateKeys))
			}
			job, err := t.Translate(ctx, project, TranslateRequest{
				Locales: a.Locales, Keys: a.Keys, Namespace: a.Namespace,
				KeyPrefix: a.KeyPrefix, Select: a.Select,
			})
			if err != nil {
				return app.Result{}, err
			}
			warn := ""
			if len(job.Warnings) > 0 {
				warn = fmt.Sprintf(" It will do little or nothing: %v.", job.Warnings)
			}
			return app.Result{
				Explanation: fmt.Sprintf(
					"Fill %s queued %s across %s (%d already existed); what they write enters review.%s",
					job.Fill, plural(job.JobsCreated, "job", "jobs"),
					plural(len(job.Locales), "locale", "locales"), job.JobsExisting, warn),
				Data:     job,
				Affected: []string{job.Fill},
			}, nil
		},
	}
}

const translateSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "locales": {
      "type": "array", "minItems": 1, "maxItems": 20, "uniqueItems": true,
      "items": {"type": "string", "maxLength": 35},
      "description": "The target locales to fill; each must already be one of the project's."
    },
    "keys": {
      "type": "array", "maxItems": 200, "uniqueItems": true,
      "items": {"type": "string", "maxLength": 200},
      "description": "Translate only these messages; the whole project when absent."
    },
    "namespace": {"type": "string", "maxLength": 200, "description": "Only messages in this namespace."},
    "key_prefix": {"type": "string", "maxLength": 200, "description": "Only messages whose key starts with this."},
    "select": {
      "type": "string", "enum": ["missing", "outdated", "missing_or_outdated"],
      "description": "Which messages to fill, by the state of their translation; missing when absent."
    }
  },
  "required": ["project", "locales"],
  "additionalProperties": false
}`
