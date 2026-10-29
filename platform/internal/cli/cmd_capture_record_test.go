package cli

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa capture --check` puts its run on the record too (RFC 0005
// §9, §12.3).
//
// It is the same check `glossa check` runs with one layer more, and it
// is the run a product's CI actually gates on when it captures — so it
// is the run the pull request has to render. Recording only the plain
// check would leave the pull request showing a run with no `visual`
// layer in it while the terminal exited 1 on that very layer, which is
// exactly the disagreement M4 is decided by.

// TestCaptureCheckRecordsItsRunInCI: CI, the visual layer included, and
// the commit and branch of the captures it graded.
func TestCaptureCheckRecordsItsRunInCI(t *testing.T) {
	srv, w := capturing(t)
	inCIWorkspace(w)
	fakeProbedRun(t, []domain.Finding{probeFinding("text-clipped", "home.title")}, nil)

	var doc captureCheckJSON
	w.json(&doc, "capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "release/1.2").
		want(t, ExitOK)
	if doc.Check == nil || doc.Check.Record == nil || !doc.Check.Record.Recorded {
		t.Fatalf("record = %+v, want the capture's own run on the record", doc.Check)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.qa.recorded) != 1 {
		t.Fatalf("the server took %d runs, want the one", len(srv.qa.recorded))
	}
	body := srv.qa.recorded[0]
	// The capture document's own commit and branch, not the runner's
	// environment: the run and the captures it graded name one commit,
	// which is what lets a pull request find the run for its head SHA.
	if body["commit"] != testCommit {
		t.Errorf("commit = %v, want the captures' own %s", body["commit"], testCommit)
	}
	if body["ref"] != "release/1.2" {
		t.Errorf("ref = %v, want the captures' own branch", body["ref"])
	}
	layers, _ := body["layers"].([]any)
	var sawVisual bool
	for _, l := range layers {
		sawVisual = sawVisual || l == string(domain.LayerVisual)
	}
	if !sawVisual {
		t.Fatalf("layers = %v, want `visual` among them: it is the layer only this command can contribute",
			layers)
	}
	findings, _ := body["findings"].([]any)
	var visual int
	for _, raw := range findings {
		if f, _ := raw.(map[string]any); f["layer"] == string(domain.LayerVisual) {
			visual++
		}
	}
	if visual != 1 {
		t.Fatalf("%d visual findings were recorded, want the clipped one; without it a pull request "+
			"cannot be gated on what a browser saw", visual)
	}
}

// TestCaptureCheckDoesNotRecordOnALaptop: the same default as
// `glossa check`. A developer capturing locally would otherwise make
// their working copy the project's newest run — the one every dashboard
// reads.
func TestCaptureCheckDoesNotRecordOnALaptop(t *testing.T) {
	srv, w := capturing(t)
	fakeProbedRun(t, []domain.Finding{probeFinding("text-clipped", "home.title")}, nil)

	var doc captureCheckJSON
	w.json(&doc, "capture", "--check", "--no-coverage", "--commit", testCommit, "--branch", "main").
		want(t, ExitOK)
	if doc.Check == nil || doc.Check.Record != nil {
		t.Fatalf("record = %+v, want none outside CI", doc.Check.Record)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.qa.recorded) != 0 {
		t.Fatalf("a laptop recorded %d runs", len(srv.qa.recorded))
	}
}
