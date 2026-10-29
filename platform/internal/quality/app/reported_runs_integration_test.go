//go:build integration

package app_test

import (
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// TestHasReportedRunAgainstTheStore: the question the pull-request
// check asks on every readiness pass, against real rows. A capture run
// does not make a project one that records `glossa check` runs; a CLI
// run does, of any commit and any branch; and another project's run
// says nothing about this one.
func TestHasReportedRunAgainstTheStore(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "records")
	other := h.project(t, "other")

	has := func(p uuid.UUID) bool {
		t.Helper()
		got, err := h.svc.HasReportedRun(h.developer(), p)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	recordAs := func(p uuid.UUID, trigger domain.Trigger, commit string) {
		t.Helper()
		if _, err := h.svc.RecordCheckRun(h.developer(), app.RecordRun{
			Project: p, Ref: "feat/a", Commit: sha(commit), Trigger: trigger,
			Layers: []domain.Layer{domain.LayerParity},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if has(project) {
		t.Fatal("a project with no runs records them")
	}
	recordAs(project, domain.TriggerCapture, "beef")
	if has(project) {
		t.Fatal("a capture run made the project one whose CI runs `glossa check`")
	}
	recordAs(other, domain.TriggerCLI, "cafe")
	if has(project) {
		t.Fatal("another project's run counted for this one")
	}
	recordAs(project, domain.TriggerCLI, "abc")
	if !has(project) {
		t.Fatal("a project that recorded a `glossa check` run does not record them")
	}
}
