package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// The M5 read tools (RFC 0006 §8): assignments_list, workflow_state
// and release_requests_list, which §8 names, and assignments_report and
// rollouts_list, which carry §3.4's quality numbers and §5.2's rollout
// state to the same surface. All five are read tools, each declaring
// the permission its use case checks; the use case checks it again,
// with project scope and an `assigned` member's visibility, because
// the tool calls exactly the service the API calls.
//
// What is not here is the point: no tool approves, denies, accepts,
// completes or assigns, and none writes a workflow definition. An
// approval is a person's decision, and no scope grants approvals.decide
// (RFC 0006 §3.2, §9.3; RFC 0005 §7.4).
const (
	AssignmentsListName     = "assignments_list"
	AssignmentsReportName   = "assignments_report"
	WorkflowStateName       = "workflow_state"
	ReleaseRequestsListName = "release_requests_list"
	RolloutsListName        = "rollouts_list"
)

// opsSelectors are the arguments the M5 tools may have recorded verbatim
// in the audit ledger: ids, states and enumerations, a time.
var opsSelectors = append(slices.Clone(selectors), "mine", "status", "assignee", "since", "cursor")

// Operations returns the M5 read tools whose ports are wired.
func Operations(s Sources) []app.Tool {
	var out []app.Tool
	if s.Workflow != nil {
		out = append(out, assignmentsList(s.Workflow), assignmentsReport(s.Workflow), workflowState(s.Workflow))
	}
	if s.ReleaseReads != nil {
		out = append(out, releaseRequestsList(s.ReleaseReads), rolloutsList(s.ReleaseReads))
	}
	return out
}

// ── assignments_list ────────────────────────────────────────────────

type assignmentsListArgs struct {
	Project string `json:"project"`
	Key     string `json:"key"`
	Locale  string `json:"locale"`
	State   string `json:"state"`
	Mine    bool   `json:"mine"`
	Cursor  string `json:"cursor"`
	Limit   *int   `json:"limit"`
}

var assignmentStates = []string{"open", "accepted", "done", "declined", "expired"}

func assignmentsList(w Workflow) app.Tool {
	return app.Tool{
		Name:  AssignmentsListName,
		Title: "List assignments",
		Description: "List assignments: batches of translation units given to a member, a role, a group or a " +
			"vendor, with their state and due date. Exactly what the API's list answers the same credential: " +
			"every assignment in its project scope for a caller holding assignments.manage, otherwise the " +
			"caller's own work. An API token is no assignee and no token scope grants assignments.manage, so " +
			"a token's list is empty — the answer says so rather than pretending there is no work. Read-only: " +
			"accepting, completing and assigning are people's acts in Studio.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermAssignmentsRead,
		Selectors:   opsSelectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(assignmentsListSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a assignmentsListArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			q := AssignmentQuery{Key: a.Key, Locale: a.Locale, State: a.State, Mine: a.Mine, Cursor: a.Cursor}
			if a.Project != "" {
				p, err := projectOf(a.Project)
				if err != nil {
					return app.Result{}, err
				}
				q.Project = p
			} else if a.Key != "" {
				return app.Result{}, invalid("key", "names a message, which needs project")
			}
			if a.State != "" && !slices.Contains(assignmentStates, a.State) {
				return app.Result{}, invalid("state", "is one of open, accepted, done, declined, expired")
			}
			limit, err := limitOf(a.Limit, MaxLimit)
			if err != nil {
				return app.Result{}, err
			}
			q.Limit = limit
			items, next, err := w.Assignments(ctx, q)
			if err != nil {
				return app.Result{}, err
			}
			explanation := fmt.Sprintf("%s visible to this session.", plural(len(items), "assignment is", "assignments are"))
			if len(items) == 0 {
				explanation = "No assignments are visible to this session. Work is given to people — a member, a role, " +
					"a group or a vendor — and an API token is none of them; seeing everyone's takes assignments.manage, " +
					"which no token scope grants."
			}
			return app.Result{Explanation: explanation, Data: map[string]any{"assignments": items, "next_cursor": next}}, nil
		},
	}
}

const assignmentsListSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "Only this project's assignments."},
    "key": {"type": "string", "maxLength": 200, "description": "Only assignments with a unit of this message (needs project)."},
    "locale": {"type": "string", "maxLength": 35, "description": "Only assignments with a unit in this BCP 47 locale."},
    "state": {"type": "string", "enum": ["open", "accepted", "done", "declined", "expired"]},
    "mine": {"type": "boolean", "description": "Only the caller's own work, even for a caller who may see more."},
    "cursor": {"type": "string", "maxLength": 200, "description": "The next_cursor of a previous page."},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100}
  },
  "additionalProperties": false
}`

// ── assignments_report ──────────────────────────────────────────────

type assignmentsReportArgs struct {
	Project  string `json:"project"`
	Assignee string `json:"assignee"`
	Since    string `json:"since"`
}

func assignmentsReport(w Workflow) app.Tool {
	return app.Tool{
		Name:  AssignmentsReportName,
		Title: "Report vendors' quality",
		Description: "The quality numbers of completed assignments, by assignee (a vendor, member, group or role) " +
			"and locale (RFC 0006 §3.4): assignments on time and late; units, source words and words delivered from " +
			"translation memory; review outcomes now; units changed after delivery and the mean edit on reviewed " +
			"units; open findings by layer and per unit; and rework, units given out again after completion. " +
			"Computed on read from the assignments, the catalog, the translations' revision logs and the findings, " +
			"as this session — nothing it could not read unit by unit. Narrow it with project, assignee " +
			"(\"vendor:<id>\") and since (RFC 3339).",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermAssignmentsRead,
		Selectors:   opsSelectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(assignmentsReportSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a assignmentsReportArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			q := QualityQuery{Assignee: a.Assignee, Since: a.Since}
			if a.Project != "" {
				p, err := projectOf(a.Project)
				if err != nil {
					return app.Result{}, err
				}
				q.Project = p
			}
			if a.Since != "" {
				if _, err := time.Parse(time.RFC3339, a.Since); err != nil {
					return app.Result{}, invalid("since", "is an RFC 3339 time, e.g. 2026-09-01T00:00:00Z")
				}
			}
			r, err := w.Report(ctx, q)
			if err != nil {
				return app.Result{}, err
			}
			units := 0
			for _, row := range r.Rows {
				units += row.Units
			}
			explanation := fmt.Sprintf("%s delivered in %s.", plural(units, "unit", "units"),
				plural(len(r.Rows), "assignee and locale", "assignee-and-locale rows"))
			if r.Truncated {
				explanation += " The report stopped at its bound; narrow it with project, assignee or since."
			}
			return app.Result{Explanation: explanation, Data: r}, nil
		},
	}
}

const assignmentsReportSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "Only this project's assignments."},
    "assignee": {"type": "string", "maxLength": 60, "description": "Only this assignee: vendor:<id>, member:<id>, group:<id> or role:<name>."},
    "since": {"type": "string", "format": "date-time", "description": "Only assignments completed at or after this time."}
  },
  "additionalProperties": false
}`

// ── workflow_state ──────────────────────────────────────────────────

type workflowStateArgs struct {
	Project string `json:"project"`
	Key     string `json:"key"`
	Locale  string `json:"locale"`
	Status  string `json:"status"`
	Limit   *int   `json:"limit"`
}

// maxStateInstances bounds the instances workflow_state returns; with a
// message named, each carries its whole transition log.
const maxStateInstances = 20

func workflowState(w Workflow) app.Tool {
	return app.Tool{
		Name:  WorkflowStateName,
		Title: "Read a workflow's state",
		Description: "Where work stands in a project's workflows: the definitions bound to the project, and the " +
			"workflow instances — one per translation unit or release request in flight — with their current " +
			"stage. Name a message by key (and a locale) to get its instances' transition logs too: every event " +
			"that reached them, the guards evaluated, the actions run and who caused each. A workflow stage is not " +
			"a review state: translation_get reports that.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermWorkflowsRead,
		Selectors:   opsSelectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(workflowStateSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a workflowStateArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if a.Status != "" && a.Status != "active" && a.Status != "finished" {
				return app.Result{}, invalid("status", "is active or finished")
			}
			limit, err := limitOf(a.Limit, maxStateInstances)
			if err != nil {
				return app.Result{}, err
			}
			st, err := w.State(ctx, project, WorkflowStateQuery{Key: a.Key, Locale: a.Locale, Status: a.Status, Limit: limit})
			if err != nil {
				return app.Result{}, err
			}
			explanation := fmt.Sprintf("The project binds %s; %s.",
				plural(len(st.Bindings), "definition", "definitions"), plural(len(st.Instances), "instance matches", "instances match"))
			if len(st.Bindings) == 0 {
				explanation = "No workflow is bound to this project: its translations move through review as they always have."
			}
			return app.Result{Explanation: explanation, Data: st}, nil
		},
	}
}

const workflowStateSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "key": {"type": "string", "maxLength": 200, "description": "A message key: its instances, with their transition logs."},
    "locale": {"type": "string", "maxLength": 35, "description": "Only instances of this BCP 47 locale."},
    "status": {"type": "string", "enum": ["active", "finished"]},
    "limit": {"type": "integer", "minimum": 1, "maximum": 20}
  },
  "required": ["project"],
  "additionalProperties": false
}`

// ── release_requests_list ───────────────────────────────────────────

type releaseRequestsArgs struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	State       string `json:"state"`
	Cursor      string `json:"cursor"`
	Limit       *int   `json:"limit"`
}

var requestStates = []string{"pending", "deployed", "denied", "withdrawn", "refused"}

func releaseRequestsList(r ReleaseReads) app.Tool {
	return app.Tool{
		Name:  ReleaseRequestsListName,
		Title: "List release requests",
		Description: "List a project's release requests, newest first: publishes and promotions into an environment " +
			"that requires approval, held until enough people other than the requester approve. Each says what it " +
			"would deploy, who asked, how many approvals it needs and has, whether the requester forced the publish " +
			"gate and why, and how it ended. Read-only: approving is a person's decision, and there is no tool for it.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermReleasesRead,
		Selectors:   opsSelectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(releaseRequestsSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a releaseRequestsArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if a.State != "" && !slices.Contains(requestStates, a.State) {
				return app.Result{}, invalid("state", "is one of pending, deployed, denied, withdrawn, refused")
			}
			limit, err := limitOf(a.Limit, MaxLimit)
			if err != nil {
				return app.Result{}, err
			}
			items, next, err := r.ReleaseRequests(ctx, project, a.Environment, a.State, a.Cursor, limit)
			if err != nil {
				return app.Result{}, err
			}
			pending := 0
			for _, x := range items {
				if x.State == "pending" {
					pending++
				}
			}
			return app.Result{
				Explanation: fmt.Sprintf("%s, %d pending approval.", plural(len(items), "release request", "release requests"), pending),
				Data:        map[string]any{"release_requests": items, "next_cursor": next},
			}, nil
		},
	}
}

const releaseRequestsSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "environment": {"type": "string", "maxLength": 63, "description": "Only this environment's requests."},
    "state": {"type": "string", "enum": ["pending", "deployed", "denied", "withdrawn", "refused"]},
    "cursor": {"type": "string", "maxLength": 200, "description": "The next_cursor of a previous page."},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100}
  },
  "required": ["project"],
  "additionalProperties": false
}`

// ── rollouts_list ───────────────────────────────────────────────────

type rolloutsArgs struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Cursor      string `json:"cursor"`
	Limit       *int   `json:"limit"`
}

func rolloutsList(r ReleaseReads) app.Tool {
	return app.Tool{
		Name:  RolloutsListName,
		Title: "List staged rollouts",
		Description: "List an environment's staged rollouts, newest first: the active one, if any — which candidate " +
			"release, at what percentage of installations, until when — and the history of ended ones and how each " +
			"ended (completed, aborted, expired, rolled back). Read-only: starting, advancing and ending a rollout " +
			"are `glossa release rollout`'s.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermReleasesRead,
		Selectors:   opsSelectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(rolloutsSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a rolloutsArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if a.Environment == "" {
				a.Environment = "production"
			}
			limit, err := limitOf(a.Limit, MaxLimit)
			if err != nil {
				return app.Result{}, err
			}
			items, next, err := r.Rollouts(ctx, project, a.Environment, a.Cursor, limit)
			if err != nil {
				return app.Result{}, err
			}
			explanation := fmt.Sprintf("%s has no active rollout (%s in its history).", a.Environment, plural(len(items), "rollout", "rollouts"))
			if len(items) > 0 && items[0].Status == "active" {
				explanation = fmt.Sprintf("%s is rolling release %s out to %d%% of installations.", a.Environment, items[0].Candidate, items[0].Percent)
			}
			return app.Result{Explanation: explanation, Data: map[string]any{"rollouts": items, "next_cursor": next}}, nil
		},
	}
}

const rolloutsSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "environment": {"type": "string", "maxLength": 63, "description": "The environment; production when absent."},
    "cursor": {"type": "string", "maxLength": 200, "description": "The next_cursor of a previous page."},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100}
  },
  "required": ["project"],
  "additionalProperties": false
}`
