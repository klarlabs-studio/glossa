//go:build integration

package app_test

import (
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// A capture upload's visual findings against a real database
// (RFC 0005 §5, §13 wave 4): stored as ordinary findings of one
// capture-triggered run, and read back by capture and by region.

func visualUpload(project uuid.UUID, captures ...app.CaptureFindings) app.RecordVisualFindings {
	return app.RecordVisualFindings{
		Project: project, Ref: "feat/checkout-copy", Commit: sha("beef"), Captures: captures,
	}
}

func TestRecordVisualFindingsStoresThemAgainstTheirCaptureAndRegion(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	message := uuid.Must(uuid.NewV7())
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	out, err := h.svc.RecordVisualFindings(h.developer(), visualUpload(project,
		app.CaptureFindings{Capture: first, Locale: "fr", Findings: []app.CaptureFinding{
			{
				Code: "text-clipped", Message: message, Key: "checkout.pay", Region: "r_0",
				Explanation: "Clipped: 412x20 px of text in 358x20 px.",
				Evidence:    map[string]any{"box": []any{358, 20}},
			},
			{
				Code: "region-overlap", Key: "cart.total", Region: "r_3", Subject: "checkout.pay",
				Explanation: "Overlaps checkout.pay by 41 %.",
			},
		}},
		app.CaptureFindings{Capture: second, Locale: "ar", Findings: []app.CaptureFinding{
			{Code: "rtl-not-mirrored", Key: "nav.home", Region: "r_1", Explanation: "Not mirrored: direction ltr."},
		}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if out.Findings != 3 || out.Run == uuid.Nil {
		t.Fatalf("recorded = %+v", out)
	}

	run, err := h.svc.GetCheckRun(h.developer(), project, out.Run)
	if err != nil {
		t.Fatal(err)
	}
	if run.Trigger != domain.TriggerCapture || run.Ref != "feat/checkout-copy" || run.Commit != sha("beef") {
		t.Errorf("run = %+v, want one capture-triggered run of the upload's branch and commit", run)
	}
	if len(run.Layers) != 1 || run.Layers[0] != domain.LayerVisual {
		t.Errorf("layers = %v, want the visual layer alone", run.Layers)
	}
	// Warnings, so the default policy lets the build through: promotion
	// to `error` needs two sightings and only the server can see them.
	if run.Counts != (domain.Counts{Warnings: 3}) || run.Conclusion != domain.ConclusionSuccess {
		t.Errorf("counts = %+v, conclusion = %q", run.Counts, run.Conclusion)
	}

	// By capture: the two on the first screenshot, and nothing of the
	// second's.
	items, next, err := h.svc.ListCaptureFindings(h.developer(), project,
		app.CaptureFindingQuery{Capture: first}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || next != nil {
		t.Fatalf("%d findings on the capture, next %v", len(items), next)
	}
	clipped := items[0]
	if clipped.Code != "text-clipped" || clipped.Locus.Capture != first.String() || clipped.Locus.Region != "r_0" {
		t.Errorf("finding = %+v", clipped)
	}
	// The fingerprint is the server's, over the catalog message ID, and
	// the locale is the capture's where the probe named none.
	want := domain.Fingerprint(domain.LayerVisual, "text-clipped",
		domain.Locus{Message: message.String(), Locale: "fr"}, "")
	if clipped.Fingerprint != want {
		t.Errorf("fingerprint = %s, want %s", clipped.Fingerprint, want)
	}
	if clipped.Locus.Message != message.String() || clipped.Locus.Key != "checkout.pay" || clipped.Locus.Locale != "fr" {
		t.Errorf("locus = %+v", clipped.Locus)
	}
	if clipped.Evidence == nil || clipped.Severity != domain.Warning || clipped.Schema != domain.Schema {
		t.Errorf("finding = %+v", clipped.Finding)
	}
	// A finding with no catalog message is still stored, and its
	// fingerprint falls back to the key.
	if items[1].Locus.Message != "" || items[1].Subject != "checkout.pay" {
		t.Errorf("finding = %+v", items[1].Finding)
	}

	// By region: one box of the screenshot.
	one, _, err := h.svc.ListCaptureFindings(h.developer(), project,
		app.CaptureFindingQuery{Capture: first, Region: "r_3"}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Code != "region-overlap" {
		t.Fatalf("region r_3 = %+v", one)
	}

	// A capture Quality holds no findings for is an empty list, not a
	// 404: "nothing was found here" is a true answer about a capture.
	none, _, err := h.svc.ListCaptureFindings(h.developer(), project,
		app.CaptureFindingQuery{Capture: uuid.Must(uuid.NewV7())}, firstPage())
	if err != nil || len(none) != 0 {
		t.Errorf("unknown capture = %+v, %v", none, err)
	}
}

// The two-sighting rule of RFC 0005 §5.2, sourced from the database:
// the previous capture of the same scope is the state, so a finding
// confirmed by two consecutive captures becomes evidence a policy can
// raise — and one seen once never does, however strict the policy.
func TestRecordVisualFindingsCountsTheSightingsOfTheScope(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	strict := checkpolicy.Policy{
		Rules: []checkpolicy.Rule{{
			Selector: checkpolicy.Selector{Layer: string(domain.LayerVisual)}, Severity: checkpolicy.Error,
		}},
	}
	if _, err := h.svc.SavePolicy(h.developer(), project, app.SavePolicy{Policy: strict}); err != nil {
		t.Fatal(err)
	}
	scope := func(capture, previous uuid.UUID, code string) app.RecordVisualFindings {
		return app.RecordVisualFindings{
			Project: project, Ref: "main", Commit: sha("beef"),
			Captures: []app.CaptureFindings{{
				Capture: capture, Previous: previous, Route: "/checkout", Width: 390, Height: 844, Locale: "fr",
				Findings: []app.CaptureFinding{{
					Code: code, Key: "checkout.pay", Explanation: code + " on checkout.pay",
				}},
			}},
		}
	}
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	// The first capture of the scope: one sighting, and a warning no
	// rule can raise.
	one, err := h.svc.RecordVisualFindings(h.developer(), scope(first, uuid.Nil, "text-clipped"))
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := h.svc.ListCaptureFindings(h.developer(), project,
		app.CaptureFindingQuery{Capture: first}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Sightings() != 1 || items[0].Severity != domain.Warning {
		t.Fatalf("first sighting = %+v", items)
	}
	run, err := h.svc.GetCheckRun(h.developer(), project, one.Run)
	if err != nil {
		t.Fatal(err)
	}
	if run.Conclusion != domain.ConclusionSuccess {
		t.Errorf("conclusion = %q, want success: one sighting is not evidence", run.Conclusion)
	}

	// The next capture of the same scope, naming the first as its
	// previous: the same fingerprint is now a second sighting, the
	// policy's rule reaches it, and the run fails.
	two, err := h.svc.RecordVisualFindings(h.developer(), scope(second, first, "text-clipped"))
	if err != nil {
		t.Fatal(err)
	}
	items, _, err = h.svc.ListCaptureFindings(h.developer(), project,
		app.CaptureFindingQuery{Capture: second}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Sightings() != 2 || items[0].Severity != domain.Error {
		t.Fatalf("second sighting = %+v", items)
	}
	if run, err = h.svc.GetCheckRun(h.developer(), project, two.Run); err != nil {
		t.Fatal(err)
	}
	if run.Conclusion != domain.ConclusionFailure || run.Counts != (domain.Counts{Errors: 1}) {
		t.Errorf("run = %q %+v, want a failure on the confirmed finding", run.Conclusion, run.Counts)
	}

	// A different problem on the same scope starts its own count: the
	// rule is per fingerprint, not per scope.
	third := uuid.Must(uuid.NewV7())
	if _, err := h.svc.RecordVisualFindings(h.developer(), scope(third, second, "region-overlap")); err != nil {
		t.Fatal(err)
	}
	items, _, err = h.svc.ListCaptureFindings(h.developer(), project,
		app.CaptureFindingQuery{Capture: third}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Sightings() != 1 || items[0].Severity != domain.Warning {
		t.Errorf("another finding on the same scope = %+v", items)
	}
}

// A waived visual finding is still listed on its capture, at severity
// `waived`, naming the waiver: nothing is ever hidden (RFC 0005 §2.3).
func TestListCaptureFindingsAppliesWaivers(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	capture := uuid.Must(uuid.NewV7())

	out, err := h.svc.RecordVisualFindings(h.developer(), visualUpload(project,
		app.CaptureFindings{Capture: capture, Locale: "ja", Findings: []app.CaptureFinding{{
			Code: "line-growth", Key: "checkout.pay", Explanation: "Wraps to 3 lines, up from 2.",
		}}}))
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := h.svc.ListCaptureFindings(h.developer(), project,
		app.CaptureFindingQuery{Capture: capture}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("%d findings", len(items))
	}
	waiver, _, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{
		Fingerprint: items[0].Fingerprint, Reason: "the Japanese button is two lines by design",
	})
	if err != nil {
		t.Fatal(err)
	}

	items, _, err = h.svc.ListCaptureFindings(h.developer(), project,
		app.CaptureFindingQuery{Capture: capture}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Severity != domain.Waived || items[0].Waiver != waiver.ID.String() {
		t.Errorf("finding = %+v, want it still listed as waived", items[0].Finding)
	}
	// The run's own verdict is history and does not move.
	run, err := h.svc.GetCheckRun(h.developer(), project, out.Run)
	if err != nil {
		t.Fatal(err)
	}
	if run.Counts != (domain.Counts{Warnings: 1}) {
		t.Errorf("run counts = %+v, want what the run concluded", run.Counts)
	}
}
