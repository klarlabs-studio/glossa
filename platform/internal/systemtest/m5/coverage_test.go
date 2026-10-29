//go:build system

package m5_test

import "testing"

// The harness's own table logic (RFC 0006 §12.2): an operation of the
// spec without a row is reported, and a required argument the fixture
// cannot fill is named. No Docker needed.
func TestMissingVerdicts(t *testing.T) {
	ops := []operation{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	rows := []sweepRow{{Operation: "a", OK: true}, {Operation: "c", Unexercised: "no id"}}
	got := missingVerdicts(ops, rows)
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("missing = %v, want [b]", got)
	}
	if got := missingVerdicts(ops, append(rows, sweepRow{Operation: "b"})); len(got) != 0 {
		t.Fatalf("missing = %v, want none", got)
	}
}

func TestUnfilledRequired(t *testing.T) {
	args := map[string]any{"project": "p"}
	if got := unfilledRequired([]any{"project", "cursor"}, args); got != "cursor" {
		t.Fatalf("unfilled = %q, want cursor", got)
	}
	if got := unfilledRequired([]any{"project"}, args); got != "" {
		t.Fatalf("unfilled = %q, want none", got)
	}
}

// Every GET operation in the contract must be found by the generator
// the sweep uses; an empty or shrunken spec read would make the table
// vacuous.
func TestSpecOperationsAreRead(t *testing.T) {
	ops, err := getOperations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) < 50 {
		t.Fatalf("read only %d GET operations from the spec", len(ops))
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if op.ID == "" || seen[op.ID] {
			t.Fatalf("operation %q has no operationId or repeats one", op.Path)
		}
		seen[op.ID] = true
	}
}
