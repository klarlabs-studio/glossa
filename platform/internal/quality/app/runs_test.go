package app_test

import (
	"encoding/json"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// A recorded run is announced (RFC 0005 §12.3).
//
// The Glossa pull-request check renders the run CI recorded rather than
// computing a second one, so the moment a run lands is the moment a
// pull request can be told its verdict. Nothing else would tell it: a
// check run is not a branch push, a usages upload or a translation, and
// those are the only events Integration wakes a check on. Without this
// event the pull request would sit out its whole thirty-minute wait on
// a verdict the server already held.

func TestRecordCheckRunAnnouncesTheRun(t *testing.T) {
	store := recordingStore()
	svc, project := serviceFor(store)

	run, err := svc.RecordCheckRun(writeCtx(t), app.RecordRun{
		Project: project, Ref: "feature/checkout", Commit: fortyHex, Trigger: domain.TriggerCLI,
		Layers: []domain.Layer{domain.LayerParity},
		Findings: []domain.Finding{domain.New(domain.Finding{
			Layer: domain.LayerParity, Code: "missing-argument", Severity: domain.Error,
			Locus: domain.Locus{Key: "checkout.pay", Locale: "fr"}, Message: "{$amount} is gone",
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	var announced []domain.CheckRunRecorded
	for _, e := range store.published {
		if e.Type != domain.EventCheckRunRecorded {
			continue
		}
		if e.AggregateType != domain.AggregateCheckRun || e.AggregateID != run.ID.String() {
			t.Errorf("the event names aggregate %s/%s, want %s/%s",
				e.AggregateType, e.AggregateID, domain.AggregateCheckRun, run.ID)
		}
		raw, err := json.Marshal(e.Payload)
		if err != nil {
			t.Fatal(err)
		}
		var payload domain.CheckRunRecorded
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		announced = append(announced, payload)
	}
	if len(announced) != 1 {
		t.Fatalf("%d quality.check_run.recorded events, want exactly one (%+v)", len(announced), store.published)
	}
	got := announced[0]
	want := domain.CheckRunRecorded{
		RunID: run.ID.String(), ProjectID: project.String(), Ref: "feature/checkout", Commit: fortyHex,
		Trigger: string(domain.TriggerCLI), Conclusion: string(run.Conclusion),
	}
	if got != want {
		t.Errorf("announced %+v, want %+v", got, want)
	}
	// The commit is the part the pull request matches on: a branch
	// moves, and the run belongs to the commit it graded.
	if got.Commit == "" {
		t.Error("the announcement names no commit, so no pull request could find the run by its head SHA")
	}
}

// fortyHex is a full Git object name, which is the only commit a run
// may carry.
const fortyHex = "6b28d05a1f3c4e7b9d0a2c5f8e1b4d7a0c3f6e91"
