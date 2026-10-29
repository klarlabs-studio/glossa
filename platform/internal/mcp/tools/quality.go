package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// FindingsListName is the findings tool's wire name.
const FindingsListName = "findings_list"

type findingsListArgs struct {
	Project  string `json:"project"`
	Ref      string `json:"ref"`
	Layer    string `json:"layer"`
	Locale   string `json:"locale"`
	Severity string `json:"severity"`
	Key      string `json:"key"`
	Waived   bool   `json:"waived"`
	Limit    *int   `json:"limit"`
	Cursor   string `json:"cursor"`
}

// FindingsListResult is a page of one check run's findings.
type FindingsListResult struct {
	Run      CheckRun  `json:"run"`
	Findings []Finding `json:"findings"`
	// NextCursor continues the page; "" on the last one.
	NextCursor string `json:"next_cursor,omitempty"`
}

// findingsList reads the stored findings of a project's latest check
// run (RFC 0005 §7.3). It reads them through Quality's application
// service, never over HTTP: MCP is a second façade on the ports, not a
// client of the REST API (RFC 0005 §7.1).
func findingsList(q Quality) app.Tool {
	return app.Tool{
		Name:  FindingsListName,
		Title: "List quality findings",
		Description: "The stored findings of a project's latest completed check run, filtered by " +
			"layer, locale, severity, message key or waived. The run itself is returned with them: " +
			"which ref it checked, which policy version graded it, which layers it computed — a " +
			"layer that is not in that list was not looked at, which is not the same as clean. " +
			"Paginated: pass the returned next_cursor to continue.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermCatalogRead,
		Selectors:   selectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(findingsListSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a findingsListArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			limit, err := limitOf(a.Limit, MaxLimit)
			if err != nil {
				return app.Result{}, err
			}
			run, found, next, err := q.Findings(ctx, project, FindingsQuery{
				Ref: a.Ref, Layer: a.Layer, Locale: a.Locale, Severity: a.Severity,
				MessageKey: a.Key, WaivedOnly: a.Waived, Limit: limit, After: a.Cursor,
			})
			if err != nil {
				return app.Result{}, err
			}
			if found == nil {
				found = []Finding{}
			}
			return app.Result{
				Explanation: fmt.Sprintf("%s from the %s run of %s (%d error(s), %d warning(s), %d waived).",
					plural(len(found), "finding", "findings"), run.Conclusion, run.Ref,
					run.Errors, run.Warnings, run.Waived),
				Data: FindingsListResult{Run: run, Findings: found, NextCursor: next},
			}, nil
		},
	}
}

const findingsListSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "ref": {"type": "string", "maxLength": 255, "description": "The branch or environment the run checked; the project's latest run when absent."},
    "layer": {"type": "string", "enum": ["structure", "parity", "completeness", "terminology", "style", "length", "locale", "source", "visual", "linguistic"]},
    "locale": {"type": "string", "maxLength": 35},
    "severity": {"type": "string", "enum": ["error", "warning", "waived"]},
    "key": {"type": "string", "maxLength": 200, "description": "Only findings about this message key."},
    "waived": {"type": "boolean", "description": "Only findings a waiver accepted."},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "Page size; 20 by default."},
    "cursor": {"type": "string", "description": "The next_cursor of the previous page."}
  },
  "required": ["project"],
  "additionalProperties": false
}`

// CheckRunName is the check tool's wire name.
const CheckRunName = "check_run"

type checkRunArgs struct {
	Project     string   `json:"project"`
	Environment string   `json:"environment"`
	Layers      []string `json:"layers"`
	Limit       *int     `json:"limit"`
}

// checkRun runs the deterministic layers over the project as it stands
// and answers with the findings and the policy's verdict
// (RFC 0005 §7.3).
//
// It is a **read** tool, and that is not a technicality. It computes;
// it stores nothing. `glossa check` in CI and the pull-request check
// record their runs because a verdict about a commit is history worth
// keeping; an agent asking "what is wrong right now" is asking a
// question, and a question should not need a write session or fill the
// run table. That is why RFC 0005 §7.3 gives check_run the `read`
// scope, and why the exit criterion has a read-only token run one.
func checkRun(c Checks) app.Tool {
	return app.Tool{
		Name:  CheckRunName,
		Title: "Run a check",
		Description: "Run the deterministic quality layers over the project as it stands now and " +
			"return what they found with the policy's verdict: the same layers, the same policy " +
			"and the same conclusion as `glossa check` and the pull-request check reach, because " +
			"there is one implementation. Nothing is stored — use findings_list to read the last " +
			"recorded run instead. Findings are bounded by limit; the counts are the whole run's.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermCatalogRead,
		Selectors:   append(slices.Clone(selectors), "layers"),
		ReadOnly:    true,
		InputSchema: json.RawMessage(checkRunSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a checkRunArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			limit, err := limitOf(a.Limit, MaxLimit)
			if err != nil {
				return app.Result{}, err
			}
			if len(a.Layers) > MaxLimit {
				return app.Result{}, invalid("layers", "names more layers than exist")
			}
			rep, err := c.Run(ctx, project, CheckRequest{
				Environment: a.Environment, Layers: a.Layers, Limit: limit,
			})
			if err != nil {
				return app.Result{}, err
			}
			if rep.Findings == nil {
				rep.Findings = []Finding{}
			}
			more := ""
			if rep.Truncated {
				more = fmt.Sprintf(" The first %d are listed; raise limit or read them by layer with findings_list.", limit)
			}
			return app.Result{
				Explanation: fmt.Sprintf("The check is a %s over %s: %d error(s) and %d warning(s) in %s.%s",
					rep.Conclusion, plural(rep.Messages, "message", "messages"), rep.Errors, rep.Warnings,
					plural(len(rep.Layers), "layer", "layers"), more),
				Data: rep,
			}, nil
		},
	}
}

const checkRunSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "environment": {"type": "string", "maxLength": 64, "description": "Grade against the policy's block for this environment; a branch check, in no environment, when absent."},
    "layers": {
      "type": "array", "maxItems": 10, "uniqueItems": true,
      "items": {"type": "string", "enum": ["structure", "parity", "completeness"]},
      "description": "The layers to compute; every deterministic layer when absent."
    },
    "limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "How many findings to return; 20 by default. The counts are the whole run's."}
  },
  "required": ["project"],
  "additionalProperties": false
}`
