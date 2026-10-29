package tools

import (
	"context"
	"encoding/json"
	"fmt"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// UsagesGetName is the usages tool's wire name.
const UsagesGetName = "usages_get"

type usagesGetArgs struct {
	Project string `json:"project"`
	Key     string `json:"key"`
	Branch  string `json:"branch"`
	Limit   *int   `json:"limit"`
}

// usagesGet answers where in the product a message is asked for
// (RFC 0005 §7.3: `file:line (component, route)`).
func usagesGet(u UsageReader) app.Tool {
	return app.Tool{
		Name:  UsagesGetName,
		Title: "Where a message is used",
		Description: "List where the product asks for a message: file and line, the component and " +
			"the route where the collector knows them, and which branch's build reported it. " +
			"Bounded rather than paginated — Context answers a message's usages in one read — so a " +
			"truncated result says more exist than the limit allowed.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermCatalogRead,
		Selectors:   selectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(usagesGetSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a usagesGetArgs
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
			limit, err := limitOf(a.Limit, MaxLimit)
			if err != nil {
				return app.Result{}, err
			}
			got, err := u.OfKey(ctx, project, a.Key, a.Branch, limit)
			if err != nil {
				return app.Result{}, err
			}
			if got.Usages == nil {
				got.Usages = []Usage{}
			}
			more := ""
			if got.Truncated {
				more = ", and more than the limit allowed"
			}
			return app.Result{
				Explanation: fmt.Sprintf("%s is used in %s%s.",
					a.Key, plural(len(got.Usages), "place", "places"), more),
				Data: got,
			}, nil
		},
	}
}

const usagesGetSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "key": {"type": "string", "maxLength": 200, "description": "The message key."},
    "branch": {"type": "string", "maxLength": 255, "description": "Read the branch's view; the default branch when absent."},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "How many usages to return; 20 by default."}
  },
  "required": ["project", "key"],
  "additionalProperties": false
}`
