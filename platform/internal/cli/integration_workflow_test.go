//go:build integration

package cli_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli"
	"go.klarlabs.de/glossa/platform/internal/cli/credentials"
)

// cliReview is a definition as a repository would keep it: when a
// translation is revised, the instance finishes at once.
const cliReview = `{
  "schema": "glossa.workflow/v1",
  "name": "cli-review",
  "subject": "translation",
  "chart": {
    "id": "cli-review", "initial": "current",
    "states": {
      "current": {"id": "current", "type": "atomic", "transitions": [
        {"event": "translation.revised", "target": "done"},
        {"event": "translation.outdated", "target": "done", "actions": ["back_to_review"]}
      ]},
      "done": {"id": "done", "type": "final"}
    }
  },
  "guards": {},
  "actions": {"back_to_review": {"use": "set_review_state", "state": "needs_review"}}
}
`

// TestWorkflowAndAssignmentsAgainstGlossaServer is `glossa workflow`
// and `glossa assignments` against the real server (RFC 0006 §2–§3,
// §8): lint → push (create, unchanged, next version, a stale version
// refused) → bind → a translation revision → the instance it started →
// its transition log. A token without the opt-in workflows scope is
// refused with a message that says so. Assignments are a person's
// work: the CLI's token sees no work of its own, cannot create any, and
// cannot act on a person's — each refusal explained.
func TestWorkflowAndAssignmentsAgainstGlossaServer(t *testing.T) {
	s := startServer(t, nil)
	cookie, csrf := s.signIn("ada@example.com")
	owner := session{cookie: cookie, csrf: csrf}
	var org struct{ ID string }
	s.do(owner.call("POST", "/v1/tenants", map[string]string{"slug": "klarlabs", "name": "Klarlabs"}), http.StatusCreated, &org)
	base := "/v1/tenants/" + org.ID
	var project struct{ ID string }
	s.do(owner.call("POST", base+"/projects", map[string]any{"slug": "brotwerk", "name": "Brotwerk", "source_locale": "en",
		"settings": map[string]any{"default_syntax": "mf1", "review_required": false}}), http.StatusCreated, &project)
	s.do(owner.call("POST", base+"/projects/"+project.ID+"/locales", map[string]string{"code": "de"}), http.StatusCreated, nil)
	token := func(name string, scopes ...string) string {
		var tok struct{ Secret string }
		s.do(owner.call("POST", base+"/tokens", map[string]any{"name": name, "scopes": scopes}), http.StatusCreated, &tok)
		return tok.Secret
	}
	ciToken, workflowsToken := token("ci", "write"), token("workflows", "write", "workflows")

	dir := t.TempDir()
	store := &credentials.File{Path: filepath.Join(t.TempDir(), "credentials.json")}
	r := runner{t: t, dir: dir, env: map[string]string{"GLOSSA_TOKEN": workflowsToken}, store: store}
	ci := runner{t: t, dir: dir, env: map[string]string{"GLOSSA_TOKEN": ciToken}, store: store}
	r.run(cli.ExitOK, nil, "init", "--server", s.base, "--project", "brotwerk")
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("locales/en.json", `{"home.title": "Welcome", "checkout.pay": "Pay {amount, number}"}`)
	r.run(cli.ExitOK, nil, "push")

	// lint: the server's compile and lint, nothing stored.
	write("workflows/cli-review.json", cliReview)
	var lint struct {
		Valid    bool
		Findings []struct{ Rule, Severity, Path, Message string }
	}
	r.run(cli.ExitOK, &lint, "workflow", "lint", "workflows/cli-review.json")
	if !lint.Valid {
		t.Fatalf("lint = %+v", lint)
	}
	write("workflows/bad.json", strings.Replace(cliReview, `"use": "set_review_state"`, `"use": "legal_signed_off"`, 1))
	r.run(cli.ExitCheckFailed, &lint, "workflow", "lint", "workflows/bad.json")
	if lint.Valid || len(lint.Findings) == 0 || lint.Findings[0].Severity != "error" || lint.Findings[0].Rule == "" ||
		lint.Findings[0].Message == "" {
		t.Fatalf("lint bad = %+v", lint)
	}

	// Without the workflows scope a token reads but cannot save.
	var refused struct {
		Error struct{ Code, Why, Fix string }
	}
	ci.run(cli.ExitNetwork, &refused, "workflow", "push", "workflows/cli-review.json")
	if refused.Error.Code != "workflows_scope_required" || !strings.Contains(refused.Error.Why, "`workflows` scope") {
		t.Fatalf("push without the scope = %+v", refused)
	}
	ci.run(cli.ExitOK, nil, "workflow", "list")

	type definition struct {
		ID      string
		Name    string
		Version int
	}
	var push struct {
		Result     string
		Definition definition
	}
	r.run(cli.ExitOK, &push, "workflow", "push", "workflows/cli-review.json")
	if push.Result != "created" || push.Definition.Version != 1 || push.Definition.Name != "cli-review" {
		t.Fatalf("push = %+v", push)
	}
	r.run(cli.ExitOK, &push, "workflow", "push", "workflows/cli-review.json")
	if push.Result != "unchanged" || push.Definition.Version != 1 {
		t.Fatalf("push again = %+v", push)
	}
	// pull → push round-trips as unchanged; an edit is version 2.
	r.run(cli.ExitOK, nil, "workflow", "pull", "cli-review", "-o", "workflows/pulled.yaml")
	r.run(cli.ExitOK, &push, "workflow", "push", "workflows/pulled.yaml")
	if push.Result != "unchanged" {
		t.Fatalf("pulled push = %+v", push)
	}
	edited := strings.Replace(cliReview, `"state": "needs_review"`, `"state": "draft"`, 1)
	write("workflows/cli-review.json", edited)
	r.run(cli.ExitOK, &push, "workflow", "push", "workflows/cli-review.json", "--if-version", "1")
	if push.Result != "saved" || push.Definition.Version != 2 {
		t.Fatalf("push v2 = %+v", push)
	}
	// Someone edited version 1 while version 2 landed: refused.
	write("workflows/stale.json", strings.Replace(cliReview, `"state": "needs_review"`, `"state": "rejected"`, 1))
	r.run(cli.ExitNetwork, &refused, "workflow", "push", "workflows/stale.json", "--if-version", "1")
	if refused.Error.Code != "precondition_failed" || !strings.Contains(refused.Error.Fix, "glossa workflow pull cli-review") {
		t.Fatalf("stale push = %+v", refused)
	}

	var bind struct {
		Result  string
		Binding struct {
			ID           string
			DefinitionID string `json:"definition_id"`
			Locales      []string
		}
	}
	r.run(cli.ExitOK, &bind, "workflow", "bind", "cli-review", "--locales", "de")
	if bind.Result != "created" || bind.Binding.DefinitionID != push.Definition.ID || len(bind.Binding.Locales) != 1 {
		t.Fatalf("bind = %+v", bind)
	}
	r.run(cli.ExitOK, &bind, "workflow", "bind", "cli-review", "--locales", "de")
	if bind.Result != "unchanged" {
		t.Fatalf("bind again = %+v", bind)
	}

	// A translation revision under the binding starts an instance on
	// version 2, which the revision itself finishes.
	write("locales/de.json", `{"home.title": "Willkommen"}`)
	r.run(cli.ExitOK, nil, "push", "--translations")
	type instance struct {
		ID, State, Status, Locale, Definition string
		DefinitionVersion                     int `json:"definition_version"`
	}
	var instances struct{ Instances []instance }
	deadline := time.Now().Add(20 * time.Second)
	for {
		r.run(cli.ExitOK, &instances, "workflow", "instances", "--status", "finished", "--definition", "cli-review")
		if len(instances.Instances) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(instances.Instances) != 1 {
		t.Fatalf("instances = %+v\n%s", instances, s.logs)
	}
	in := instances.Instances[0]
	if in.State != "done" || in.Locale != "de" || in.DefinitionVersion != 2 || in.Definition != "cli-review" {
		t.Fatalf("instance = %+v", in)
	}
	var log struct {
		Instance    instance
		Transitions []struct{ From, Event, To, Outcome, Actor string }
	}
	r.run(cli.ExitOK, &log, "workflow", "log", in.ID)
	if log.Instance.ID != in.ID || len(log.Transitions) != 1 {
		t.Fatalf("log = %+v", log)
	}
	// The event that creates an instance moves it from no state at all.
	if tr := log.Transitions[0]; tr.From != "" || tr.Event != "translation.revised" || tr.To != "done" ||
		tr.Outcome != "applied" || !strings.HasPrefix(tr.Actor, "token:") {
		t.Fatalf("transition = %+v", tr)
	}
	r.run(cli.ExitOK, &bind, "workflow", "unbind", "cli-review")
	if bind.Binding.ID == "" {
		t.Fatalf("unbind = %+v", bind)
	}

	// Assignments. A person makes one through the API; the CLI's token
	// is not a member, so it has no work, cannot make any and cannot
	// act on the person's — and every refusal says why.
	var assignment struct{ ID string }
	s.do(owner.call("POST", base+"/assignments", map[string]any{"project_id": project.ID,
		"assignee": map[string]any{"role": "translator"}, "units": []map[string]string{{"message": "checkout.pay", "locale": "de"}}}),
		http.StatusCreated, &assignment)
	var mine struct {
		Mine        bool
		Assignments []struct{ ID string }
	}
	r.run(cli.ExitOK, &mine, "assignments")
	if !mine.Mine || len(mine.Assignments) != 0 {
		t.Fatalf("a token's work = %+v", mine)
	}
	r.run(cli.ExitNetwork, &refused, "assignments", "create", "--to", "role:translator", "--units", "home.title@de")
	if refused.Error.Code != "assignments_manage_required" || !strings.Contains(refused.Error.Why, "no API token scope grants") {
		t.Fatalf("create = %+v", refused)
	}
	for _, action := range []string{"show", "accept", "complete"} {
		r.run(cli.ExitNetwork, &refused, "assignments", action, assignment.ID)
		if refused.Error.Code != "not_found" || !strings.Contains(refused.Error.Fix, "an API token is none of them") {
			t.Fatalf("%s = %+v", action, refused)
		}
	}
}
