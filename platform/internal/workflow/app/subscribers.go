package app

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The event contracts the runner reads (an anti-corruption layer: the
// published JSON, never the other contexts' Go types), and the
// vocabulary event each one is (RFC 0006 §2.4).
const (
	localizationTranslationRevised  = "localization.translation.revised"
	localizationTranslationReviewed = "localization.translation.reviewed"
	localizationTranslationOutdated = "localization.translation.outdated"
	qualityCheckRunRecorded         = "quality.check_run.recorded"
	identityTenantCreated           = "identity.tenant.created"
	// Release's release request events (RFC 0006 §5.1). Their payload
	// names the request and its project (RequestPayload).
	releaseRequestCreated   = "release.release_request.created"
	releaseRequestDeployed  = "release.release_request.deployed"
	releaseRequestRefused   = "release.release_request.refused"
	releaseRequestWithdrawn = "release.release_request.withdrawn"
	// IntelligenceSuggestionCreated is the event suggestion.created is
	// read from. NOTHING PUBLISHES IT YET: Intelligence records a
	// suggestion when its job completes but announces it on no event.
	// Its payload, when it does, is UnitPayload; until then a chart
	// reacting to suggestion.created waits, and the translation.revised
	// the suggestion's write causes (origin "ai") is what moves it.
	IntelligenceSuggestionCreated = "intelligence.suggestion.created"
)

// Subscriber names are stored with events; never rename them.
const (
	subscriberRunner = "workflow.runner"
	subscriberSeed   = "workflow.seed"
)

// UnitPayload is how Localization's translation events name their unit:
// three fields at the top level.
type UnitPayload struct {
	ProjectID string `json:"project_id"`
	MessageID string `json:"message_id"`
	Locale    string `json:"locale"`
}

// vocabulary maps the outbox event types the runner reads to the
// vocabulary events they are. Assignments' and approvals' four come
// from domain.VocabularyEvent, their owner's mapping.
var vocabulary = func() map[string]domain.EventName {
	m := map[string]domain.EventName{
		localizationTranslationRevised:  domain.EventTranslationRevised,
		localizationTranslationReviewed: domain.EventTranslationReviewed,
		localizationTranslationOutdated: domain.EventTranslationOutdated,
		IntelligenceSuggestionCreated:   domain.EventSuggestionCreated,
		qualityCheckRunRecorded:         domain.EventCheckRunRecorded,
		EventInstanceTimerDue:           domain.EventTimerDue,
		EventInstanceTimerOverdue:       domain.EventTimerOverdue,
		releaseRequestCreated:           domain.EventReleaseRequestCreated,
		releaseRequestDeployed:          domain.EventReleaseRequestDeployed,
		releaseRequestRefused:           domain.EventReleaseRequestRefused,
		releaseRequestWithdrawn:         domain.EventReleaseRequestWithdrawn,
	}
	for _, typ := range []string{
		domain.EventTypeAssignmentCompleted, domain.EventTypeAssignmentDeclined,
		domain.EventTypeApprovalGranted, domain.EventTypeApprovalDenied,
	} {
		name, _ := domain.VocabularyEvent(typ)
		m[typ] = name
	}
	return m
}()

// Subscribe registers the runner for every vocabulary event, and the
// default definition's seeding for every new tenant.
// The runner is a batch subscriber (#89): see handleDeliveries.
func (r *Runner) Subscribe(reg *outbox.Registry) error {
	runner := outbox.BatchHandlerFuncs{Event: r.handleDelivery, Batch: r.handleDeliveries}
	if err := reg.SubscribeBatch(subscriberRunner, runner, slices.Sorted(maps.Keys(vocabulary))...); err != nil {
		return err
	}
	return reg.Subscribe(identityTenantCreated, subscriberSeed, outbox.HandlerFunc(func(ctx context.Context, _ outbox.Delivery) error {
		return r.EnsureDefault(ctx)
	}))
}

func (r *Runner) handleDelivery(ctx context.Context, d outbox.Delivery) error {
	ev, err := EventOf(d)
	if err != nil {
		return err
	}
	return r.Handle(ctx, ev)
}

// EventOf reads a delivery as a vocabulary event.
func EventOf(d outbox.Delivery) (Event, error) {
	name, ok := vocabulary[d.Type]
	if !ok {
		return Event{}, outbox.Permanent(fmt.Errorf("workflow: %s is not a workflow event", d.Type))
	}
	ev := Event{ID: d.EventID, Name: name, Actor: d.Actor}
	var err error
	switch d.Type {
	case EventInstanceTimerDue, EventInstanceTimerOverdue:
		err = timerEvent(d, &ev)
	case qualityCheckRunRecorded:
		err = projectEvent(d, &ev)
	case domain.EventTypeAssignmentCompleted, domain.EventTypeAssignmentDeclined:
		err = readAssignment(d, &ev)
	case domain.EventTypeApprovalGranted, domain.EventTypeApprovalDenied:
		err = readApproval(d, &ev)
	case releaseRequestCreated, releaseRequestDeployed, releaseRequestRefused, releaseRequestWithdrawn:
		err = requestEvent(d, &ev)
	default:
		err = unitEvent(d, &ev)
	}
	if err != nil {
		if outbox.IsPermanent(err) {
			return Event{}, err
		}
		return Event{}, outbox.Permanent(fmt.Errorf("workflow: %s event %s: %w", d.Type, d.EventID, err))
	}
	return ev, nil
}

func timerEvent(d outbox.Delivery, ev *Event) error {
	var p TimerRaised
	if err := d.Decode(&p); err != nil {
		return err
	}
	id, err := uuid.Parse(p.InstanceID)
	ev.Instance, ev.TimerState = id, p.State
	return err
}

func projectEvent(d outbox.Delivery, ev *Event) error {
	var p struct {
		ProjectID string `json:"project_id"`
	}
	if err := d.Decode(&p); err != nil {
		return err
	}
	project, err := uuid.Parse(p.ProjectID)
	ev.Project, ev.Kind = project, domain.SubjectTranslation
	return err
}

// readAssignment steps the instance that asked for the assignment, or,
// for an assignment made by hand, every unit's instances.
func readAssignment(d outbox.Delivery, ev *Event) error {
	var p domain.AssignmentEvent
	if err := d.Decode(&p); err != nil {
		return err
	}
	if p.InstanceID != "" {
		id, err := uuid.Parse(p.InstanceID)
		ev.Instance = id
		return err
	}
	for _, u := range p.Units {
		s, err := unitRef(p.ProjectID, u.MessageID, u.Locale)
		if err != nil {
			return err
		}
		ev.Subjects = append(ev.Subjects, s)
	}
	return nil
}

// readApproval steps the instance that asked for the approval, or the
// subject's instances for one asked by hand.
func readApproval(d outbox.Delivery, ev *Event) error {
	var p domain.ApprovalEvent
	if err := d.Decode(&p); err != nil {
		return err
	}
	if p.InstanceID != "" {
		id, err := uuid.Parse(p.InstanceID)
		ev.Instance = id
		return err
	}
	if domain.SubjectKind(p.SubjectKind) == domain.SubjectReleaseRequest {
		project, err1 := uuid.Parse(p.ProjectID)
		id, err2 := uuid.Parse(p.SubjectID)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("release request %q in project %q", p.SubjectID, p.ProjectID)
		}
		ev.Subjects = []SubjectRef{{Kind: domain.SubjectReleaseRequest, Project: project, ID: id}}
		return nil
	}
	s, err := unitRef(p.ProjectID, p.SubjectID, p.Locale)
	ev.Subjects = []SubjectRef{s}
	return err
}

// RequestPayload is how Release's release request events name their
// subject.
type RequestPayload struct {
	RequestID string `json:"request_id"`
	ProjectID string `json:"project_id"`
}

func requestEvent(d outbox.Delivery, ev *Event) error {
	var p RequestPayload
	if err := d.Decode(&p); err != nil {
		return err
	}
	project, err := uuid.Parse(p.ProjectID)
	if err != nil {
		return fmt.Errorf("project_id: %w", err)
	}
	id, err := uuid.Parse(p.RequestID)
	if err != nil {
		return fmt.Errorf("request_id: %w", err)
	}
	ev.Subjects = []SubjectRef{{Kind: domain.SubjectReleaseRequest, Project: project, ID: id}}
	return nil
}

func unitEvent(d outbox.Delivery, ev *Event) error {
	var p UnitPayload
	if err := d.Decode(&p); err != nil {
		return err
	}
	s, err := unitRef(p.ProjectID, p.MessageID, p.Locale)
	ev.Subjects = []SubjectRef{s}
	return err
}

func unitRef(project, message, locale string) (SubjectRef, error) {
	p, err := uuid.Parse(project)
	if err != nil {
		return SubjectRef{}, fmt.Errorf("project_id: %w", err)
	}
	m, err := uuid.Parse(message)
	if err != nil {
		return SubjectRef{}, fmt.Errorf("message_id: %w", err)
	}
	tag, err := bcp47.Parse(locale)
	if err != nil {
		return SubjectRef{}, fmt.Errorf("locale: %w", err)
	}
	return SubjectRef{Kind: domain.SubjectTranslation, Project: p, ID: m, Locale: tag.String()}, nil
}
