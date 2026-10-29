package domain

import "time"

// Release's domain events (platform/README.md, "Domain events"). The
// Release aggregate shares the context's name, so its events are
// release.<verb>, as runtimes/SPEC.md §3 names release.published.
const (
	AggregateRelease     = "release"
	AggregateEnvironment = "environment"
	AggregateDeliveryKey = "delivery_key"

	EventPublished  = "release.published"
	EventPromoted   = "release.promoted"
	EventRolledBack = "release.rolled_back"

	EventEnvironmentCreated       = "release.environment.created"
	EventEnvironmentPolicyChanged = "release.environment.policy_changed"
	// EventEnvironmentDestroyed: a branch environment was destroyed; its
	// manifest is removed, so the edge answers 404 (RFC 0004 §4.2).
	EventEnvironmentDestroyed = "release.environment.destroyed"
	// EventPublishRequested: a branch environment is to be published
	// again once the request is due (debounced).
	EventPublishRequested = "release.environment.publish_requested"

	// Release requests (RFC 0006 §5.1): a publish or a promote into an
	// environment that requires approval. Each names its actor: the
	// requester for created, the last approver for approved, deployed
	// and refused, the denier, and whoever withdrew it.
	AggregateReleaseRequest = "release_request"
	EventRequestCreated     = "release.release_request.created"
	EventRequestApproved    = "release.release_request.approved"
	EventRequestDenied      = "release.release_request.denied"
	EventRequestDeployed    = "release.release_request.deployed"
	EventRequestWithdrawn   = "release.release_request.withdrawn"
	// EventRequestRefused: approved, and the publish gate, run again,
	// refused the deploy. No pointer moved.
	EventRequestRefused = "release.release_request.refused"

	EventDeliveryKeyCreated = "release.delivery_key.created"
	// EventDeliveryKeyScopeChanged: what the key reads changed
	// (RFC 0004 §4.3); its index object is written again.
	EventDeliveryKeyScopeChanged = "release.delivery_key.scope_changed"
	EventDeliveryKeyRevoked      = "release.delivery_key.revoked"
)

// Published is the payload of release.published.
type Published struct {
	ReleaseID      string `json:"release_id"`
	ProjectID      string `json:"project_id"`
	Version        int    `json:"version"`
	Environment    string `json:"environment"`
	ParentID       string `json:"parent_id,omitempty"`
	ManifestDigest string `json:"manifest_digest"`
	Messages       int    `json:"messages"`
	By             string `json:"by"`
}

// PointerMoved is the payload of release.promoted and release.rolled_back:
// environment now serves release_id instead of previous_release_id.
type PointerMoved struct {
	ReleaseID         string `json:"release_id"`
	ProjectID         string `json:"project_id"`
	Version           int    `json:"version"`
	Environment       string `json:"environment"`
	PreviousReleaseID string `json:"previous_release_id,omitempty"`
	By                string `json:"by"`
}

// EnvironmentChanged is the payload of the environment events.
type EnvironmentChanged struct {
	ProjectID   string `json:"project_id"`
	Environment string `json:"environment"`
	// Kind is standard or branch; Branch names a branch environment's
	// branch.
	Kind            string   `json:"kind,omitempty"`
	Branch          string   `json:"branch,omitempty"`
	States          []string `json:"states"`
	IncludeOutdated bool     `json:"include_outdated"`
	// Approval is the environment's approval requirement, absent when
	// it requires none (RFC 0006 §5.1).
	Approval *ApprovalPolicy `json:"approval,omitempty"`
	Version  int             `json:"version"`
	By       string          `json:"by"`
}

// RequestChanged is the payload of the release request events. It
// carries identifiers and the requirement, never the force reason or a
// withdrawal reason: those are text a person wrote, and readers of the
// request see them through Release.
type RequestChanged struct {
	RequestID   string `json:"request_id"`
	ProjectID   string `json:"project_id"`
	Environment string `json:"environment"`
	ReleaseID   string `json:"release_id"`
	// Action is publish or promote: what the deploy records.
	Action    string `json:"action"`
	State     string `json:"state"`
	Requester string `json:"requester"`
	// ApprovalsRequired and ApprovalFrom are the requirement the
	// request was made under ("role:reviewer").
	ApprovalsRequired int    `json:"approvals_required"`
	ApprovalFrom      string `json:"approval_from"`
	// GateMet is the publish gate's verdict at request time; Forced
	// says the requester overrode it.
	GateMet bool `json:"gate_met"`
	Forced  bool `json:"forced"`
	// Approvers are the distinct people whose grants counted, on
	// approved, deployed and refused.
	Approvers []string `json:"approvers,omitempty"`
	By        string   `json:"by"`
}

// RequestChangedOf builds the payload for r, changed by by.
func RequestChangedOf(r ReleaseRequest, approvers []string, by string) RequestChanged {
	return RequestChanged{
		RequestID: r.ID.String(), ProjectID: r.ProjectID.String(), Environment: r.Environment,
		ReleaseID: r.ReleaseID.String(), Action: string(r.Action), State: string(r.State), Requester: r.Requester,
		ApprovalsRequired: r.Approval.N, ApprovalFrom: r.Approval.From.Kind() + ":" + r.Approval.From.Ref(),
		GateMet: r.Verdict.Met, Forced: r.Override.Forced, Approvers: approvers, By: by,
	}
}

// DeliveryKeyChanged is the payload of the delivery key events. It never
// carries the key itself: public or not, keys stay out of event logs.
type DeliveryKeyChanged struct {
	KeyID        string   `json:"key_id"`
	ProjectID    string   `json:"project_id"`
	Name         string   `json:"name"`
	Environments []string `json:"environments"`
	Branches     bool     `json:"branches"`
	By           string   `json:"by"`
}

// PublishRequested is the payload of release.environment.publish_requested.
type PublishRequested struct {
	ProjectID   string    `json:"project_id"`
	Environment string    `json:"environment"`
	Branch      string    `json:"branch"`
	RequestID   string    `json:"request_id"`
	NotBefore   time.Time `json:"not_before"`
	By          string    `json:"by"`
}
