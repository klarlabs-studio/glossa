// Package quality adapts Quality's application service to Context's
// Findings port: the visual findings a capture upload carries
// (RFC 0005 §5.1, RFC 0002 §4 — contexts reach each other through
// application ports).
//
// The translation is deliberately thin and deliberately one-way.
// Context hands over what it validated and resolved: the finding as the
// page wrote it, the message its key named, and the capture it is on.
// Quality decides everything else — the fingerprint, which only it can
// compute, the severity the project's policy gives the finding, and
// whether it is stored at all. Neither side sees the other's domain.
package quality

import (
	"context"

	contextapp "go.klarlabs.de/glossa/platform/internal/context/app"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
)

// Port implements contextapp.Findings on Quality's service.
type Port struct{ svc *qualityapp.Service }

// New returns the port.
func New(svc *qualityapp.Service) *Port { return &Port{svc: svc} }

var _ contextapp.Findings = (*Port)(nil)

// RecordFindings implements contextapp.Findings: one capture upload
// becomes one check run of the visual layer, with the findings the
// upload carried.
func (p *Port) RecordFindings(ctx context.Context, in contextapp.RecordFindings) (int, error) {
	out := qualityapp.RecordVisualFindings{
		Project: in.Project, Ref: in.Ref, Commit: in.Commit, StartedAt: in.At,
		Captures: make([]qualityapp.CaptureFindings, 0, len(in.Captures)),
	}
	for _, c := range in.Captures {
		fs := make([]qualityapp.CaptureFinding, len(c.Findings))
		for i, f := range c.Findings {
			fs[i] = qualityapp.CaptureFinding{
				Code: f.Code, Key: f.Key, Locale: f.Locale, Region: f.Region,
				Explanation: f.Message, Subject: f.Subject, Evidence: f.Evidence,
			}
			if f.MessageID != nil {
				fs[i].Message = *f.MessageID
			}
		}
		out.Captures = append(out.Captures, qualityapp.CaptureFindings{
			Capture: c.Capture, Previous: c.Previous, Route: c.Route,
			Width: c.Viewport.Width, Height: c.Viewport.Height, Locale: c.Locale, Findings: fs,
		})
	}
	rec, err := p.svc.RecordVisualFindings(ctx, out)
	return rec.Findings, err
}
