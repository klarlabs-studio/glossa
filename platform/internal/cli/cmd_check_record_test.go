package cli

import (
	"strings"
	"testing"
)

// `glossa check` puts its run on the record (RFC 0005 §9), so a product
// whose CI already runs the check populates Studio's quality view, the
// findings list and the summary by doing what it already does.
//
// What these tests pin is the default — CI yes, laptop no — and what
// the command sends: the findings, and none of the members the server
// computes.

// recordDoc is `glossa check --json`, as far as recording goes.
type recordDoc struct {
	Passed bool `json:"passed"`
	Record *struct {
		Recorded   bool   `json:"recorded"`
		Run        string `json:"run"`
		Conclusion string `json:"conclusion"`
		Why        string `json:"why"`
	} `json:"record"`
}

// inCIWorkspace is a checked-out branch on a CI runner, which is the
// situation the default is for.
func inCIWorkspace(w *workspace) *workspace {
	w.env["CI"] = "true"
	w.env["GLOSSA_BRANCH"] = "feat/checkout-copy"
	w.env["GLOSSA_COMMIT"] = "0123456789abcdef0123456789abcdef01234567"
	return w
}

// TestCheckRecordsItsRunInCI: the default has to serve M4's exit
// criterion without a new line in a workflow file — a product's CI
// populates the dashboard by running the command it already runs.
func TestCheckRecordsItsRunInCI(t *testing.T) {
	srv, w := pushed(t)
	inCIWorkspace(w)

	var doc recordDoc
	w.json(&doc, "check").want(t, ExitCheckFailed)

	if doc.Record == nil || !doc.Record.Recorded || doc.Record.Run == "" {
		t.Fatalf("record = %+v, want a recorded run", doc.Record)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.qa.recorded) != 1 {
		t.Fatalf("the server took %d runs, want the one", len(srv.qa.recorded))
	}
	body := srv.qa.recorded[0]
	if body["ref"] != "feat/checkout-copy" {
		t.Errorf("ref = %v, want the branch CI is on", body["ref"])
	}
	if body["commit"] != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("commit = %v, want the commit graded", body["commit"])
	}
	if body["trigger"] != "cli" {
		t.Errorf("trigger = %v, want cli: the pull-request check's verdict is its own", body["trigger"])
	}
	if layers, _ := body["layers"].([]any); len(layers) == 0 {
		t.Error("layers is empty: a recorded run has to say what it looked at")
	}
	findings, _ := body["findings"].([]any)
	if len(findings) == 0 {
		t.Fatal("no findings were sent, and this run found some")
	}
	// The whole reason the operation exists: the members only the server
	// can compute are not in the body, so nothing a client says can
	// become an identity or a verdict.
	for _, key := range []string{"conclusion", "counts", "policy_version"} {
		if _, sent := body[key]; sent {
			t.Errorf("the run sent %q; the policy reaches that, not the caller", key)
		}
	}
	for i, raw := range findings {
		f, _ := raw.(map[string]any)
		if _, sent := f["fingerprint"]; sent {
			t.Errorf("finding %d carries a fingerprint; the server computes it over the catalog message", i)
		}
		if _, sent := f["waiver"]; sent {
			t.Errorf("finding %d carries a waiver; waivers are applied server-side", i)
		}
		locus, _ := f["locus"].(map[string]any)
		for _, key := range []string{"message", "capture", "region"} {
			if _, sent := locus[key]; sent {
				t.Errorf("finding %d's locus carries %q, which only the server can fill", i, key)
			}
		}
		if f["schema"] != "glossa.finding/v1" {
			t.Errorf("finding %d: schema = %v", i, f["schema"])
		}
	}
}

// TestCheckDoesNotRecordFromALaptop: a developer running the check in a
// loop while editing a message would otherwise post a run a second,
// each one becoming the project's newest — the run every dashboard
// reads. Nothing is sent, and `--json` says nothing about a record
// nobody asked for.
func TestCheckDoesNotRecordFromALaptop(t *testing.T) {
	srv, w := pushed(t)
	w.env["GLOSSA_BRANCH"] = "feat/checkout-copy"

	var doc recordDoc
	w.json(&doc, "check").want(t, ExitCheckFailed)

	if doc.Record != nil {
		t.Errorf("record = %+v, want nothing: recording is not a local run's default", doc.Record)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.qa.recorded) != 0 {
		t.Errorf("the server took %d runs from a laptop", len(srv.qa.recorded))
	}
}

// TestCheckRecordFlagDecidesEitherWay: --record files a local run, and
// --record=false keeps CI's out of the record.
func TestCheckRecordFlagDecidesEitherWay(t *testing.T) {
	t.Run("--record from a laptop", func(t *testing.T) {
		srv, w := pushed(t)
		w.env["GLOSSA_BRANCH"] = "feat/checkout-copy"

		var doc recordDoc
		w.json(&doc, "check", "--record").want(t, ExitCheckFailed)
		if doc.Record == nil || !doc.Record.Recorded {
			t.Fatalf("record = %+v, want a recorded run", doc.Record)
		}
		srv.mu.Lock()
		defer srv.mu.Unlock()
		if len(srv.qa.recorded) != 1 {
			t.Errorf("the server took %d runs", len(srv.qa.recorded))
		}
	})
	t.Run("--record=false in CI", func(t *testing.T) {
		srv, w := pushed(t)
		inCIWorkspace(w)

		var doc recordDoc
		w.json(&doc, "check", "--record=false").want(t, ExitCheckFailed)
		if doc.Record != nil {
			t.Errorf("record = %+v, want nothing", doc.Record)
		}
		srv.mu.Lock()
		defer srv.mu.Unlock()
		if len(srv.qa.recorded) != 0 {
			t.Errorf("the server took %d runs after --record=false", len(srv.qa.recorded))
		}
	})
}

// TestCheckCannotRecordOffline: there is no server to record to, and a
// run graded against a cached policy is not the project's verdict. The
// command says so and the exit code stays the run's own.
func TestCheckCannotRecordOffline(t *testing.T) {
	srv, w := pushed(t)
	inCIWorkspace(w)

	var doc recordDoc
	w.json(&doc, "check", "--offline")
	if doc.Record == nil || doc.Record.Recorded || doc.Record.Why == "" {
		t.Fatalf("record = %+v, want a refusal that says why", doc.Record)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.qa.recorded) != 0 {
		t.Errorf("an offline run reached the server")
	}
}

// TestCheckWithoutABranchRecordsNothing: a run is of a branch. Guessing
// one would file the verdict under a ref nobody checked.
func TestCheckWithoutABranchRecordsNothing(t *testing.T) {
	srv, w := pushed(t)
	w.env["CI"] = "true"

	var doc recordDoc
	w.json(&doc, "check").want(t, ExitCheckFailed)
	if doc.Record == nil || doc.Record.Recorded || doc.Record.Why == "" {
		t.Fatalf("record = %+v, want a refusal that says why", doc.Record)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.qa.recorded) != 0 {
		t.Errorf("a run with no branch was filed")
	}
}

// TestRecordingNeverChangesTheExitCode: a check reports what it found,
// and failing to file the report is not the same as failing the check —
// nor does a successful filing turn a red run green.
func TestRecordingNeverChangesTheExitCode(t *testing.T) {
	srv, w := pushed(t)
	inCIWorkspace(w)
	srv.mu.Lock()
	srv.checkPolicy = map[string]any{
		"require_complete": "none", "fail_on": "error", "missing_translations": "warning",
	}
	srv.mu.Unlock()

	var green recordDoc
	w.json(&green, "check").want(t, ExitOK)
	if green.Record == nil || !green.Record.Recorded {
		t.Fatalf("record = %+v, want a recorded run", green.Record)
	}

	// The same run against a server that refuses the record: still
	// exit 0, and the reason is on the document.
	srv2, w2 := pushed(t)
	inCIWorkspace(w2)
	srv2.mu.Lock()
	srv2.checkPolicy = map[string]any{
		"require_complete": "none", "fail_on": "error", "missing_translations": "warning",
	}
	srv2.qa.refuse = true
	srv2.mu.Unlock()

	var refused recordDoc
	w2.json(&refused, "check").want(t, ExitOK)
	if refused.Record == nil || refused.Record.Recorded || refused.Record.Why == "" {
		t.Fatalf("record = %+v, want a refusal that says why and leaves the exit code alone", refused.Record)
	}
}

// TestInCIRecognizesTheConventions: `CI` is what every provider sets,
// and the three named after it are the ones detectBuild already reads.
func TestInCIRecognizesTheConventions(t *testing.T) {
	for env, want := range map[string]bool{
		"":               false,
		"CI=false":       false,
		"CI=0":           false,
		"CI=true":        true,
		"CI=1":           true,
		"GITHUB_ACTIONS": true,
		"GITLAB_CI":      true,
	} {
		vars := map[string]string{}
		switch env {
		case "GITHUB_ACTIONS":
			vars["GITHUB_ACTIONS"] = "true"
		case "GITLAB_CI":
			vars["GITLAB_CI"] = "true"
		case "":
		default:
			k, v, _ := strings.Cut(env, "=")
			vars[k] = v
		}
		if got := inCI(func(k string) string { return vars[k] }); got != want {
			t.Errorf("inCI(%q) = %v, want %v", env, got, want)
		}
	}
}
