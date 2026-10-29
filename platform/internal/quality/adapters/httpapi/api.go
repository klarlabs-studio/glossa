// Package httpapi is the Quality context's HTTP edge: its operations of
// the generated /v1 strict server — a project's check runs and one run
// with its counts, the findings of a run with the filters the
// dashboards, Studio and MCP's read tools need, and the waivers that
// accept a finding. The composition root embeds API next to the other
// contexts' handlers; Identity's Guard has authenticated the caller and
// resolved the tenant before any of these run.
package httpapi

import (
	"context"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// API serves Quality's operations.
type API struct{ svc *app.Service }

// New returns the API.
func New(svc *app.Service) *API { return &API{svc: svc} }

func tenantPath(ctx context.Context, sub string) string {
	t, _ := tenancy.FromContext(ctx)
	return "/v1/tenants/" + t.String() + sub
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// projectID parses a project ID; a malformed one is simply not found.
func projectID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, mapError(app.ErrProjectNotFound)
	}
	return id, nil
}

// pathID parses a resource ID in the path; a malformed one is not found.
func pathID(s string, missing error) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, mapError(missing)
	}
	return id, nil
}

// optionalRunID reads the `run` query parameter; a malformed one names
// no run, which is a 404 rather than a silent fall back to the newest.
func optionalRunID(s *string) (uuid.UUID, error) {
	if s == nil || *s == "" {
		return uuid.Nil, nil
	}
	return pathID(*s, app.ErrCheckRunNotFound)
}

// ── check runs ──────────────────────────────────────────────────────

func toCheckRun(r domain.CheckRun) apiv1.CheckRun {
	out := apiv1.CheckRun{
		Id: r.ID.String(), Ref: r.Ref, Trigger: apiv1.CheckRunTrigger(r.Trigger), PolicyVersion: r.PolicyVersion,
		Layers: make([]apiv1.FindingLayer, len(r.Layers)), CreatedBy: r.CreatedBy, StartedAt: r.StartedAt,
		Counts: apiv1.CheckRunCounts{Errors: r.Counts.Errors, Warnings: r.Counts.Warnings, Waived: r.Counts.Waived},
	}
	for i, l := range r.Layers {
		out.Layers[i] = apiv1.FindingLayer(l)
	}
	if r.Commit != "" {
		out.Commit = apiconv.Ptr(r.Commit)
	}
	if r.Conclusion != "" {
		out.Conclusion = apiconv.Ptr(apiv1.CheckRunConclusion(r.Conclusion))
	}
	if !r.CompletedAt.IsZero() {
		out.CompletedAt = apiconv.Ptr(r.CompletedAt)
	}
	return out
}

// ListCheckRuns pages a project's runs, newest first.
func (a *API) ListCheckRuns(ctx context.Context, req apiv1.ListCheckRunsRequestObject) (apiv1.ListCheckRunsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	runs, next, err := a.svc.ListCheckRuns(ctx, project, app.RunFilter{
		Ref: deref(req.Params.Branch), Commit: deref(req.Params.Commit),
		Conclusion: string(deref(req.Params.Conclusion)), Trigger: string(deref(req.Params.Trigger)),
	}, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListCheckRuns200JSONResponse{Items: make([]apiv1.CheckRun, len(runs)), NextPageToken: next}
	for i, r := range runs {
		out.Items[i] = toCheckRun(r)
	}
	return out, nil
}

// CreateCheckRun records a check that ran somewhere else — `glossa
// check` in a product's CI, the pull-request check, an API caller —
// with the findings it produced (RFC 0005 §9).
//
// The conversion is deliberately lossy in one direction only. What the
// wire carries is what a reporter can know; the fingerprint, the graded
// severity, the counts and the conclusion are all computed past this
// point, so there is no member here for a caller to put one in. That is
// the same shape `createCaptures` gives the visual probe pass, for the
// same reason: a client-minted fingerprint is not the one every other
// surface computes, and a client-declared verdict is not a verdict.
func (a *API) CreateCheckRun(
	ctx context.Context, req apiv1.CreateCheckRunRequestObject,
) (apiv1.CreateCheckRunResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := app.ReportCheckRun{
		Project: project, Ref: b.Ref, Commit: deref(b.Commit), Trigger: domain.TriggerAPI,
		Environment: deref(b.Environment), Layers: make([]domain.Layer, len(b.Layers)),
	}
	if b.Trigger != nil {
		in.Trigger = domain.Trigger(*b.Trigger)
	}
	for i, l := range b.Layers {
		in.Layers[i] = domain.Layer(l)
	}
	if b.StartedAt != nil {
		in.StartedAt = *b.StartedAt
	}
	if b.Findings != nil {
		in.Findings = make([]app.ReportedFinding, len(*b.Findings))
		for i, f := range *b.Findings {
			in.Findings[i] = reportedFinding(f)
		}
	}
	run, err := a.svc.ReportCheckRun(ctx, in)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CreateCheckRun201JSONResponse{
		Body: toCheckRun(run),
		Headers: apiv1.CreateCheckRun201ResponseHeaders{
			Location: apiconv.Ptr(tenantPath(ctx, "/projects/"+project.String()+"/check-runs/"+run.ID.String())),
		},
	}, nil
}

func reportedFinding(f apiv1.ReportedFinding) app.ReportedFinding {
	out := app.ReportedFinding{
		Layer: domain.Layer(f.Layer), Code: f.Code, Severity: domain.Severity(f.Severity),
		Locus: reportedLocus(f.Locus), Explanation: f.Message, Subject: deref(f.Subject),
		Detail: deref(f.Detail), SourceRevision: f.SourceRevision,
	}
	if f.Evidence != nil {
		out.Evidence = *f.Evidence
	}
	if f.Fix != nil {
		fix := domain.Fix{Kind: domain.FixKind(f.Fix.Kind), Term: deref(f.Fix.Term), Hint: deref(f.Fix.Hint)}
		if f.Fix.To != nil {
			fix.To = apiconv.Ptr(*f.Fix.To)
		}
		out.Fix = &fix
	}
	return out
}

func reportedLocus(l apiv1.ReportedFindingLocus) app.ReportedLocus {
	out := app.ReportedLocus{
		Key: deref(l.Key), Locale: string(deref(l.Locale)), Revision: deref(l.Revision),
		Namespace: string(deref(l.Namespace)), File: deref(l.File),
		Line: deref(l.Line), Column: deref(l.Column),
		Route: deref(l.Route), Component: deref(l.Component),
	}
	if l.Span != nil {
		out.Span = &domain.Span{Side: domain.Side(l.Span.Side), Start: l.Span.Start, End: l.Span.End}
	}
	return out
}

// GetCheckRun reads one run with the counts it concluded.
func (a *API) GetCheckRun(ctx context.Context, req apiv1.GetCheckRunRequestObject) (apiv1.GetCheckRunResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := pathID(req.CheckRun, app.ErrCheckRunNotFound)
	if err != nil {
		return nil, err
	}
	run, err := a.svc.GetCheckRun(ctx, project, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetCheckRun200JSONResponse(toCheckRun(run)), nil
}

// ── findings ────────────────────────────────────────────────────────

// toFinding renders the glossa.finding/v1 document. Its shape is the
// contract the CLI, the pull-request check and MCP's read tools share
// (RFC 0005 §2.1), so an absent member stays absent.
func toFinding(f domain.Finding) apiv1.Finding {
	out := apiv1.Finding{
		Schema: apiv1.FindingSchema(domain.Schema), Fingerprint: f.Fingerprint, Layer: apiv1.FindingLayer(f.Layer),
		Code: f.Code, Severity: apiv1.FindingSeverity(f.Severity), Message: f.Message, Locus: toLocus(f.Locus),
	}
	if f.Subject != "" {
		out.Subject = apiconv.Ptr(f.Subject)
	}
	if f.Detail != "" {
		out.Detail = apiconv.Ptr(f.Detail)
	}
	if len(f.Evidence) > 0 {
		out.Evidence = apiconv.Ptr(map[string]any(f.Evidence))
	}
	if f.Fix != nil {
		fix := apiv1.FindingFix{Kind: apiv1.FindingFixKind(f.Fix.Kind)}
		if f.Fix.To != nil {
			fix.To = apiconv.Ptr(*f.Fix.To)
		}
		if f.Fix.Term != "" {
			fix.Term = apiconv.Ptr(f.Fix.Term)
		}
		if f.Fix.Hint != "" {
			fix.Hint = apiconv.Ptr(f.Fix.Hint)
		}
		out.Fix = &fix
	}
	if f.SourceRevision != nil {
		out.SourceRevision = apiconv.Ptr(*f.SourceRevision)
	}
	if f.Waiver != "" {
		out.Waiver = apiconv.Ptr(f.Waiver)
	}
	return out
}

func toLocus(l domain.Locus) apiv1.FindingLocus {
	out := apiv1.FindingLocus{}
	set := func(dst **string, v string) {
		if v != "" {
			*dst = apiconv.Ptr(v)
		}
	}
	set(&out.Message, l.Message)
	set(&out.Key, l.Key)
	set(&out.Locale, l.Locale)
	set(&out.Revision, l.Revision)
	set(&out.Namespace, l.Namespace)
	set(&out.File, l.File)
	set(&out.Route, l.Route)
	set(&out.Component, l.Component)
	set(&out.Capture, l.Capture)
	set(&out.Region, l.Region)
	if l.Line > 0 {
		out.Line = apiconv.Ptr(l.Line)
	}
	if l.Column > 0 {
		out.Column = apiconv.Ptr(l.Column)
	}
	if l.Span != nil {
		out.Span = &apiv1.FindingSpan{
			Side: apiv1.FindingSpanSide(l.Span.Side), Start: l.Span.Start, End: l.Span.End,
		}
	}
	return out
}

// ListFindings pages one run's findings with today's waivers applied.
func (a *API) ListFindings(ctx context.Context, req apiv1.ListFindingsRequestObject) (apiv1.ListFindingsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	run, err := optionalRunID(req.Params.Run)
	if err != nil {
		return nil, err
	}
	p := req.Params
	got, err := a.svc.ListFindings(ctx, project, app.FindingQuery{
		Run: run, Ref: deref(p.Branch), Commit: deref(p.Commit),
		Filter: app.FindingFilter{
			Layer: string(deref(p.Layer)), Severity: string(deref(p.Severity)), Code: deref(p.Code),
			Locale: deref(p.Locale), Namespace: deref(p.Namespace), Key: deref(p.Message), Waived: p.Waived,
		},
	}, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListFindings200JSONResponse{
		Items:         make([]apiv1.Finding, len(got.Items)),
		NextPageToken: got.Next,
		Counts: apiv1.CheckRunCounts{
			Errors: got.Counts.Errors, Warnings: got.Counts.Warnings, Waived: got.Counts.Waived,
		},
	}
	for i, f := range got.Items {
		out.Items[i] = toFinding(f.Finding)
	}
	if got.Run != nil {
		out.Run = apiconv.Ptr(toCheckRun(*got.Run))
	}
	return out, nil
}

// ListCaptureFindings pages the visual findings on one capture, with
// today's waivers applied: what Studio outlines on the stored
// screenshot (RFC 0005 §5.2).
//
// A capture that carries no findings — and one nobody ever probed —
// reads as an empty page, because "nothing was found here" and "nothing
// looked here" are both true statements about a capture Quality holds
// no findings for, and neither is a 404. Only an ID that cannot name a
// capture is.
func (a *API) ListCaptureFindings(
	ctx context.Context, req apiv1.ListCaptureFindingsRequestObject,
) (apiv1.ListCaptureFindingsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	capture, err := pathID(req.Capture, app.ErrCaptureNotFound)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	rows, next, err := a.svc.ListCaptureFindings(ctx, project,
		app.CaptureFindingQuery{Capture: capture, Region: deref(req.Params.Region)}, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListCaptureFindings200JSONResponse{Items: make([]apiv1.Finding, len(rows)), NextPageToken: next}
	for i, f := range rows {
		out.Items[i] = toFinding(f.Finding)
	}
	return out, nil
}

// ── waivers ─────────────────────────────────────────────────────────

func toWaiver(w app.WaiverRecord) apiv1.Waiver {
	out := apiv1.Waiver{
		Id: w.ID.String(), Fingerprint: w.Fingerprint, Reason: w.Reason, Scope: apiv1.WaiverScope(w.Scope),
		SourceRevision: w.SourceRevision, Active: w.Active, CreatedBy: w.CreatedBy, CreatedAt: w.CreatedAt,
	}
	if w.Ref != "" {
		out.Ref = apiconv.Ptr(w.Ref)
	}
	if w.ExpiresAt != nil {
		out.ExpiresAt = apiconv.Ptr(*w.ExpiresAt)
	}
	if w.RevokedAt != nil {
		out.RevokedAt = apiconv.Ptr(*w.RevokedAt)
	}
	if a := w.Accepts; a != (app.FindingSummary{}) {
		accepts := apiv1.WaiverFinding{}
		if a.Layer != "" {
			accepts.Layer = apiconv.Ptr(apiv1.FindingLayer(a.Layer))
		}
		if a.Code != "" {
			accepts.Code = apiconv.Ptr(a.Code)
		}
		if a.Locale != "" {
			accepts.Locale = apiconv.Ptr(a.Locale)
		}
		if a.Key != "" {
			accepts.Key = apiconv.Ptr(a.Key)
		}
		if a.Namespace != "" {
			accepts.Namespace = apiconv.Ptr(a.Namespace)
		}
		if a.Explanation != "" {
			accepts.Message = apiconv.Ptr(a.Explanation)
		}
		out.Accepts = &accepts
	}
	return out
}

// ListWaivers pages a project's waivers, newest first.
func (a *API) ListWaivers(ctx context.Context, req apiv1.ListWaiversRequestObject) (apiv1.ListWaiversResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	p := req.Params
	ws, next, err := a.svc.ListWaivers(ctx, project, app.WaiverFilter{
		Fingerprint: deref(p.Fingerprint), Layer: string(deref(p.Layer)), Code: deref(p.Code),
		Key: deref(p.Message), Active: p.Active,
	}, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListWaivers200JSONResponse{Items: make([]apiv1.Waiver, len(ws)), NextPageToken: next}
	for i, w := range ws {
		out.Items[i] = toWaiver(w)
	}
	return out, nil
}

// CreateWaiver accepts a finding, with a reason. 200 restates the
// project's live waiver for the same finding and reach; 201 is a new
// one.
func (a *API) CreateWaiver(ctx context.Context, req apiv1.CreateWaiverRequestObject) (apiv1.CreateWaiverResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := app.CreateWaiver{
		Fingerprint: b.Fingerprint, Reason: b.Reason, Scope: domain.WaiverScope(deref(b.Scope)),
		Ref: deref(b.Ref), SourceRevision: b.SourceRevision, ExpiresAt: b.ExpiresAt,
	}
	w, created, err := a.svc.CreateWaiver(ctx, project, in)
	if err != nil {
		return nil, mapError(err)
	}
	body := toWaiver(w)
	if !created {
		return apiv1.CreateWaiver200JSONResponse(body), nil
	}
	return apiv1.CreateWaiver201JSONResponse{
		Body: body,
		Headers: apiv1.CreateWaiver201ResponseHeaders{
			Location: apiconv.Ptr(tenantPath(ctx, "/projects/"+project.String()+"/waivers/"+w.ID.String())),
		},
	}, nil
}

// RevokeWaiver takes a waiver back; the findings it accepted are
// ordinary findings again from the next read.
func (a *API) RevokeWaiver(ctx context.Context, req apiv1.RevokeWaiverRequestObject) (apiv1.RevokeWaiverResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := pathID(req.Waiver, app.ErrWaiverNotFound)
	if err != nil {
		return nil, err
	}
	if err := a.svc.RevokeWaiver(ctx, project, id); err != nil {
		return nil, mapError(err)
	}
	return apiv1.RevokeWaiver204Response{}, nil
}
