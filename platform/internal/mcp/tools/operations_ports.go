package tools

import (
	"context"

	"github.com/google/uuid"
)

// ── Operations (RFC 0006 §8) ────────────────────────────────────────
//
// The M5 read ports: assignments, a translation unit's workflow, the
// per-vendor quality numbers, release requests and rollouts. Each is
// the use case the API calls, so who may read what — project scope,
// an `assigned` member's own work, the permissions — is the owning
// context's to decide and is never restated here.
//
// There is no decision port. Approving a translation or a release is a
// person's act: no scope grants approvals.decide, so an agent could not
// approve through a tool even if one existed, and none does (§3.2,
// §9.3; RFC 0005 §7.4). Nothing here writes a workflow definition
// either.

// AssignmentUnit is one translation unit of an assignment.
type AssignmentUnit struct {
	MessageID string `json:"message_id"`
	Locale    string `json:"locale"`
}

// Assignment is a batch of translation units given to someone.
//
// Omitted: who made it and its version. Units are capped at
// MaxAssignmentUnitsShown; UnitCount says how many there are.
type Assignment struct {
	ID         string           `json:"id"`
	ProjectID  string           `json:"project_id"`
	InstanceID string           `json:"instance_id,omitempty"`
	Assignee   string           `json:"assignee"`
	Permission string           `json:"permission"`
	State      string           `json:"state"`
	DueAt      string           `json:"due_at,omitempty"`
	Units      []AssignmentUnit `json:"units"`
	UnitCount  int              `json:"unit_count"`
	CreatedAt  string           `json:"created_at"`
	ClosedAt   string           `json:"closed_at,omitempty"`
}

// MaxAssignmentUnitsShown bounds the units one assignment lists in a
// tool result: an assignment may hold a thousand.
const MaxAssignmentUnitsShown = 50

// AssignmentQuery narrows assignments_list.
type AssignmentQuery struct {
	Project uuid.UUID
	// Key names a message by key, in Project.
	Key    string
	Locale string
	State  string
	// Mine asks for the caller's own work even when they may see more.
	Mine   bool
	Cursor string
	Limit  int
}

// Binding is one binding of a definition to the project.
type Binding struct {
	ID             string   `json:"id"`
	Subject        string   `json:"subject"`
	Locales        []string `json:"locales,omitempty"`
	Namespace      string   `json:"namespace,omitempty"`
	DefinitionID   string   `json:"definition_id"`
	DefinitionName string   `json:"definition_name,omitempty"`
}

// GuardOutcome is one guard a transition evaluated.
type GuardOutcome struct {
	Guard  string `json:"guard"`
	Passed bool   `json:"passed"`
}

// ActionOutcome is one action a transition ran.
type ActionOutcome struct {
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

// Transition is one row of an instance's log.
type Transition struct {
	Seq     int64           `json:"seq"`
	From    string          `json:"from"`
	Event   string          `json:"event"`
	To      string          `json:"to"`
	Outcome string          `json:"outcome"`
	Actor   string          `json:"actor"`
	At      string          `json:"at"`
	Guards  []GuardOutcome  `json:"guards,omitempty"`
	Actions []ActionOutcome `json:"actions,omitempty"`
}

// Instance is one workflow instance, with its log when one message was
// asked about.
type Instance struct {
	ID           string       `json:"id"`
	DefinitionID string       `json:"definition_id"`
	Version      int          `json:"version"`
	Subject      string       `json:"subject"`
	SubjectID    string       `json:"subject_id"`
	Locale       string       `json:"locale,omitempty"`
	State        string       `json:"state"`
	Status       string       `json:"status"`
	CreatedAt    string       `json:"created_at"`
	UpdatedAt    string       `json:"updated_at"`
	Transitions  []Transition `json:"transitions,omitempty"`
}

// WorkflowStateQuery narrows workflow_state.
type WorkflowStateQuery struct {
	Key    string
	Locale string
	Status string
	Limit  int
}

// WorkflowState is a project's bindings and the instances asked about.
type WorkflowState struct {
	Bindings  []Binding  `json:"bindings"`
	Instances []Instance `json:"instances"`
	// More says further instances matched beyond Limit.
	More bool `json:"more"`
}

// QualityQuery narrows assignments_report.
type QualityQuery struct {
	Project uuid.UUID
	// Assignee is a stored spelling ("vendor:<uuid>", "member:<uuid>",
	// "group:<uuid>", "role:<name>").
	Assignee string
	// Since is an RFC 3339 time; "" is no lower bound.
	Since string
}

// Workflow is Workflow's application services, as MCP reads them.
type Workflow interface {
	// Assignments lists what the caller may see: everything in their
	// project scope with assignments.manage, their own work otherwise.
	Assignments(ctx context.Context, q AssignmentQuery) (items []Assignment, next string, err error)
	// State reads a project's bindings and its instances, with each
	// instance's transition log when q names a message.
	State(ctx context.Context, project uuid.UUID, q WorkflowStateQuery) (WorkflowState, error)
	// Report aggregates completed assignments' quality numbers by
	// assignee and locale (RFC 0006 §3.4).
	Report(ctx context.Context, q QualityQuery) (QualityReport, error)
}

// QualityRow is one assignee's completed work in one locale; the
// fields are Workflow's (workflow/app.QualityRow), and their meaning is
// documented there and in RFC 0006 §3.4.
type QualityRow struct {
	Assignee         string         `json:"assignee"`
	Locale           string         `json:"locale"`
	Assignments      int            `json:"assignments"`
	OnTime           int            `json:"on_time"`
	Late             int            `json:"late"`
	NoDue            int            `json:"no_due"`
	Units            int            `json:"units"`
	Unavailable      int            `json:"unavailable"`
	SourceWords      int            `json:"source_words"`
	TMWords          map[string]int `json:"tm_words"`
	Approved         int            `json:"approved"`
	Rejected         int            `json:"rejected"`
	InReview         int            `json:"needs_review"`
	Draft            int            `json:"draft"`
	Unreviewed       int            `json:"unreviewed"`
	Changed          int            `json:"changed_after_delivery"`
	MeanEditDistance float64        `json:"mean_edit_distance"`
	MeanEditRatio    float64        `json:"mean_edit_ratio"`
	Findings         map[string]int `json:"findings"`
	FindingsPerUnit  float64        `json:"findings_per_unit"`
	Reworked         int            `json:"reworked"`
	ReworkRate       float64        `json:"rework_rate"`
	OnTimeRate       float64        `json:"on_time_rate"`
}

// QualityReport is the rows, by assignee and locale.
type QualityReport struct {
	Since       string       `json:"since,omitempty"`
	GeneratedAt string       `json:"generated_at"`
	Rows        []QualityRow `json:"rows"`
	// Truncated says a bound stopped the report before every completed
	// assignment was read.
	Truncated bool `json:"truncated"`
}

// ReleaseRequest is a publish or a promote held for approval.
type ReleaseRequest struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
	ReleaseID   string `json:"release_id"`
	Action      string `json:"action"`
	Requester   string `json:"requester"`
	// ApprovalsRequired is how many distinct people other than the
	// requester must approve; Approvals the grants on its current
	// approval so far, when the caller may read approvals.
	ApprovalsRequired int    `json:"approvals_required"`
	Approvals         *int   `json:"approvals_granted,omitempty"`
	State             string `json:"state"`
	// Forced says the requester overrode the publish gate; ForceReason
	// is their reason, which approvers see.
	Forced      bool     `json:"forced"`
	ForceReason string   `json:"force_reason,omitempty"`
	GateMet     bool     `json:"gate_met"`
	GateUnmet   []string `json:"gate_unmet,omitempty"`
	CreatedAt   string   `json:"created_at"`
	DecidedBy   string   `json:"decided_by,omitempty"`
	DecidedAt   string   `json:"decided_at,omitempty"`
}

// Rollout is a staged rollout of a candidate release.
type Rollout struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
	Candidate   string `json:"candidate_release_id"`
	Stable      string `json:"stable_release_id"`
	Percent     int    `json:"percent"`
	Status      string `json:"status"`
	End         string `json:"end,omitempty"`
	StartedBy   string `json:"started_by"`
	StartedAt   string `json:"started_at"`
	ExpiresAt   string `json:"expires_at"`
	EndedAt     string `json:"ended_at,omitempty"`
}

// ReleaseReads is Release's read side of approvals and rollouts.
type ReleaseReads interface {
	// ReleaseRequests lists a project's release requests, newest first,
	// in one environment and one state when given.
	ReleaseRequests(ctx context.Context, project uuid.UUID, environment, state, cursor string, limit int) ([]ReleaseRequest, string, error)
	// Rollouts lists an environment's rollouts, newest first.
	Rollouts(ctx context.Context, project uuid.UUID, environment, cursor string, limit int) ([]Rollout, string, error)
}
