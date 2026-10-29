package remote

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Quality's stored findings, the waivers that accept them and the
// project's seven numbers (RFC 0005 §2.1, §2.3, §8).
//
// Everything here reads what the server already decided. `glossa
// findings`, `glossa waive` and `glossa status --quality` consume these
// calls and compute nothing of their own: a finding the CLI printed and
// a finding the dashboard shows have to be the same finding, and a
// second implementation of "which findings are open" is how they start
// to differ.

// Types the quality commands read, re-exported so they don't import the
// generated package.
type (
	CheckRun       = apiclient.CheckRun
	CheckRunCounts = apiclient.CheckRunCounts
	Waiver         = apiclient.Waiver
	QualitySummary = apiclient.QualitySummary
)

// FindingFilter is §2.1's filter set: which run, and which of its
// findings.
type FindingFilter struct {
	// Run names a check run. Empty: the newest run matching Ref and
	// Commit.
	Run string
	// Ref is a branch or an environment name; Commit a commit SHA.
	Ref, Commit string
	// Layer, Severity, Code, Locale, Namespace and Key narrow the
	// findings themselves. Key is the message key, exactly.
	Layer, Severity, Code, Locale, Namespace, Key string
	// Waived selects only the accepted findings (true) or only those no
	// waiver accepts (false). nil: both.
	Waived *bool
}

// FindingPage is what a findings query answers: the findings, the whole
// run's counts whatever the filters selected, and the run they came
// from.
type FindingPage struct {
	Findings []domain.Finding
	Counts   CheckRunCounts
	// Run is nil when the project has never been checked.
	Run *CheckRun
	// More says the server has findings this page stopped short of.
	More bool
}

// Findings reads a run's findings, with today's waivers applied. limit
// caps how many are read; 0 reads every page.
func (c *Client) Findings(ctx context.Context, s Scope, f FindingFilter, limit int) (FindingPage, error) {
	var (
		out   FindingPage
		token *string
		first = true
	)
	size := pageSize
	url := c.path("/v1/tenants/%s/projects/%s/findings", s.Tenant, s.Project)
	for {
		params := &apiclient.ListFindingsParams{
			PageSize: &size, PageToken: token,
			Run: optional(f.Run), Branch: optional(f.Ref), Commit: optional(f.Commit),
			Code: optional(f.Code), Locale: optional(f.Locale), Namespace: optional(f.Namespace),
			Message: optional(f.Key), Waived: f.Waived,
		}
		if f.Layer != "" {
			params.Layer = ptrOf(apiclient.FindingLayer(f.Layer))
		}
		if f.Severity != "" {
			params.Severity = ptrOf(apiclient.FindingSeverity(f.Severity))
		}
		r, err := c.api.ListFindingsWithResponse(ctx, s.Tenant, s.Project, params)
		if err := check(r, err, http.MethodGet, url); err != nil {
			return FindingPage{}, err
		}
		body := r.JSON200
		if first {
			out.Counts, out.Run, first = body.Counts, body.Run, false
		}
		for _, w := range body.Items {
			if limit > 0 && len(out.Findings) == limit {
				out.More = true
				return out, nil
			}
			out.Findings = append(out.Findings, FindingFromWire(w))
		}
		if body.NextPageToken == nil || *body.NextPageToken == "" {
			return out, nil
		}
		token = body.NextPageToken
	}
}

// FindingFromWire reads a glossa.finding/v1 document back into the
// domain type the CLI, the pull-request check and the server share.
//
// It carries the server's fingerprint over verbatim and never
// recomputes one. The two are the same value — the CLI and the server
// hash the same five things (domain.Fingerprint), which is what lets a
// waiver created here match a finding there — but recomputing it would
// turn that property into an assumption the code could no longer be
// wrong about, and a waiver is addressed by the fingerprint the server
// stored.
func FindingFromWire(f apiclient.Finding) domain.Finding {
	out := domain.Finding{
		Schema: string(f.Schema), Fingerprint: f.Fingerprint, Layer: domain.Layer(f.Layer),
		Code: f.Code, Severity: domain.Severity(f.Severity), Message: f.Message,
		Subject: derefOr(f.Subject), Detail: derefOr(f.Detail), Locus: locusFromWire(f.Locus),
		SourceRevision: f.SourceRevision,
	}
	if f.Evidence != nil {
		out.Evidence = map[string]any(*f.Evidence)
	}
	if f.Fix != nil {
		out.Fix = &domain.Fix{
			Kind: domain.FixKind(f.Fix.Kind), To: f.Fix.To,
			Term: derefOr(f.Fix.Term), Hint: derefOr(f.Fix.Hint),
		}
	}
	if f.Waiver != nil {
		out.Waiver = *f.Waiver
	}
	return out
}

func locusFromWire(l apiclient.FindingLocus) domain.Locus {
	out := domain.Locus{
		Message: derefOr(l.Message), Key: derefOr(l.Key), Locale: derefOr(l.Locale),
		Revision: derefOr(l.Revision), Namespace: derefOr(l.Namespace), File: derefOr(l.File),
		Line: derefOr(l.Line), Column: derefOr(l.Column), Route: derefOr(l.Route),
		Component: derefOr(l.Component), Capture: derefOr(l.Capture), Region: derefOr(l.Region),
	}
	if l.Span != nil {
		out.Span = &domain.Span{Side: domain.Side(l.Span.Side), Start: l.Span.Start, End: l.Span.End}
	}
	return out
}

// ── recording a run ─────────────────────────────────────────────────

// RecordCheckRun is a check that ran here, to put on the record: what
// was checked, which layers ran, and what they found.
//
// It carries no fingerprint, no counts and no conclusion. The
// fingerprint is the server's, computed over the catalog message the
// key resolved to — a print the CLI minted over a key would not be the
// one the waiver list and the dashboard compute for the same finding —
// and the verdict is the project's policy's. Sending either would mean
// a client deciding whether its own build is green.
type RecordCheckRun struct {
	// Ref is the branch checked and Commit the commit graded.
	Ref, Commit string
	// Trigger is "cli", "pull_request" or "api"; empty is "api".
	Trigger string
	// Environment selects the policy's block for it; empty is a branch
	// check, which is every check in CI.
	Environment string
	// Layers are the layers that actually ran, so the record can tell
	// "clean" from "not looked at".
	Layers    []domain.Layer
	Findings  []domain.Finding
	StartedAt time.Time
}

// RecordCheck records a run and its findings, and returns the run the
// server stored — with the counts and the conclusion its policy
// reached, which may not be the ones this run printed if the project's
// policy moved since the check fetched it.
func (c *Client) RecordCheck(ctx context.Context, s Scope, in RecordCheckRun) (CheckRun, error) {
	body := apiclient.CreateCheckRunJSONRequestBody{
		Ref: in.Ref, Commit: optional(in.Commit), Environment: optional(in.Environment),
		Layers: make([]apiclient.FindingLayer, len(in.Layers)),
	}
	if in.Trigger != "" {
		body.Trigger = ptrOf(apiclient.ReportedTrigger(in.Trigger))
	}
	for i, l := range in.Layers {
		body.Layers[i] = apiclient.FindingLayer(l)
	}
	if !in.StartedAt.IsZero() {
		body.StartedAt = ptrOf(in.StartedAt.UTC())
	}
	findings := make([]apiclient.ReportedFinding, 0, len(in.Findings))
	for _, f := range in.Findings {
		findings = append(findings, reportedFinding(f))
	}
	body.Findings = &findings

	url := c.path("/v1/tenants/%s/projects/%s/check-runs", s.Tenant, s.Project)
	r, err := c.api.CreateCheckRunWithResponse(ctx, s.Tenant, s.Project, body)
	if err := check(r, err, http.MethodPost, url); err != nil {
		return CheckRun{}, err
	}
	return *r.JSON201, nil
}

// reportedFinding sends what a reporter knows. The fingerprint and the
// catalog message ID are deliberately not among them: the server
// resolves the key and computes the print, so one finding keeps one
// identity across the terminal, the pull request and the dashboard.
func reportedFinding(f domain.Finding) apiclient.ReportedFinding {
	out := apiclient.ReportedFinding{
		Schema: apiclient.ReportedFindingSchema(domain.Schema),
		Layer:  apiclient.FindingLayer(f.Layer), Code: f.Code,
		Severity: apiclient.ReportedFindingSeverity(f.Severity),
		Message:  f.Message, Subject: optional(f.Subject), Detail: optional(f.Detail),
		SourceRevision: f.SourceRevision,
		Locus: apiclient.ReportedFindingLocus{
			Key: optional(f.Locus.Key), Revision: optional(f.Locus.Revision),
			File: optional(f.Locus.File), Route: optional(f.Locus.Route),
			Component: optional(f.Locus.Component),
		},
	}
	if f.Locus.Locale != "" {
		out.Locus.Locale = ptrOf(apiclient.Locale(f.Locus.Locale))
	}
	if f.Locus.Namespace != "" {
		out.Locus.Namespace = ptrOf(apiclient.Namespace(f.Locus.Namespace))
	}
	if f.Locus.Line > 0 {
		out.Locus.Line = ptrOf(f.Locus.Line)
	}
	if f.Locus.Column > 0 {
		out.Locus.Column = ptrOf(f.Locus.Column)
	}
	if f.Locus.Span != nil {
		out.Locus.Span = &apiclient.FindingSpan{
			Side: apiclient.FindingSpanSide(f.Locus.Span.Side), Start: f.Locus.Span.Start, End: f.Locus.Span.End,
		}
	}
	if len(f.Evidence) > 0 {
		out.Evidence = ptrOf(map[string]any(f.Evidence))
	}
	if f.Fix != nil {
		fix := apiclient.FindingFix{
			Kind: apiclient.FindingFixKind(f.Fix.Kind), To: f.Fix.To, Hint: optional(f.Fix.Hint),
		}
		if f.Fix.Term != "" {
			fix.Term = ptrOf(f.Fix.Term)
		}
		out.Fix = &fix
	}
	return out
}

// ── waivers ─────────────────────────────────────────────────────────

// CreateWaiver is a waiver to create. Reason is required and non-empty;
// the server refuses a blank one with a 400, because a waiver without a
// reason is a suppression nobody has to justify (RFC 0005 §2.3).
type CreateWaiver struct {
	Fingerprint string
	Reason      string
	// Scope is "project" (the default) or "branch"; Ref is the branch a
	// branch-scoped waiver is for.
	Scope, Ref string
	// SourceRevision is the revision to waive against; nil takes the one
	// the most recent stored finding with this fingerprint carries.
	SourceRevision *int
	ExpiresAt      *time.Time
}

// Waive accepts a finding. It reports whether the waiver is new: the
// server answers 200 with the project's live waiver for the same
// finding and reach, and 201 with a new one.
func (c *Client) Waive(ctx context.Context, s Scope, in CreateWaiver) (Waiver, bool, error) {
	body := apiclient.CreateWaiverJSONRequestBody{
		Fingerprint: in.Fingerprint, Reason: in.Reason,
		Ref: optional(in.Ref), SourceRevision: in.SourceRevision, ExpiresAt: in.ExpiresAt,
	}
	if in.Scope != "" {
		body.Scope = ptrOf(apiclient.WaiverScope(in.Scope))
	}
	url := c.path("/v1/tenants/%s/projects/%s/waivers", s.Tenant, s.Project)
	r, err := c.api.CreateWaiverWithResponse(ctx, s.Tenant, s.Project, body)
	if err := check(r, err, http.MethodPost, url); err != nil {
		return Waiver{}, false, err
	}
	if r.JSON201 != nil {
		return *r.JSON201, true, nil
	}
	return *r.JSON200, false, nil
}

// WaiverFilter narrows Waivers.
type WaiverFilter struct {
	Fingerprint, Layer, Code, Key string
	// Active selects only the waivers that stand now (true) or only the
	// revoked and expired ones (false). nil: both.
	Active *bool
}

// Waivers pages a project's waivers, newest first.
func (c *Client) Waivers(ctx context.Context, s Scope, f WaiverFilter, limit int) ([]Waiver, error) {
	var (
		out   []Waiver
		token *string
	)
	size := pageSize
	url := c.path("/v1/tenants/%s/projects/%s/waivers", s.Tenant, s.Project)
	for {
		params := &apiclient.ListWaiversParams{
			PageSize: &size, PageToken: token, Fingerprint: optional(f.Fingerprint),
			Code: optional(f.Code), Message: optional(f.Key), Active: f.Active,
		}
		if f.Layer != "" {
			params.Layer = ptrOf(apiclient.FindingLayer(f.Layer))
		}
		r, err := c.api.ListWaiversWithResponse(ctx, s.Tenant, s.Project, params)
		if err := check(r, err, http.MethodGet, url); err != nil {
			return nil, err
		}
		for _, w := range r.JSON200.Items {
			if limit > 0 && len(out) == limit {
				return out, nil
			}
			out = append(out, w)
		}
		if r.JSON200.NextPageToken == nil || *r.JSON200.NextPageToken == "" {
			return out, nil
		}
		token = r.JSON200.NextPageToken
	}
}

// LiveWaivers reads the waivers that stand now, as the domain type that
// applies them (domain.Waivers).
//
// It is what a locally graded run needs to reach the same counts the
// server reaches: `glossa check` computes its own findings, and a
// finding the project has accepted must come back waived there too, or
// the terminal and the pull request disagree about a decision somebody
// already made and wrote a reason for (RFC 0005 §2.3).
func (c *Client) LiveWaivers(ctx context.Context, s Scope) ([]domain.Waiver, error) {
	live := true
	ws, err := c.Waivers(ctx, s, WaiverFilter{Active: &live}, 0)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Waiver, 0, len(ws))
	for _, w := range ws {
		out = append(out, WaiverFromWire(w))
	}
	return out, nil
}

// WaiverFromWire reads a waiver back into the domain type the CLI, the
// pull-request check and the server share.
//
// Everything the matching rule reads comes over verbatim — the
// fingerprint, the scope and its branch, the source revision it was made
// against, and when it was revoked or expires. An ID that does not parse
// leaves the waiver with a nil one; it still covers what it covers, and
// the finding it waives names uuid.Nil rather than nothing.
func WaiverFromWire(w Waiver) domain.Waiver {
	out := domain.Waiver{
		Fingerprint: w.Fingerprint, Reason: w.Reason, Scope: domain.WaiverScope(w.Scope),
		Ref: derefOr(w.Ref), SourceRevision: w.SourceRevision,
		CreatedBy: w.CreatedBy, CreatedAt: w.CreatedAt,
		ExpiresAt: w.ExpiresAt, RevokedAt: w.RevokedAt,
	}
	out.ID, _ = uuid.Parse(w.Id)
	return out
}

// RevokeWaiver takes a waiver back; the findings it accepted are
// ordinary findings again from the next read.
func (c *Client) RevokeWaiver(ctx context.Context, s Scope, id string) error {
	url := c.path("/v1/tenants/%s/projects/%s/waivers/%s", s.Tenant, s.Project, id)
	r, err := c.api.RevokeWaiverWithResponse(ctx, s.Tenant, s.Project, id)
	return check(r, err, http.MethodDelete, url)
}

// ── the seven numbers ───────────────────────────────────────────────

// SummaryQuery narrows the quality summary.
type SummaryQuery struct {
	Locale, Environment string
	// Since is the start of the window for the windowed numbers; the
	// zero time takes the server's default (30 days).
	Since time.Time
}

// QualitySummaryFor reads the project's seven numbers (RFC 0005 §8).
//
// Every number is a pointer in what comes back, and that is the whole
// contract: a number the server could not compute is absent and named
// in Unmeasured with why. Nothing here fills one in.
func (c *Client) QualitySummaryFor(ctx context.Context, s Scope, q SummaryQuery) (QualitySummary, error) {
	params := &apiclient.GetQualitySummaryParams{
		Locale: optional(q.Locale), Environment: optional(q.Environment),
	}
	if !q.Since.IsZero() {
		t := q.Since
		params.Since = &t
	}
	url := c.path("/v1/tenants/%s/projects/%s/quality-summary", s.Tenant, s.Project)
	r, err := c.api.GetQualitySummaryWithResponse(ctx, s.Tenant, s.Project, params)
	if err := check(r, err, http.MethodGet, url); err != nil {
		return QualitySummary{}, err
	}
	return *r.JSON200, nil
}

// ── small helpers ───────────────────────────────────────────────────

func ptrOf[T any](v T) *T { return &v }

func derefOr[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
