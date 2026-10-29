package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The visual layer's findings arrive with a capture (RFC 0005 §5.1):
// the probe pass measures them live in the product's CI, while the page
// is open and `scrollWidth`, `getComputedStyle`, `document.fonts.check()`
// and `explain()` still exist, and uploads them with the capture. The
// server validates them and re-measures nothing — it cannot.
//
// Two members of the finding the page could not write, the ingest
// completes here, and both are the reason this is a use case rather
// than a passthrough:
//
//   - The **fingerprint** is computed by domain.Fingerprint, over the
//     catalog message ID where the key resolved to one. A browser has
//     the key and never the ID, so a fingerprint minted in the page
//     would not be the one the server computes for the same finding,
//     and every waiver against it would silently stop applying.
//   - **locus.capture** is minted here: the probe names its region as
//     `r_<index>` and the capture it is on has just been given an ID.
//     The finding schema pairs the two (`region` is only valid beside a
//     `capture`), and only the server can pair them.
//
// Everything else is what the page wrote.

// CaptureFinding is one visual finding of one capture, as Context hands
// it over: the probe's, with the key already resolved.
type CaptureFinding struct {
	// Code is the rule the probe applied.
	Code string
	// Message is the catalog message the finding is about; uuid.Nil
	// where the catalog didn't know the key, and the fingerprint then
	// falls back to the key, exactly as an offline check's does.
	Message uuid.UUID
	// Key is the message key.
	Key string
	// Locale is the locale the finding is about; empty for the capture's.
	Locale string
	// Region is `r_<index into the capture's regions>`, or empty.
	Region string
	// Explanation is the sentence for a person.
	Explanation string
	// Subject is what the finding names: the message it overlaps, the
	// screen's locale. It is part of the fingerprint, because two
	// overlaps of one message are two findings.
	Subject string
	// Evidence is what the probe measured.
	Evidence map[string]any
}

// CaptureFindings are one capture's findings, in the scope the
// two-sighting rule counts in.
type CaptureFindings struct {
	// Capture is the capture's ID, which the ingest has just minted.
	Capture uuid.UUID
	// Previous is the capture the same scope showed last, or uuid.Nil
	// where it showed none. Its stored findings are the previous
	// sighting (RFC 0005 §5.2).
	Previous uuid.UUID
	// Route, Width, Height and Locale are the scope: one capture is one
	// (route, viewport, locale), and the locale is also the locale of
	// every finding on it that names no other.
	Route         string
	Width, Height int
	Locale        string
	Findings      []CaptureFinding
}

// RecordVisualFindings is one capture upload's findings to record.
type RecordVisualFindings struct {
	Project uuid.UUID
	// Ref is the branch the upload was of, and Commit the commit it
	// captured.
	Ref      string
	Commit   string
	Captures []CaptureFindings
	// StartedAt is when the capture session ran, where the caller knows.
	StartedAt time.Time
}

// Findings counts what the upload carries.
func (in RecordVisualFindings) Findings() int {
	n := 0
	for _, c := range in.Captures {
		n += len(c.Findings)
	}
	return n
}

// VisualFindingsRecorded is what RecordVisualFindings stored.
type VisualFindingsRecorded struct {
	// Run is the check run the findings were recorded in; uuid.Nil when
	// the policy switched the visual layer off and nothing was stored.
	Run uuid.UUID
	// Findings counts what was stored: fewer than were handed in when a
	// policy rule switched some of them off.
	Findings int
}

// RecordVisualFindings stores a capture upload's probe findings as one
// check run of the visual layer (RFC 0005 §5, §13 wave 4).
//
// The project's policy is the evaluator here as everywhere else: a
// project that switched the visual layer off stores nothing and pays
// for nothing, and a rule that grades `visual` differently in a locale
// or a namespace grades these the same way it grades a check's. What
// the page may never do is grade itself — Context refuses any severity
// but `warning` on the way in, and promotion to `error` needs the same
// fingerprint in two consecutive captures, which only the server can
// see.
func (s *Service) RecordVisualFindings(
	ctx context.Context, in RecordVisualFindings,
) (out VisualFindingsRecorded, err error) {
	// Context calls it inside its own authorized capture upload; the
	// project is checked again here so the port can't be the way round
	// a project scope (RFC 0006 §4.1).
	if err := authz.InProject(ctx, in.Project); err != nil {
		return VisualFindingsRecorded{}, err
	}
	if n := in.Findings(); n > MaxRunFindings {
		return VisualFindingsRecorded{}, fmt.Errorf("%w: %d findings, at most %d", ErrTooManyFindings, n, MaxRunFindings)
	}
	ctx, end := s.span(ctx, "quality.record_visual_findings",
		attribute.String("glossa.project_id", in.Project.String()),
		attribute.Int("glossa.quality.captures", len(in.Captures)),
		attribute.Int("glossa.quality.findings", in.Findings()))
	defer end(&err)

	stored, err := s.storedPolicy(ctx, in.Project)
	if err != nil {
		return VisualFindingsRecorded{}, err
	}
	// `off` means the project does not compute the layer, so an upload
	// carrying findings of a layer nobody asked for stores nothing —
	// that is what keeps a project from paying for a layer it ignores
	// (RFC 0005 §4.1).
	if !domain.Computes(stored.Policy, "", domain.LayerVisual) {
		s.dropProbes(in)
		return VisualFindingsRecorded{}, nil
	}
	counted, err := s.count(ctx, in, stored.Policy.Visual())
	if err != nil {
		return VisualFindingsRecorded{}, err
	}
	ev := domain.Evaluate(stored.Policy, "", counted)
	graded := ev.Findings()
	s.countProbes(counted, graded)
	if len(graded) == 0 {
		return VisualFindingsRecorded{}, nil
	}
	run, err := s.RecordCheckRun(ctx, RecordRun{
		Project: in.Project, Ref: in.Ref, Commit: in.Commit, Trigger: domain.TriggerCapture,
		PolicyVersion: ev.PolicyVersion, Layers: []domain.Layer{domain.LayerVisual}, Policy: stored.Policy,
		Findings: graded, StartedAt: in.StartedAt,
	})
	if err != nil {
		return VisualFindingsRecorded{}, err
	}
	return VisualFindingsRecorded{Run: run.ID, Findings: len(graded)}, nil
}

// countProbes records `glossa_quality_visual_probes_total{code,outcome}`
// for one upload (RFC 0005 §11): what the probe pass found, and what
// the server made of it.
//
// The outcome is the one question a person has about the visual layer:
// is this being believed? `first_sighting` is a warning the
// two-sighting rule has not confirmed, `confirmed` is one it has — the
// state that lets a policy rule reach it — and `dropped` is a finding a
// policy rule switched off, which is what a project that pays no
// attention to a code looks like from here.
func (s *Service) countProbes(counted, graded []domain.Finding) {
	kept := make(map[string]bool, len(graded))
	for _, f := range graded {
		kept[f.Fingerprint] = true
	}
	for _, f := range counted {
		switch {
		case !kept[f.Fingerprint]:
			s.metrics.VisualProbeRecorded(f.Code, ProbeDropped)
		case f.Sightings() > 1:
			s.metrics.VisualProbeRecorded(f.Code, ProbeConfirmed)
		default:
			s.metrics.VisualProbeRecorded(f.Code, ProbeFirstSighting)
		}
	}
}

// dropProbes records an upload to a project that switched the visual
// layer off. Nothing was sealed, so the codes are the page's own — the
// only place in this file they are taken at face value, and harmless
// here because the metric's codes are allowlisted.
func (s *Service) dropProbes(in RecordVisualFindings) {
	for _, c := range in.Captures {
		for _, f := range c.Findings {
			s.metrics.VisualProbeRecorded(f.Code, ProbeDropped)
		}
	}
}

// count seals every capture's findings and applies the two-sighting
// rule of RFC 0005 §5.2 to them, against what the previous capture of
// the same scope found.
//
// It is `layers.PromoteVisual` — the same function `glossa capture
// --check` runs — called once per capture, so the rule has exactly one
// implementation and the two surfaces cannot drift. Per capture and not
// per upload, because the store keeps a row per capture: two captures
// that show the same problem are two places to outline it, and
// PromoteVisual over a whole upload would report it once and drop the
// second capture's locus. The rule is unaffected: it counts within a
// scope, and one capture is one scope.
//
// The previous sighting is read from the findings already stored
// against the previous capture — the record is the data, so a CI runner
// with an empty workspace counts as well as one that kept its
// `.glossa/`. A scope with no previous capture, or one whose findings
// cannot be read, counts one sighting: never promote on a guess.
func (s *Service) count(
	ctx context.Context, in RecordVisualFindings, thresholds checkpolicy.VisualThresholds,
) ([]domain.Finding, error) {
	out := make([]domain.Finding, 0, in.Findings())
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		for _, c := range in.Captures {
			scope := layers.VisualScope{Route: c.Route, Width: c.Width, Height: c.Height, Locale: c.Locale}
			seen := layers.Seen{}
			if c.Previous != uuid.Nil {
				before, err := st.CaptureFingerprints(ctx, in.Project, c.Previous)
				if err != nil {
					return err
				}
				seen[scope.Key()] = before
			}
			visual, _ := layers.PromoteVisual(seen,
				[]layers.Probed{{Scope: scope, Findings: c.findings()}}, thresholds)
			out = append(out, visual.Findings...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// findings is one capture's probe findings as domain findings, with the
// capture minted into every locus. PromoteVisual seals the rest: the
// route and the locale come from the scope, and the fingerprint is
// computed there — over the catalog message ID where the key resolved
// to one, which is the identity only a caller that knows the catalog
// can compute.
func (c CaptureFindings) findings() []domain.Finding {
	out := make([]domain.Finding, 0, len(c.Findings))
	for _, f := range c.Findings {
		locus := domain.Locus{Key: f.Key, Locale: f.Locale, Capture: c.Capture.String(), Region: f.Region}
		if f.Message != uuid.Nil {
			locus.Message = f.Message.String()
		}
		out = append(out, domain.Finding{
			Layer: domain.LayerVisual, Code: f.Code, Severity: domain.Warning, Locus: locus,
			Message: f.Explanation, Subject: f.Subject, Evidence: f.Evidence,
		})
	}
	return out
}

// CaptureFindingQuery narrows the findings read off one capture.
type CaptureFindingQuery struct {
	// Capture is the capture to read.
	Capture uuid.UUID
	// Region is one of its regions (`r_0`); empty reads all of them.
	Region string
}

// ListCaptureFindings pages the findings on one capture, with today's
// waivers applied (RFC 0005 §13 wave 4): what Studio outlines on the
// stored screenshot, and what `glossa capture --check` reads back.
//
// It reads the capture's own findings across runs, not the project's
// newest run, because the run that saw this screenshot is the one that
// ingested it — the project's newest run is usually a later check of
// the catalog, which never saw it.
func (s *Service) ListCaptureFindings(
	ctx context.Context, project uuid.UUID, q CaptureFindingQuery, page pagination.Page,
) ([]FindingRecord, *string, error) {
	if err := s.read(ctx, project); err != nil {
		return nil, nil, err
	}
	if q.Capture == uuid.Nil {
		return nil, nil, ErrCaptureNotFound
	}
	now := s.now()
	var rows []FindingRecord
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.ListCaptureFindings(ctx, project, q.Capture, q.Region, page.After, page.Limit(), now)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(f FindingRecord) string { return f.SortKey })
	if items == nil {
		items = []FindingRecord{}
	}
	return items, next, nil
}
