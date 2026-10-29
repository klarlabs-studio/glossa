package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Rebase (RFC 0006 §2.3): a running instance moves to a newer version of
// its definition by state name, and is refused when the state is not
// one the new version can wait in.

// rebaseV1 waits in translating, then reviewing, then is done.
const rebaseV1 = `{
  "schema": "glossa.workflow/v1", "name": "two-steps", "subject": "translation",
  "chart": { "id": "two-steps", "initial": "translating", "states": {
    "translating": { "type": "atomic", "transitions": [ { "event": "assignment.completed", "target": "reviewing" } ] },
    "reviewing": { "type": "atomic", "transitions": [ { "event": "translation.reviewed", "target": "done" } ] },
    "done": { "type": "final" } } },
  "guards": {}, "actions": {}
}`

// rebaseV2 drops translating, adds a second review, and has another
// chart id: a snapshot names the chart it was taken on.
const rebaseV2 = `{
  "schema": "glossa.workflow/v1", "name": "two-steps", "subject": "translation",
  "chart": { "id": "two-steps-v2", "initial": "reviewing", "states": {
    "reviewing": { "type": "atomic", "transitions": [ { "event": "translation.reviewed", "target": "second_review" } ] },
    "second_review": { "type": "atomic", "transitions": [ { "event": "translation.reviewed", "target": "done" } ] },
    "done": { "type": "final" } } },
  "guards": {}, "actions": {}
}`

// rebaseV3 makes reviewing final.
const rebaseV3 = `{
  "schema": "glossa.workflow/v1", "name": "two-steps", "subject": "translation",
  "chart": { "id": "two-steps", "initial": "translating", "states": {
    "translating": { "type": "atomic", "transitions": [ { "event": "assignment.completed", "target": "reviewing" } ] },
    "reviewing": { "type": "final" } } },
  "guards": {}, "actions": {}
}`

func running(state string) domain.Instance {
	return domain.Instance{ID: uuid.New(), Definition: uuid.New(), Version: 1, Kind: domain.SubjectTranslation,
		State: state, Status: domain.StatusActive}
}

func TestARebaseKeepsTheStateByName(t *testing.T) {
	v2 := compile(t, []byte(rebaseV2))
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	due := now.Add(time.Hour)
	inst := running("reviewing")
	inst.Timer = &domain.Timer{State: "reviewing", DueAt: &due}

	out, snapshot, err := inst.Rebase(v2, 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if out.Version != 2 || out.State != "reviewing" || !out.UpdatedAt.Equal(now) {
		t.Errorf("rebased = version %d, state %q, updated %v", out.Version, out.State, out.UpdatedAt)
	}
	if out.Timer == nil || !out.Timer.DueAt.Equal(due) {
		t.Errorf("the pending timer was dropped or moved: %+v", out.Timer)
	}
	// The snapshot is the new chart's: the runner restores it there.
	if state, err := v2.RestoreState(snapshot); err != nil || state != "reviewing" {
		t.Errorf("RestoreState = %q, %v", state, err)
	}
	// And the instance runs on: the next review moves it to v2's new
	// second review, not to v1's done.
	to, _, ok, err := v2.Advance("reviewing", domain.Step{Trigger: domain.Trigger{Event: domain.EventTranslationReviewed}})
	if err != nil || !ok || to != "second_review" {
		t.Errorf("Advance = %q, %v, %v", to, ok, err)
	}
}

func TestARebaseIsRefused(t *testing.T) {
	v2, v3 := compile(t, []byte(rebaseV2)), compile(t, []byte(rebaseV3))
	finished := running("done")
	finished.Status = domain.StatusFinished
	for name, c := range map[string]struct {
		inst    domain.Instance
		target  *domain.Definition
		version int
		want    error
	}{
		"a state the new version lacks": {running("translating"), v2, 2, domain.ErrRebaseStateMissing},
		"a state final in the new one":  {running("reviewing"), v3, 3, domain.ErrRebaseStateFinal},
		"the same version":              {running("reviewing"), v2, 1, domain.ErrRebaseNotNewer},
		"an older version":              {func() domain.Instance { i := running("reviewing"); i.Version = 3; return i }(), v2, 2, domain.ErrRebaseNotNewer},
		"a finished instance":           {finished, v2, 2, domain.ErrInstanceFinished},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := c.inst.Rebase(c.target, c.version, time.Now()); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestAnInstanceNotYetStartedRebasesWithoutASnapshot(t *testing.T) {
	out, snapshot, err := running(domain.NotStarted).Rebase(compile(t, []byte(rebaseV2)), 2, time.Now())
	if err != nil || snapshot != nil || out.Version != 2 || out.Started() {
		t.Fatalf("rebase = %+v, %s, %v; want version 2, not started, no snapshot", out, snapshot, err)
	}
}
