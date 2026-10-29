package domain

import (
	"time"

	"github.com/google/uuid"
)

// The domain events assignments and approvals publish through the
// outbox, named by the platform convention
// "<context>.<aggregate>.<past-tense verb>". Four of them are what the
// vocabulary calls assignment.completed, assignment.declined,
// approval.granted and approval.denied (§2.4); VocabularyEvent maps
// them for the instance runner. The rest exist so that every change
// names who made it (§6.1) and the audit projection sees it.
const (
	EventTypeAssignmentCreated   = "workflow.assignment.created"
	EventTypeAssignmentAccepted  = "workflow.assignment.accepted"
	EventTypeAssignmentCompleted = "workflow.assignment.completed"
	EventTypeAssignmentDeclined  = "workflow.assignment.declined"
	EventTypeAssignmentExpired   = "workflow.assignment.expired"
	EventTypeApprovalRequested   = "workflow.approval.requested"
	EventTypeApprovalGranted     = "workflow.approval.granted"
	EventTypeApprovalDenied      = "workflow.approval.denied"
)

// Aggregate types of those events.
const (
	AggregateAssignment = "assignment"
	AggregateApproval   = "approval"
)

// VocabularyEvent is the vocabulary event an outbox event type moves an
// instance with, and false for a type that moves none.
func VocabularyEvent(outboxType string) (EventName, bool) {
	switch outboxType {
	case EventTypeAssignmentCompleted:
		return EventAssignmentCompleted, true
	case EventTypeAssignmentDeclined:
		return EventAssignmentDeclined, true
	case EventTypeApprovalGranted:
		return EventApprovalGranted, true
	case EventTypeApprovalDenied:
		return EventApprovalDenied, true
	}
	return "", false
}

// UnitRef is a translation unit in an event payload.
type UnitRef struct {
	MessageID string `json:"message_id"`
	Locale    string `json:"locale"`
}

// AssignmentEvent is the payload of every workflow.assignment.* event.
// The runner finds the instances to step from InstanceID or, for an
// assignment made by hand, from each unit.
type AssignmentEvent struct {
	AssignmentID string    `json:"assignment_id"`
	ProjectID    string    `json:"project_id"`
	InstanceID   string    `json:"instance_id,omitempty"`
	Units        []UnitRef `json:"units"`
	Assignee     string    `json:"assignee"`
	Permission   string    `json:"permission"`
	State        string    `json:"state"`
	DueAt        string    `json:"due_at,omitempty"`
	By           string    `json:"by"`
	Reason       string    `json:"reason,omitempty"`
}

// ApprovalEvent is the payload of every workflow.approval.* event. A
// granted event is raised for every grant, with Satisfied true once
// Required distinct grants count; the definition's approvals_at_least
// guard decides on its own count.
type ApprovalEvent struct {
	ApprovalID  string `json:"approval_id"`
	ProjectID   string `json:"project_id"`
	InstanceID  string `json:"instance_id,omitempty"`
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	Locale      string `json:"locale,omitempty"`
	Required    int    `json:"required"`
	Eligible    string `json:"eligible"`
	State       string `json:"state"`
	// Principal, Verdict and Reason are the decision that raised a
	// granted or denied event.
	Principal string   `json:"principal,omitempty"`
	Verdict   string   `json:"verdict,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Approvers []string `json:"approvers"`
	Satisfied bool     `json:"satisfied"`
	By        string   `json:"by"`
}

// AssignmentPayload builds the payload for a's event, by the actor who
// caused it.
func AssignmentPayload(a Assignment, by string) AssignmentEvent {
	e := AssignmentEvent{
		AssignmentID: a.ID.String(), ProjectID: a.ProjectID.String(), Assignee: a.Assignee.String(),
		Permission: a.Permission, State: string(a.State), By: by, Reason: a.Reason,
		Units: make([]UnitRef, len(a.Units)),
	}
	if a.InstanceID != uuid.Nil {
		e.InstanceID = a.InstanceID.String()
	}
	if a.DueAt != nil {
		e.DueAt = a.DueAt.UTC().Format(time.RFC3339)
	}
	for i, u := range a.Units {
		e.Units[i] = UnitRef{MessageID: u.Message.String(), Locale: u.Locale}
	}
	return e
}

// ApprovalPayload builds the payload for a's event, by the actor who
// caused it; d is the decision that raised it, if any, and author the
// subject's author the count was taken against.
func ApprovalPayload(a Approval, d *Decision, author, by string) ApprovalEvent {
	e := ApprovalEvent{
		ApprovalID: a.ID.String(), ProjectID: a.ProjectID.String(), SubjectKind: string(a.Subject.Kind),
		SubjectID: a.Subject.ID.String(), Locale: a.Subject.Locale, Required: a.Required,
		Eligible: a.Eligible.String(), State: string(a.State), Approvers: a.Approvers(),
		Satisfied: len(a.Granters(author)) >= a.Required, By: by,
	}
	if e.Approvers == nil {
		e.Approvers = []string{}
	}
	if a.InstanceID != uuid.Nil {
		e.InstanceID = a.InstanceID.String()
	}
	if d != nil {
		e.Principal, e.Verdict, e.Reason = d.Principal, string(d.Verdict), d.Reason
	}
	return e
}
