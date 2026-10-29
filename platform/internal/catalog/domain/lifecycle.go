package domain

import (
	"fmt"

	"go.klarlabs.de/statekit"
)

// branchEvent is an input to the branch lifecycle.
type branchEvent string

// Lifecycle events.
const (
	eventPush   branchEvent = "PUSH"
	eventClose  branchEvent = "CLOSE"
	eventReopen branchEvent = "REOPEN"
	eventMerge  branchEvent = "MERGE"
)

// branchLifecycle is the branch's state machine (RFC 0004 §4.2):
//
//	open   --PUSH-->   open
//	open   --CLOSE-->  closed
//	open   --MERGE-->  merged
//	closed --PUSH-->   open     (CI pushes for a reopened PR)
//	closed --REOPEN--> open
//	closed --MERGE-->  merged   (closed and merged webhooks race)
//	merged is final.
//
// Release's branch environment adds "destroyed" after merged | closed;
// that is the environment's lifecycle, not the Catalog branch's.
var branchLifecycle = mustBranchLifecycle()

func mustBranchLifecycle() *statekit.MachineConfig[struct{}] {
	open, closed, merged := statekit.StateID(BranchOpen), statekit.StateID(BranchClosed), statekit.StateID(BranchMerged)
	m, err := statekit.NewMachine[struct{}]("catalog.branch").
		WithInitial(open).
		State(open).
		On(statekit.EventType(eventPush)).Target(open).
		On(statekit.EventType(eventClose)).Target(closed).
		On(statekit.EventType(eventMerge)).Target(merged).
		Done().
		State(closed).
		On(statekit.EventType(eventPush)).Target(open).
		On(statekit.EventType(eventReopen)).Target(open).
		On(statekit.EventType(eventMerge)).Target(merged).
		Done().
		State(merged).Final().Done().
		Build()
	if err != nil {
		panic(fmt.Sprintf("catalog: branch lifecycle: %v", err))
	}
	return m
}

// nextBranchState returns the state e leads to from s, or false if the
// lifecycle doesn't allow e in s.
func nextBranchState(s BranchState, e branchEvent) (BranchState, bool) {
	interp := statekit.NewInterpreter(branchLifecycle)
	defer func() { _ = interp.Close() }()
	if err := interp.Restore(statekit.Snapshot[struct{}]{MachineID: branchLifecycle.ID, CurrentState: statekit.StateID(s)}); err != nil {
		return "", false
	}
	if !interp.SendResult(statekit.Event{Type: statekit.EventType(e)}) {
		return "", false
	}
	return BranchState(interp.State().Value), true
}
