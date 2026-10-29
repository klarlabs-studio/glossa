package sources

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
)

// Quality adapts Quality's application service to tools.Quality. It
// reads the stored run through the context's own service, never over
// HTTP: MCP is a second façade on the ports (RFC 0005 §7.1).
type Quality struct{ quality *qualityapp.Service }

// NewQuality returns the adapter.
func NewQuality(s *qualityapp.Service) *Quality { return &Quality{quality: s} }

var _ tools.Quality = (*Quality)(nil)

var _ tools.Checks = (*Quality)(nil)

// Run implements tools.Checks: Quality's own server-side check, the
// same layers and the same policy `glossa check` and the pull-request
// check run. It records nothing, which is why a read-only session may
// call it (RFC 0005 §7.3).
func (a *Quality) Run(
	ctx context.Context, project uuid.UUID, in tools.CheckRequest,
) (tools.CheckReport, error) {
	rep, err := a.quality.RunCheck(ctx, project, qualityapp.CheckRequest{
		Environment: in.Environment, Layers: in.Layers,
	})
	if err != nil {
		if errors.Is(err, qualityapp.ErrInvalidQuery) {
			return tools.CheckReport{}, &app.InvalidArgumentError{
				Argument: "layers", Reason: "names a layer this check does not compute",
			}
		}
		return tools.CheckReport{}, notFound(err, qualityapp.ErrNotFound, qualityapp.ErrProjectNotFound)
	}
	out := tools.CheckReport{
		Conclusion: string(rep.Conclusion), PolicyVersion: rep.PolicyVersion,
		Layers: qualityapp.LayerNames(rep.Layers), Skipped: qualityapp.LayerNames(rep.Skipped),
		Messages: rep.Messages, Invalid: rep.Invalid,
		Errors: rep.Counts.Errors, Warnings: rep.Counts.Warnings,
		Findings: []tools.Finding{},
	}
	// The counts above are the whole run's; the list is a page of it, so
	// a caller that asked for twenty findings still learns there are two
	// hundred.
	found := rep.Findings
	if in.Limit > 0 && len(found) > in.Limit {
		found, out.Truncated = found[:in.Limit], true
	}
	for _, f := range found {
		out.Findings = append(out.Findings, tools.Finding{
			Fingerprint: f.Fingerprint, Layer: string(f.Layer), Code: f.Code,
			Severity: string(f.Severity), Key: f.Locus.Key, Locale: f.Locus.Locale,
			Namespace: f.Locus.Namespace, File: f.Locus.File, Line: f.Locus.Line,
			Explanation: f.Message, Subject: f.Subject,
		})
	}
	return out, nil
}

// Findings implements tools.Quality.
func (a *Quality) Findings(
	ctx context.Context, project uuid.UUID, q tools.FindingsQuery,
) (tools.CheckRun, []tools.Finding, string, error) {
	// `waived` is one-way: asking for it narrows to what a waiver
	// accepts, and not asking leaves both in. The filter's tri-state
	// `false` — only what is *not* waived — has no argument on this
	// tool, so it is never sent.
	var waived *bool
	if q.WaivedOnly {
		t := true
		waived = &t
	}
	p, err := page(q.After, q.Limit)
	if err != nil {
		return tools.CheckRun{}, nil, "", err
	}
	found, err := a.quality.ListFindings(ctx, project, qualityapp.FindingQuery{
		Ref: q.Ref,
		Filter: qualityapp.FindingFilter{
			Layer: q.Layer, Severity: q.Severity, Locale: q.Locale,
			Key: q.MessageKey, Waived: waived,
		},
	}, p)
	if err != nil {
		if errors.Is(err, qualityapp.ErrInvalidQuery) {
			return tools.CheckRun{}, nil, "", &app.InvalidArgumentError{
				Argument: "arguments", Reason: "the filter is not one Quality accepts",
			}
		}
		return tools.CheckRun{}, nil, "", notFound(err,
			qualityapp.ErrNotFound, qualityapp.ErrProjectNotFound, qualityapp.ErrCheckRunNotFound)
	}
	// Nothing checked yet is an empty page, not an error (the service
	// only 404s a run named explicitly, which this tool never does).
	if found.Run == nil {
		return tools.CheckRun{}, []tools.Finding{}, "", nil
	}
	// The counts are the page's, not the run row's: they are this run's
	// findings with today's waivers applied, which is what the items
	// beside them carry (RFC 0005 §2.3). The run's own stored verdict —
	// its conclusion, its policy version, the layers it computed — says
	// what it concluded when it ran, and stays what it was.
	run := tools.CheckRun{
		ID: found.Run.ID.String(), Ref: found.Run.Ref, Trigger: string(found.Run.Trigger),
		Conclusion: string(found.Run.Conclusion), PolicyVersion: found.Run.PolicyVersion,
		Errors: found.Counts.Errors, Warnings: found.Counts.Warnings, Waived: found.Counts.Waived,
		StartedAt: found.Run.StartedAt.UTC().Format(time.RFC3339),
	}
	for _, l := range found.Run.Layers {
		run.Layers = append(run.Layers, string(l))
	}
	if !found.Run.CompletedAt.IsZero() {
		run.CompletedAt = found.Run.CompletedAt.UTC().Format(time.RFC3339)
	}
	out := make([]tools.Finding, len(found.Items))
	for i, f := range found.Items {
		out[i] = tools.Finding{
			Fingerprint: f.Fingerprint, Layer: string(f.Layer), Code: f.Code,
			Severity: string(f.Severity), Key: f.Locus.Key, Locale: f.Locus.Locale,
			Namespace: f.Locus.Namespace, File: f.Locus.File, Line: f.Locus.Line,
			Explanation: f.Message, Subject: f.Subject, Waiver: f.Waiver,
		}
	}
	return run, out, next(found.Next), nil
}
