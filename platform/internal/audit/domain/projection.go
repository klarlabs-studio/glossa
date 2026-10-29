package domain

import (
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// Projection says how one outbox event type becomes an entry. The action
// is the event type itself (RFC 0006 §6.1), so what a projection adds is
// what the envelope does not say: where in the payload the project and
// the locale are, which payload paths are identifiers or selectors and
// may be kept verbatim, and which payload field named the actor before
// the envelope did.
type Projection struct {
	// Project and Locale are payload paths ("project_id",
	// "message.project_id"); "" when the event names none.
	Project string
	Locale  string
	// Selectors are the payload paths recorded verbatim (see Summarize).
	// Everything not listed is recorded as its shape. A name, an email,
	// a description, any text: never listed.
	Selectors []string
	// By is the payload path that named the actor before the envelope
	// carried one (migration 0042). For an event recorded before then,
	// the entry takes its actor from here if it is a well-formed actor,
	// and is "unknown" otherwise (RFC 0006 §6.1: the backfill does not
	// invent actors). "" where the payload's field names someone other
	// than the one who caused the event.
	By string
}

// The selector sets the event payloads share.
var (
	catalogMessage = Projection{
		Project: "message.project_id",
		Selectors: []string{
			"message.message_id", "message.project_id", "message.key", "message.namespace", "message.state",
			"old_key", "new_key", "by",
		},
		By: "by",
	}
	// A branch's name (and the names of those it conflicts with) is
	// written by people and is recorded as its shape; its id names it.
	catalogBranch = Projection{
		Project:   "project_id",
		Selectors: []string{"branch_id", "project_id", "head_commit", "state", "by"},
		By:        "by",
	}
	identityVendor = Projection{
		Selectors: []string{"vendor_id", "locales", "created_by", "changed_by", "deleted_by"},
	}
	identityGroup  = Projection{Selectors: []string{"group_id", "member_id", "by"}, By: "by"}
	identityDevice = Projection{Selectors: []string{"authorization_id", "person_id", "status", "by"}, By: "by"}
	integrationJob = Projection{
		// The payload's by is the job's requester; the event's actor is
		// whoever ended it (often the worker), so by is not taken.
		Project: "project_id",
		Selectors: []string{
			"job_id", "project_id", "kind", "format", "mode", "state", "failure_code", "reused_job_id", "summary",
			"summary.by_kind", "by",
		},
	}
	knowledgeConcept    = Projection{Project: "project_id", Selectors: []string{"concept_id", "project_id", "by"}, By: "by"}
	knowledgeStyleGuide = Projection{
		Project: "project_id", Locale: "locale",
		Selectors: []string{"style_guide_id", "project_id", "locale", "namespace", "by"}, By: "by",
	}
	localizationLocale = Projection{
		Project: "project_id", Locale: "locale",
		Selectors: []string{"project_id", "locale", "direction", "by"}, By: "by",
	}
	localizationTranslation = Projection{
		Project: "project_id", Locale: "locale",
		Selectors: []string{"translation_id", "project_id", "message_id", "locale", "state", "origin", "self_review", "by"},
		By:        "by",
	}
	releasePointer = Projection{
		Project:   "project_id",
		Selectors: []string{"release_id", "project_id", "environment", "parent_id", "previous_release_id", "manifest_digest", "by"},
		By:        "by",
	}
	// An environment's approval requirement keeps its count, its
	// four-eyes flag and a role (a built-in name); a member or a group
	// it names may be a name somebody wrote, so it is recorded as its
	// shape.
	releaseEnvironment = Projection{
		Project: "project_id",
		Selectors: []string{
			"project_id", "environment", "kind", "branch", "states", "by",
			"approval.n", "approval.from.role", "approval.distinct_from_requester",
		},
		By: "by",
	}
	// A release request (RFC 0006 §5.1) is identifiers, its state, the
	// requirement's count and who: the requester, the approvers whose
	// grants counted, and the actor. Who may approve (approval_from) can
	// name a group by its name, so it is recorded as its shape; the force
	// and withdrawal reasons are never in the payload.
	releaseRequest = Projection{
		Project: "project_id",
		Selectors: []string{
			"request_id", "project_id", "environment", "release_id", "action", "state", "requester",
			"approvals_required", "gate_met", "forced", "approvers", "by",
		},
		By: "by",
	}
	// A rollout's events carry no text: ids, the share, the state and
	// who changed it. The salt is not in them at all: it is in the
	// signed manifest, and an audit entry has no use for it.
	releaseRollout = Projection{
		Project: "project_id",
		Selectors: []string{
			"rollout_id", "project_id", "environment", "release_id", "stable_release_id", "percent", "previous_percent",
			"status", "end", "max_duration_seconds", "expires_at", "forced", "by",
		},
		By: "by",
	}
	releaseDeliveryKey = Projection{
		Project:   "project_id",
		Selectors: []string{"key_id", "project_id", "environments", "by"},
		By:        "by",
	}
)

// Projections maps every outbox event type the platform publishes to
// its projection. A type missing here would never reach the audit trail,
// so projection_registry_test.go reads every declared event type out of
// the source and fails on one this table does not name.
// Workflow's projections (RFC 0006 §2, §3). A definition's name and an
// assignment's or decision's reason are text a person wrote, so they are
// recorded as their length; the assignee and the eligible party are
// identifiers ("member:<uuid>", "role:<name>", "group:<uuid>"), kept as
// written so an entry says who the work went to. An assignment covers
// units in several locales, so it names no single locale.
var (
	workflowDefinition = Projection{
		Project: "project_id", Selectors: []string{"definition_id", "project_id", "subject"},
	}
	workflowBinding = Projection{
		Project: "project_id",
		Selectors: []string{
			"binding_id", "project_id", "definition_id", "subject", "locales", "namespace", "change",
		},
	}
	workflowAssignment = Projection{
		Project: "project_id",
		Selectors: []string{
			"assignment_id", "project_id", "instance_id", "assignee", "permission", "state", "due_at", "by",
		},
		By: "by",
	}
	workflowApproval = Projection{
		Project: "project_id", Locale: "locale",
		Selectors: []string{
			"approval_id", "project_id", "instance_id", "subject_kind", "subject_id", "locale",
			"eligible", "state", "principal", "verdict", "approvers", "by",
		},
		By: "by",
	}
	workflowTimer = Projection{Project: "project_id", Selectors: []string{"instance_id", "project_id", "state"}}
	// A rebase (RFC 0006 §2.3): which instance moved between which
	// versions of which definition, and the state it kept — a name in
	// the definition, like a timer's.
	workflowRebase = Projection{
		Project: "project_id", Locale: "locale",
		Selectors: []string{
			"instance_id", "project_id", "definition_id", "subject_kind", "locale", "from_version", "to_version", "state",
		},
	}
)

var Projections = map[string]Projection{
	"workflow.definition_saved": workflowDefinition, "workflow.definition_deleted": workflowDefinition,
	"workflow.binding_changed":    workflowBinding,
	"workflow.assignment.created": workflowAssignment, "workflow.assignment.accepted": workflowAssignment,
	"workflow.assignment.completed": workflowAssignment, "workflow.assignment.declined": workflowAssignment,
	"workflow.assignment.expired": workflowAssignment,
	"workflow.approval.requested": workflowApproval, "workflow.approval.granted": workflowApproval,
	"workflow.approval.denied":    workflowApproval,
	"workflow.instance.timer_due": workflowTimer, "workflow.instance.timer_overdue": workflowTimer,
	"workflow.instance.rebased": workflowRebase,

	"catalog.project.created": catalogProject, "catalog.project.updated": catalogProject,
	"catalog.project.deleted": catalogProject,

	"catalog.application.created": catalogApplication, "catalog.application.updated": catalogApplication,
	"catalog.application.deleted": catalogApplication,

	"catalog.message.created": catalogMessage, "catalog.message.source_revised": catalogMessage,
	"catalog.message.updated": catalogMessage, "catalog.message.renamed": catalogMessage,
	"catalog.message.obsoleted": catalogMessage, "catalog.message.reactivated": catalogMessage,
	"catalog.message.activated": catalogMessage, "catalog.message.proposed": catalogMessage,

	"catalog.branch.opened": catalogBranch, "catalog.branch.pushed": catalogBranch,
	"catalog.branch.closed": catalogBranch, "catalog.branch.reopened": catalogBranch,
	"catalog.branch.merged": catalogBranch,

	"context.build.ingested": {
		Project:   "project_id",
		Selectors: []string{"build_id", "project_id", "application_id", "commit", "branch", "source", "by"},
		By:        "by",
	},
	"context.capture.ingested": {
		Project: "project_id", Locale: "locale",
		Selectors: []string{"capture_id", "build_id", "project_id", "application_id", "route", "locale", "image_digest", "by"},
		By:        "by",
	},

	"identity.tenant.created": {Selectors: []string{"tenant_id", "kind", "slug", "created_by"}, By: "created_by"},
	// A member's email is personal data and stays out: entries hold the
	// person's id, so erasing a person never breaks a chain (§6.2).
	"identity.member.added": {
		Selectors: []string{"member_id", "person_id", "status", "roles", "locales", "projects", "vendor_id", "visibility", "added_by"},
		By:        "added_by",
	},
	"identity.member.activated": {Selectors: []string{"member_id", "person_id"}},
	"identity.member.access_changed": {
		Selectors: []string{"member_id", "roles", "locales", "changed_by"}, By: "changed_by",
	},
	"identity.member.removed": {Selectors: []string{"member_id", "person_id", "removed_by"}, By: "removed_by"},
	"identity.member.restriction_changed": {
		Selectors: []string{"member_id", "projects", "vendor_id", "visibility", "changed_by"}, By: "changed_by",
	},
	"identity.token.created":  {Selectors: []string{"token_id", "scopes", "projects", "created_by"}, By: "created_by"},
	"identity.token.revoked":  {Selectors: []string{"token_id", "revoked_by"}, By: "revoked_by"},
	"identity.vendor.created": withBy(identityVendor, "created_by"),
	"identity.vendor.changed": withBy(identityVendor, "changed_by"),
	"identity.vendor.deleted": withBy(identityVendor, "deleted_by"),
	"identity.group.created":  identityGroup, "identity.group.renamed": identityGroup,
	"identity.group.deleted": identityGroup, "identity.group.member_added": identityGroup,
	"identity.group.member_removed": identityGroup,
	// Device sign-in (RFC 0006 §7.2): the authorization, the person and
	// the outcome. The name the device gave itself is whatever it sent,
	// so it is recorded as its length.
	"identity.device_authorization.approved": identityDevice, "identity.device_authorization.denied": identityDevice,
	"identity.device_authorization.redeemed": identityDevice,

	"integration.import.completed": integrationJob, "integration.export.completed": integrationJob,

	// Audit exports are recorded in the trail they export (RFC 0006
	// §6.1). Their payloads are identifiers, the range and the outcome.
	// The request's by is its actor; the completion's actor is the
	// exporter, and its by the requester, so by is not taken there.
	EventExportRequested: withBy(auditExport, "by"),
	EventExportCompleted: auditExport,

	"knowledge.concept.created": knowledgeConcept, "knowledge.concept.updated": knowledgeConcept,
	"knowledge.concept.deleted":     knowledgeConcept,
	"knowledge.style_guide.created": knowledgeStyleGuide, "knowledge.style_guide.updated": knowledgeStyleGuide,
	"knowledge.style_guide.deleted": knowledgeStyleGuide,

	"localization.locale.added": localizationLocale, "localization.locale.removed": localizationLocale,
	"localization.fallback_graph.changed": {
		Project: "project_id", Selectors: []string{"project_id", "fallback", "fallback.*", "by"}, By: "by",
	},
	"localization.translation.revised":  localizationTranslation,
	"localization.translation.reviewed": localizationTranslation,
	"localization.translation.outdated": {
		Project: "project_id", Locale: "locale",
		Selectors: []string{"translation_id", "project_id", "message_id", "locale"},
	},

	"quality.check_run.recorded": {
		Project:   "project_id",
		Selectors: []string{"run_id", "project_id", "ref", "commit", "trigger", "conclusion"},
	},

	"release.published": releasePointer, "release.promoted": releasePointer, "release.rolled_back": releasePointer,
	"release.environment.created": releaseEnvironment, "release.environment.policy_changed": releaseEnvironment,
	"release.environment.destroyed": releaseEnvironment,
	"release.environment.publish_requested": {
		Project:   "project_id",
		Selectors: []string{"project_id", "environment", "branch", "request_id", "not_before", "by"},
		By:        "by",
	},
	"release.release_request.created": releaseRequest, "release.release_request.approved": releaseRequest,
	"release.release_request.denied": releaseRequest, "release.release_request.deployed": releaseRequest,
	"release.release_request.withdrawn": releaseRequest, "release.release_request.refused": releaseRequest,
	"release.delivery_key.created": releaseDeliveryKey, "release.delivery_key.scope_changed": releaseDeliveryKey,
	"release.delivery_key.revoked": releaseDeliveryKey,
	"release.rollout.started":      releaseRollout, "release.rollout.advanced": releaseRollout,
	"release.rollout.completed": releaseRollout, "release.rollout.aborted": releaseRollout,
}

var (
	catalogProject = Projection{
		Project: "project_id", Selectors: []string{"project_id", "slug", "source_locale", "by"}, By: "by",
	}
	catalogApplication = Projection{
		Project: "project_id", Selectors: []string{"application_id", "project_id", "slug", "platform", "by"}, By: "by",
	}
	auditExport = Projection{
		Selectors: []string{
			"job_id", "state", "from", "to", "first_sequence", "last_sequence", "entry_count", "key_id", "failure_code", "by",
		},
	}
)

func withBy(p Projection, by string) Projection {
	p.By = by
	return p
}

// ErrUnmapped is returned for an event type Projections does not name.
var ErrUnmapped = fmt.Errorf("%w: event type has no projection", ErrInvalidEntry)

// FromEvent projects one delivered outbox event into a draft. An event
// whose envelope recorded no actor takes its payload's by-field when
// that is a well-formed actor, and stays "unknown" otherwise; live
// delivery and the backfill both come through here, so the same event
// always yields the same entry.
func FromEvent(d outbox.Delivery) (Draft, error) {
	p, ok := Projections[d.Type]
	if !ok {
		return Draft{}, fmt.Errorf("%w: %s", ErrUnmapped, d.Type)
	}
	return project(d, p), nil
}

// FromRetiredEvent projects an event from history whose type no code
// publishes any more (so Projections no longer names it). It is recorded
// anyway — the backfill never drops history — with nothing verbatim: no
// project, no locale, its whole payload as its shape, and the envelope's
// actor only.
func FromRetiredEvent(d outbox.Delivery) Draft { return project(d, Projection{}) }

func project(d outbox.Delivery, p Projection) Draft {
	payload, _ := decode(d.Payload)
	obj, _ := payload.(map[string]any)
	draft := Draft{
		EventID:       d.EventID,
		Source:        SourceOutbox,
		Action:        d.Type,
		Actor:         resolveActor(d.Actor, stringAt(obj, p.By)),
		OccurredAt:    Instant(d.OccurredAt),
		AggregateType: d.AggregateType,
		AggregateID:   d.AggregateID,
		Locale:        localeAt(obj, p.Locale),
		Summary:       Summarize(d.Payload, p.Selectors),
		TraceID:       d.TraceID,
	}
	if id, err := uuid.Parse(stringAt(obj, p.Project)); err == nil && id != uuid.Nil {
		draft.Project = uuid.NullUUID{UUID: id, Valid: true}
	}
	return draft
}

// ActorFromPayload reports whether the draft's actor would come from the
// payload rather than the envelope (for the backfill's report).
func ActorFromPayload(d outbox.Delivery) bool {
	if d.Actor.Validate() == nil {
		return false
	}
	p := Projections[d.Type]
	payload, _ := decode(d.Payload)
	obj, _ := payload.(map[string]any)
	return outbox.Actor(stringAt(obj, p.By)).Validate() == nil
}

func resolveActor(envelope outbox.Actor, by string) string {
	if envelope.Validate() == nil {
		return string(envelope)
	}
	if outbox.Actor(by).Validate() == nil {
		return by
	}
	return string(outbox.ActorUnknown)
}

// stringAt returns the string at a dotted path, or "".
func stringAt(obj map[string]any, path string) string {
	if path == "" || obj == nil {
		return ""
	}
	parts := strings.Split(path, ".")
	var cur any = obj
	for _, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[part]
	}
	s, _ := cur.(string)
	return s
}

func localeAt(obj map[string]any, path string) string {
	s := stringAt(obj, path)
	if !localePattern.MatchString(s) {
		return ""
	}
	return s
}

// ProjectedTypes lists the event types Projections names, sorted.
func ProjectedTypes() []string {
	out := make([]string, 0, len(Projections))
	for t := range Projections {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}
