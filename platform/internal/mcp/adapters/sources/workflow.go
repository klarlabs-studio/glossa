package sources

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
	workflowapp "go.klarlabs.de/glossa/platform/internal/workflow/app"
	workflow "go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Workflow adapts Workflow's application services to tools.Workflow
// (RFC 0006 §8). It calls the services the HTTP API calls, so project
// scope, an `assigned` member's own-work view and the permissions are
// those services' and nothing here restates them.
type Workflow struct {
	svc       *workflowapp.Service
	work      *workflowapp.WorkService
	instances *workflowapp.Instances
	catalog   workflowapp.Catalog
}

// NewWorkflow returns the adapter. instances is the same read side the
// API builds (workflowapp.NewInstances over the instance store).
func NewWorkflow(
	svc *workflowapp.Service, work *workflowapp.WorkService, instances *workflowapp.Instances, catalog workflowapp.Catalog,
) *Workflow {
	return &Workflow{svc: svc, work: work, instances: instances, catalog: catalog}
}

var _ tools.Workflow = (*Workflow)(nil)

var workflowNotFound = []error{workflowapp.ErrNotFound, workflowapp.ErrProjectNotFound}

// canonicalLocale canonicalizes an optional locale argument.
func canonicalLocale(l string) (string, error) {
	if l == "" {
		return "", nil
	}
	tags, err := workflow.CanonicalLocales([]string{l})
	if err != nil {
		return "", &app.InvalidArgumentError{Argument: "locale", Reason: "is not a BCP 47 tag"}
	}
	return tags[0], nil
}

// messageOf resolves a message key. found is false for a key the
// project does not have or the caller cannot see: it matches nothing.
func (a *Workflow) messageOf(ctx context.Context, project uuid.UUID, key string) (id uuid.UUID, found bool, err error) {
	id, err = a.catalog.MessageID(ctx, project, key)
	if err != nil {
		if nf := notFound(err, workflowNotFound...); errors.Is(nf, tools.ErrNotFound) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// Assignments implements tools.Workflow.
func (a *Workflow) Assignments(ctx context.Context, q tools.AssignmentQuery) ([]tools.Assignment, string, error) {
	pg, err := page(q.Cursor, q.Limit)
	if err != nil {
		return nil, "", err
	}
	locale, err := canonicalLocale(q.Locale)
	if err != nil {
		return nil, "", err
	}
	f := workflowapp.AssignmentFilter{Project: q.Project, Locale: locale, Limit: pg.Limit()}
	if pg.After != "" {
		if f.After, err = uuid.Parse(pg.After); err != nil {
			return nil, "", &app.InvalidArgumentError{Argument: "cursor", Reason: "not a cursor this tool issued"}
		}
	}
	if q.Key != "" {
		m, found, err := a.messageOf(ctx, q.Project, q.Key)
		if err != nil || !found {
			return nil, "", err
		}
		f.Message = m
	}
	if q.State != "" {
		f.States = []workflow.AssignmentState{workflow.AssignmentState(q.State)}
	}
	rows, err := a.work.VisibleAssignments(ctx, f, q.Mine)
	if err != nil {
		return nil, "", notFound(err, workflowNotFound...)
	}
	items, nextToken := pagination.Trim(rows, pg, func(x workflow.Assignment) string { return x.ID.String() })
	out := make([]tools.Assignment, len(items))
	for i, x := range items {
		out[i] = assignmentOf(x)
	}
	return out, next(nextToken), nil
}

func assignmentOf(a workflow.Assignment) tools.Assignment {
	out := tools.Assignment{
		ID: a.ID.String(), ProjectID: a.ProjectID.String(), Assignee: a.Assignee.String(),
		Permission: a.Permission, State: string(a.State), UnitCount: len(a.Units),
		CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339), Units: []tools.AssignmentUnit{},
	}
	if a.InstanceID != uuid.Nil {
		out.InstanceID = a.InstanceID.String()
	}
	if a.DueAt != nil {
		out.DueAt = a.DueAt.UTC().Format(time.RFC3339)
	}
	if a.ClosedAt != nil {
		out.ClosedAt = a.ClosedAt.UTC().Format(time.RFC3339)
	}
	for i, u := range a.Units {
		if i == tools.MaxAssignmentUnitsShown {
			break
		}
		out.Units = append(out.Units, tools.AssignmentUnit{MessageID: u.Message.String(), Locale: u.Locale})
	}
	return out
}

// State implements tools.Workflow.
func (a *Workflow) State(ctx context.Context, project uuid.UUID, q tools.WorkflowStateQuery) (tools.WorkflowState, error) {
	locale, err := canonicalLocale(q.Locale)
	if err != nil {
		return tools.WorkflowState{}, err
	}
	bs, err := a.svc.Bindings(ctx, project)
	if err != nil {
		return tools.WorkflowState{}, notFound(err, workflowNotFound...)
	}
	names := map[uuid.UUID]string{}
	if defs, err := a.svc.Definitions(ctx, project); err == nil {
		for _, d := range defs {
			names[d.ID] = d.Name
		}
	}
	out := tools.WorkflowState{Bindings: make([]tools.Binding, len(bs)), Instances: []tools.Instance{}}
	for i, b := range bs {
		out.Bindings[i] = tools.Binding{
			ID: b.ID.String(), Subject: string(b.Subject), Locales: b.Locales, Namespace: b.Namespace,
			DefinitionID: b.DefinitionID.String(), DefinitionName: names[b.DefinitionID],
		}
	}
	iq := workflowapp.InstanceQuery{
		InstanceFilter: workflowapp.InstanceFilter{Status: q.Status, Locale: locale, Limit: q.Limit + 1},
		MessageKey:     q.Key,
	}
	views, _, err := a.instances.List(ctx, project, iq)
	if err != nil {
		if errors.Is(err, workflowapp.ErrInvalidQuery) {
			return tools.WorkflowState{}, &app.InvalidArgumentError{Argument: "key", Reason: err.Error()}
		}
		return tools.WorkflowState{}, notFound(err, workflowNotFound...)
	}
	if len(views) > q.Limit {
		views, out.More = views[:q.Limit], true
	}
	for _, v := range views {
		in := instanceOf(v)
		if q.Key != "" {
			ts, err := a.instances.Transitions(ctx, project, v.ID)
			if err != nil {
				return tools.WorkflowState{}, notFound(err, workflowNotFound...)
			}
			for _, t := range ts {
				in.Transitions = append(in.Transitions, transitionOf(t))
			}
		}
		out.Instances = append(out.Instances, in)
	}
	return out, nil
}

func instanceOf(v workflowapp.InstanceView) tools.Instance {
	return tools.Instance{
		ID: v.ID.String(), DefinitionID: v.Definition.String(), Version: v.Version, Subject: string(v.Kind),
		SubjectID: v.SubjectID.String(), Locale: v.Locale, State: v.State, Status: v.Status,
		CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: v.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func transitionOf(t workflowapp.TransitionView) tools.Transition {
	out := tools.Transition{
		Seq: t.Seq, From: t.From, Event: t.Event, To: t.To, Outcome: t.Outcome, Actor: t.Actor,
		At: t.At.UTC().Format(time.RFC3339),
	}
	for _, g := range t.Guards {
		out.Guards = append(out.Guards, tools.GuardOutcome{Guard: g.Guard, Passed: g.Passed})
	}
	for _, x := range t.Actions {
		out.Actions = append(out.Actions, tools.ActionOutcome{Action: x.Action, Outcome: x.Outcome, Detail: x.Detail})
	}
	return out
}

// Report implements tools.Workflow.
func (a *Workflow) Report(ctx context.Context, q tools.QualityQuery) (tools.QualityReport, error) {
	rq := workflowapp.ReportQuery{Project: q.Project, Assignee: q.Assignee}
	if q.Since != "" {
		t, err := time.Parse(time.RFC3339, q.Since)
		if err != nil {
			return tools.QualityReport{}, &app.InvalidArgumentError{Argument: "since", Reason: "is an RFC 3339 time"}
		}
		rq.Since = t
	}
	r, err := a.work.QualityReport(ctx, rq)
	if err != nil {
		if errors.Is(err, workflowapp.ErrInvalidQuery) {
			return tools.QualityReport{}, &app.InvalidArgumentError{Argument: "assignee", Reason: "is vendor:<id>, member:<id>, group:<id> or role:<name>"}
		}
		return tools.QualityReport{}, notFound(err, workflowNotFound...)
	}
	out := tools.QualityReport{
		GeneratedAt: r.GeneratedAt.UTC().Format(time.RFC3339), Truncated: r.Truncated, Rows: make([]tools.QualityRow, len(r.Rows)),
	}
	if r.Since != nil {
		out.Since = r.Since.UTC().Format(time.RFC3339)
	}
	for i, x := range r.Rows {
		out.Rows[i] = tools.QualityRow{
			Assignee: x.Assignee, Locale: x.Locale, Assignments: x.Assignments, OnTime: x.OnTime, Late: x.Late,
			NoDue: x.NoDue, Units: x.Units, Unavailable: x.Unavailable, SourceWords: x.SourceWords, TMWords: x.TMWords,
			Approved: x.Approved, Rejected: x.Rejected, InReview: x.InReview, Draft: x.Draft, Unreviewed: x.Unreviewed,
			Changed: x.Changed, MeanEditDistance: x.MeanEditDistance, MeanEditRatio: x.MeanEditRatio,
			Findings: x.Findings, FindingsPerUnit: x.FindingsPerUnit, Reworked: x.Reworked, ReworkRate: x.ReworkRate,
			OnTimeRate: x.OnTimeRate,
		}
	}
	return out, nil
}
