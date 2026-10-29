package domain

// Catalog's domain events (platform/README.md, "Domain events"). Their
// names and payloads are a published contract: a breaking change is a
// new ".v2" type, never an edit.
const (
	AggregateProject     = "project"
	AggregateApplication = "application"
	AggregateMessage     = "message"
	AggregateBranch      = "branch"

	EventProjectCreated = "catalog.project.created"
	EventProjectUpdated = "catalog.project.updated"
	EventProjectDeleted = "catalog.project.deleted"

	EventApplicationCreated = "catalog.application.created"
	EventApplicationUpdated = "catalog.application.updated"
	EventApplicationDeleted = "catalog.application.deleted"

	EventMessageCreated       = "catalog.message.created"
	EventMessageSourceRevised = "catalog.message.source_revised"
	EventMessageUpdated       = "catalog.message.updated"
	EventMessageRenamed       = "catalog.message.renamed"
	EventMessageObsoleted     = "catalog.message.obsoleted"
	EventMessageReactivated   = "catalog.message.reactivated"
	// EventMessageActivated: a proposed message went live (the default
	// branch's push brought it in).
	EventMessageActivated = "catalog.message.activated"
	// EventMessageProposed: an obsolete message is proposed again by a
	// branch (a reopened PR, or a later push of its key).
	EventMessageProposed = "catalog.message.proposed"

	// EventBranchOpened: a branch exists (its first push, or CI's
	// upsert). Release opens its preview environment on it.
	EventBranchOpened   = "catalog.branch.opened"
	EventBranchPushed   = "catalog.branch.pushed"
	EventBranchClosed   = "catalog.branch.closed"
	EventBranchReopened = "catalog.branch.reopened"
	EventBranchMerged   = "catalog.branch.merged"
)

// ProjectEvent is the payload of every project event.
type ProjectEvent struct {
	ProjectID    string `json:"project_id"`
	Slug         string `json:"slug"`
	SourceLocale string `json:"source_locale"`
	Version      int    `json:"version"`
	By           string `json:"by"`
}

// ProjectEventOf builds the payload for p.
func ProjectEventOf(p Project, by Author) ProjectEvent {
	return ProjectEvent{
		ProjectID: p.ID.String(), Slug: string(p.Slug), SourceLocale: p.SourceLocale.String(),
		Version: p.Version, By: string(by),
	}
}

// ApplicationEvent is the payload of every application event.
type ApplicationEvent struct {
	ApplicationID string `json:"application_id"`
	ProjectID     string `json:"project_id"`
	Slug          string `json:"slug"`
	Platform      string `json:"platform"`
	By            string `json:"by"`
}

// ApplicationEventOf builds the payload for a.
func ApplicationEventOf(a Application, by Author) ApplicationEvent {
	return ApplicationEvent{
		ApplicationID: a.ID.String(), ProjectID: a.ProjectID.String(), Slug: string(a.Slug),
		Platform: string(a.Platform), By: string(by),
	}
}

// MessageSnapshot is the state of a message after the change an event
// reports. Every message event carries one, so a projection can apply
// events in any order and more than once: it keeps the snapshot with the
// highest Version (the outbox guarantees neither order nor uniqueness).
type MessageSnapshot struct {
	MessageID      string `json:"message_id"`
	ProjectID      string `json:"project_id"`
	Key            string `json:"key"`
	Namespace      string `json:"namespace"`
	State          string `json:"state"`
	SourceRevision int    `json:"source_revision"`
	Version        int    `json:"version"`
}

// SnapshotOf takes m's snapshot.
func SnapshotOf(m Message) MessageSnapshot {
	return MessageSnapshot{
		MessageID: m.ID.String(), ProjectID: m.ProjectID.String(), Key: string(m.Key),
		Namespace: string(m.Namespace), State: string(m.State), SourceRevision: m.Revision, Version: m.Version,
	}
}

// MessageEvent is the payload of catalog.message.{created, updated,
// obsoleted, reactivated}.
type MessageEvent struct {
	Message MessageSnapshot `json:"message"`
	By      string          `json:"by"`
}

// SourceRevised is the payload of catalog.message.source_revised.
type SourceRevised struct {
	Message     MessageSnapshot `json:"message"`
	OldRevision int             `json:"old_revision"`
	NewRevision int             `json:"new_revision"`
	By          string          `json:"by"`
}

// MessageRenamed is the payload of catalog.message.renamed.
type MessageRenamed struct {
	Message MessageSnapshot `json:"message"`
	OldKey  string          `json:"old_key"`
	NewKey  string          `json:"new_key"`
	By      string          `json:"by"`
}

// BranchEvent is the payload of every branch event.
type BranchEvent struct {
	BranchID   string `json:"branch_id"`
	ProjectID  string `json:"project_id"`
	Name       string `json:"name"`
	PR         *int   `json:"pr_number,omitempty"`
	HeadCommit string `json:"head_commit,omitempty"`
	State      string `json:"state"`
	Version    int    `json:"version"`
	By         string `json:"by"`
}

// BranchEventOf builds the payload for b.
func BranchEventOf(b Branch, by Author) BranchEvent {
	return BranchEvent{
		BranchID: b.ID.String(), ProjectID: b.ProjectID.String(), Name: string(b.Name), PR: b.PR,
		HeadCommit: b.HeadCommit, State: string(b.State), Version: b.Version, By: string(by),
	}
}

// BranchPushed is the payload of catalog.branch.pushed: the branch and
// what its push left it proposing. ConflictingBranches are the other
// open branches that propose one of its new keys with different source;
// their PR checks need running again too.
type BranchPushed struct {
	BranchEvent
	NewKeys             int      `json:"new_keys"`
	SourceProposals     int      `json:"source_proposals"`
	Conflicts           int      `json:"conflicts"`
	ConflictingBranches []string `json:"conflicting_branches"`
}
