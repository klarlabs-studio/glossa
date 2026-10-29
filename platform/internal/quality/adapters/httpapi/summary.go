package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The quality summary's edge (RFC 0005 §8).
//
// Every number is a pointer in the response, and the rendering here is
// why: a number the service could not compute is left out of the JSON
// and named in `unmeasured` with its reason, never emitted as a zero.
// The same rule holds one level down — a percentile over an empty
// sample, a pass rate over no graded check, a layer nobody ran — each
// of which is absent rather than 0, because 0 is an answer and these
// have none.

// GetQualitySummary answers the project's seven numbers.
func (a *API) GetQualitySummary(
	ctx context.Context, req apiv1.GetQualitySummaryRequestObject,
) (apiv1.GetQualitySummaryResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	s, err := a.svc.QualitySummary(ctx, project, app.SummaryQuery{
		Locale:      deref(req.Params.Locale),
		Environment: deref(req.Params.Environment),
		Since:       deref(req.Params.Since),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return summaryResponse{summary: toQualitySummary(s), maxAge: int(app.SummaryTTL / time.Second)}, nil
}

// summaryResponse is the 200 with the cache lifetime the summary itself
// promises, so a browser and the server agree on how stale the numbers
// may be without anybody having to read the documentation.
type summaryResponse struct {
	summary apiv1.QualitySummary
	maxAge  int
}

func (r summaryResponse) VisitGetQualitySummaryResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(r.maxAge))
	return apiv1.GetQualitySummary200JSONResponse(r.summary).VisitGetQualitySummaryResponse(w)
}

// toQualitySummary renders the document.
func toQualitySummary(s app.Summary) apiv1.QualitySummary {
	out := apiv1.QualitySummary{
		Schema: apiv1.QualitySummarySchema(app.SummarySchema), ProjectId: s.Project.String(),
		Environment: s.Environment, Since: s.Since, ComputedAt: s.ComputedAt, ExpiresAt: s.ExpiresAt,
		Cached:     s.Cached,
		Project:    toProjectHealth(s.Health),
		Locales:    make([]apiv1.QualityLocaleHealth, 0, len(s.Locales)),
		Unmeasured: make([]apiv1.QualityUnmeasured, 0, len(s.Unmeasured)),
	}
	if s.Locale != "" {
		out.Locale = apiconv.Ptr(s.Locale)
	}
	for _, l := range s.Locales {
		out.Locales = append(out.Locales, toLocaleHealth(l))
	}
	for _, u := range s.Unmeasured {
		out.Unmeasured = append(out.Unmeasured, apiv1.QualityUnmeasured{
			Number: apiv1.QualityUnmeasuredNumber(u.Number), Reason: u.Reason,
		})
	}
	out.FindingsByDay = toQualityTrend(s.Trend)
	return out
}

func toProjectHealth(h app.ProjectHealth) apiv1.QualityProjectHealth {
	out := apiv1.QualityProjectHealth{
		Coverage: toCoverage(h.Coverage), Findings: toCounts(h.Findings),
		Ai: toAcceptance(h.AI), Queue: toQueue(h.Queue),
		Context: toContextCoverage(h.Context), LeadTime: toPercentiles(h.LeadTime),
		Checks: toCheckHealth(h.Checks), Run: toSummaryRun(h.Run),
	}
	if len(h.ByLayer) > 0 {
		byLayer := make([]apiv1.QualityLayerCounts, 0, len(h.ByLayer))
		for _, l := range h.ByLayer {
			byLayer = append(byLayer, apiv1.QualityLayerCounts{
				Layer: apiv1.FindingLayer(l.Layer), Counts: counts(l.Counts),
			})
		}
		out.ByLayer = &byLayer
	}
	return out
}

func toLocaleHealth(l app.LocaleHealth) apiv1.QualityLocaleHealth {
	out := apiv1.QualityLocaleHealth{
		Code: l.Code, Direction: apiv1.Direction(l.Direction), IsSource: l.IsSource,
		Coverage: toCoverage(l.Coverage), Findings: toCounts(l.Findings),
		Ai: toAcceptance(l.AI), Queue: toQueue(l.Queue), LeadTime: toPercentiles(l.LeadTime),
		Layers: make([]apiv1.QualitySummaryLayer, 0, len(l.Layers)),
	}
	for _, h := range l.Layers {
		layer := apiv1.QualitySummaryLayer{
			Layer: apiv1.FindingLayer(h.Layer), Available: h.Available, Checked: h.Checked,
			Findings: toCounts(h.Findings),
		}
		if h.Unavailable != "" {
			layer.Unavailable = apiconv.Ptr(apiv1.QualitySummaryLayerUnavailable(h.Unavailable))
		}
		out.Layers = append(out.Layers, layer)
	}
	return out
}

func counts(c domain.Counts) apiv1.CheckRunCounts {
	return apiv1.CheckRunCounts{Errors: c.Errors, Warnings: c.Warnings, Waived: c.Waived}
}

func toCounts(c *domain.Counts) *apiv1.CheckRunCounts {
	if c == nil {
		return nil
	}
	return apiconv.Ptr(counts(*c))
}

// toPercentiles renders percentiles, and nothing at all where there was
// no sample to take them from.
func toPercentiles(s *domain.Spread) *apiv1.QualityPercentiles {
	if s == nil || !s.Measured() {
		return nil
	}
	return &apiv1.QualityPercentiles{Samples: s.N, P50Seconds: s.P50.Seconds(), P90Seconds: s.P90.Seconds()}
}

func toCoverage(c *app.Coverage) *apiv1.QualityCoverage {
	if c == nil {
		return nil
	}
	return &apiv1.QualityCoverage{
		Messages: c.Messages, Translated: c.Translated, Outdated: c.Outdated, Missing: c.Missing,
	}
}

func toAcceptance(a *app.Acceptance) *apiv1.QualityAcceptance {
	if a == nil {
		return nil
	}
	return &apiv1.QualityAcceptance{
		Decisions: a.Decisions, Accepted: a.Accepted, Edited: a.Edited, Rejected: a.Rejected,
		AcceptanceRate: a.AcceptanceRate, MeanEditDistance: a.MeanEditDistance,
	}
}

func toQueue(q *app.Queue) *apiv1.QualityQueue {
	if q == nil {
		return nil
	}
	return &apiv1.QualityQueue{Depth: q.Depth, Age: toPercentiles(&q.Age)}
}

func toContextCoverage(c *app.ContextCoverage) *apiv1.QualityContextCoverage {
	if c == nil {
		return nil
	}
	return &apiv1.QualityContextCoverage{
		ActiveMessages: c.ActiveMessages, WithUsage: c.WithUsage, WithRegion: c.WithRegion,
	}
}

func toCheckHealth(h *app.CheckHealth) *apiv1.QualityCheckHealth {
	if h == nil {
		return nil
	}
	out := apiv1.QualityCheckHealth{
		Runs: h.Concluded, Succeeded: h.Succeeded, Failed: h.Failed, Neutral: h.Neutral,
		Latency: toPercentiles(&h.Latency),
	}
	if rate, measured := h.PassRate(); measured {
		out.PassRate = apiconv.Ptr(rate)
	}
	if h.Latency.Measured() {
		out.MedianSeconds = apiconv.Ptr(h.Latency.P50.Seconds())
	}
	return &out
}

func toSummaryRun(r *domain.CheckRun) *apiv1.QualitySummaryRun {
	if r == nil {
		return nil
	}
	out := apiv1.QualitySummaryRun{
		Id: r.ID.String(), Ref: r.Ref, PolicyVersion: r.PolicyVersion, StartedAt: r.StartedAt,
		Layers: make([]apiv1.FindingLayer, 0, len(r.Layers)),
	}
	for _, l := range r.Layers {
		out.Layers = append(out.Layers, apiv1.FindingLayer(l))
	}
	if r.Commit != "" {
		out.Commit = apiconv.Ptr(r.Commit)
	}
	if r.Conclusion != "" {
		out.Conclusion = apiconv.Ptr(apiv1.CheckRunConclusion(r.Conclusion))
	}
	return &out
}

func toQualityTrend(t *app.TrendSummary) *apiv1.QualityTrend {
	if t == nil {
		return nil
	}
	out := apiv1.QualityTrend{
		From: openapi_types.Date{Time: t.From}, To: openapi_types.Date{Time: t.To},
		Days: make([]apiv1.QualityDayFindings, 0, len(t.Days)),
	}
	for _, d := range t.Days {
		out.Days = append(out.Days, apiv1.QualityDayFindings{
			Day: openapi_types.Date{Time: d.Day}, Layer: apiv1.FindingLayer(d.Layer), Findings: d.Findings,
		})
	}
	return &out
}
