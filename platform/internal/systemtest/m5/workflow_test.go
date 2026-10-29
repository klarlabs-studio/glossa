//go:build system

package m5_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ── §12.1 Two workflows, one event ───────────────────────────────────

// definitionRef is a saved workflow definition version.
type definitionRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// instance is a workflow instance as the API reads it back
// (WorkflowInstance in platform/api/openapi.yaml).
type instance struct {
	ID         string `json:"id"`
	Definition string `json:"definition_id"`
	Version    int    `json:"definition_version"`
	State      string `json:"state"`
	Status     string `json:"status"`
}

// transition is one row of an instance's transition log (§2.5), as
// WorkflowTransition in platform/api/openapi.yaml spells it. Actions
// carry no actor of their own: a transition's actions run as the actor
// whose event caused it (§2.5), so that actor is the transition's.
type transition struct {
	From    string `json:"from"`
	Event   string `json:"event"`
	To      string `json:"to"`
	Outcome string `json:"outcome"`
	Actor   string `json:"actor"`
	Actions []struct {
		Name    string `json:"name"`
		Outcome string `json:"outcome"`
	} `json:"actions"`
}

// instanceFinished is the status of an instance that reached a final
// state (WorkflowInstanceStatus).
const instanceFinished = "finished"

func (s *scenario) twoWorkflows() {
	const id = "12.1"
	// The three refusals are independent of everything else: each is a
	// definition the platform must turn away at save with 422.
	for _, bad := range []struct{ file, why string }{
		{"invalid-unknown-guard.json", "naming a guard the vocabulary does not have"},
		{"invalid-unreachable-state.json", "with an unreachable state"},
		{"invalid-delayed-transition.json", "with a delayed transition"},
	} {
		s.step(id, "refuse a definition "+bad.why+" with 422 `invalid_workflow`", func() error {
			_, err := s.saveDefinition(bad.file)
			switch {
			case err == nil:
				return fmt.Errorf("it was saved")
			case statusOf(err) == http.StatusUnprocessableEntity:
				return nil
			}
			return missing("saving a workflow definition (POST "+s.workflowDefinitionsPath()+")", err)
		})
	}

	s.archTest = s.architectureTest()

	var b definitionRef
	if !s.step(id, "save `vendor-then-four-eyes` (testdata) through the API", func() error {
		var err error
		b, err = s.saveDefinition("vendor-then-four-eyes.json")
		if err != nil {
			return missing("saving a workflow definition (POST "+s.workflowDefinitionsPath()+")", err)
		}
		if b.Version != 1 {
			return fmt.Errorf("the first save is version %d, want 1", b.Version)
		}
		return nil
	}) {
		s.unreachedWorkflow("find the seeded default definition")
		return
	}
	var a definitionRef
	if !s.step(id, "find the seeded default definition (`defaults/review.json`)", func() error {
		defs, err := list[definitionRef](s.owner, s.workflowDefinitionsPath(), nil)
		if err != nil {
			return missing("listing workflow definitions", err)
		}
		for _, d := range defs {
			if d.Name == "review" {
				a = d
				return nil
			}
		}
		return fmt.Errorf("the tenant has %d definition(s) and none is the seeded `review`", len(defs))
	}) {
		s.unreachedWorkflow("bind A and B")
		return
	}
	if !s.step(id, "bind project A to the default and project B to `vendor-then-four-eyes` for `de`", func() error {
		if _, err := s.owner.try(http.MethodPost, s.workflowBindingsPath(s.projectA),
			map[string]any{"definition_id": a.ID}, http.StatusCreated, nil); err != nil {
			return missing("binding a definition to project A", err)
		}
		if _, err := s.owner.try(http.MethodPost, s.workflowBindingsPath(s.projectB),
			map[string]any{"definition_id": b.ID, "locales": []string{"de"}}, http.StatusCreated, nil); err != nil {
			return missing("binding a definition to project B", err)
		}
		return nil
	}) {
		s.unreachedWorkflow("revise the shared source")
		return
	}
	// The one event: the same new source text, so the same source
	// revision, in both projects.
	if !s.step(id, "revise `"+sharedKey+"`'s source in both projects to the same revision", func() error {
		for _, p := range []string{s.projectA, s.projectB} {
			var out struct {
				Results []struct {
					Status string `json:"status"`
				} `json:"results"`
			}
			if _, err := s.owner.try(http.MethodPost, s.projectPathOf(p, "/message-upserts"), map[string]any{
				"items": []map[string]any{{"key": sharedKey, "text": "Welcome back, {$name}! " + canaries[4], "syntax": "mf2"}},
			}, http.StatusOK, &out); err != nil {
				return err
			}
			if len(out.Results) != 1 || out.Results[0].Status != "revised" {
				return fmt.Errorf("the push answered %+v, want one `revised`", out.Results)
			}
		}
		return nil
	}) {
		s.unreachedWorkflow("project A's instance")
		return
	}

	okA := s.projectAPath()
	okB := s.projectBPath()
	if okA && okB {
		s.transitionLogs()
	} else {
		s.unreached(id, "both instances reached a final state, every action ran as a person, never as Workflow's principal")
	}
}

// unreachedWorkflow lists what a stop before the instances leaves
// unproven.
func (s *scenario) unreachedWorkflow(from string) {
	steps := []string{
		"find the seeded default definition",
		"bind A and B",
		"revise the shared source",
		"project A's instance",
		"A: the `de` translation goes to `needs_review`",
		"A: one reviewer's approval makes it `approved`",
		"B: assigned to the vendor",
		"B: the vendor member writes and completes it",
		"B: the author's own approval is refused",
		"B: a token's approval is refused",
		"B: one reviewer is not enough",
		"B: two distinct reviewers make it `approved`",
		"both instances reached a final state, every action ran as a person, never as Workflow's principal",
	}
	for i, st := range steps {
		if st == from {
			s.unreached("12.1", steps[i:]...)
			return
		}
	}
	s.unreached("12.1", steps...)
}

func (s *scenario) saveDefinition(file string) (definitionRef, error) {
	raw, err := os.ReadFile(filepath.Join("testdata", "workflows", file))
	if err != nil {
		fatalf("%v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		fatalf("%s: %v", file, err)
	}
	var d definitionRef
	_, err = s.owner.try(http.MethodPost, s.workflowDefinitionsPath(), body, http.StatusCreated, &d)
	return d, err
}

// instanceOf finds the instance for the shared message's `de` unit.
func (s *scenario) instanceOf(project string) (instance, error) {
	q := url.Values{"message": {sharedKey}, "locale": {"de"}}
	items, err := list[instance](s.owner, s.workflowInstancesPath(project), q)
	if err != nil {
		return instance{}, missing("listing workflow instances", err)
	}
	if len(items) == 0 {
		return instance{}, fmt.Errorf("no instance for `%s`/de: the source change started no workflow", sharedKey)
	}
	return items[len(items)-1], nil
}

func (s *scenario) translationState(project string) (string, string, error) {
	var tr struct {
		State string `json:"state"`
	}
	h, err := s.owner.try(http.MethodGet, s.projectPathOf(project, "/messages/"+sharedKey+"/translations/de"), nil, http.StatusOK, &tr)
	if err != nil {
		return "", "", err
	}
	return tr.State, h.Get("ETag"), nil
}

// approvalOf finds the open approval on the shared `de` unit.
func (s *scenario) approvalOf(as *client, project string) (string, error) {
	items, err := list[struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}](as, s.approvalsPath(), url.Values{"project": {project}, "message": {sharedKey}, "locale": {"de"}})
	if err != nil {
		return "", missing("listing approvals", err)
	}
	for _, a := range items {
		if a.State == "pending" {
			return a.ID, nil
		}
	}
	return "", fmt.Errorf("no open approval on `%s`/de (%d listed)", sharedKey, len(items))
}

func (s *scenario) decide(as *client, approval string) error {
	_, err := as.try(http.MethodPost, s.approvalDecisions(approval),
		map[string]any{"decision": "granted", "reason": "reads well"}, http.StatusCreated, nil)
	return err
}

// projectAPath: the default definition. One reviewer's approval is
// enough.
func (s *scenario) projectAPath() bool {
	const id = "12.1"
	if !s.step(id, "A: an instance of the default definition exists and the `de` translation is `needs_review`", func() error {
		ok, state := softly(15*time.Second, func() (bool, string) {
			inst, err := s.instanceOf(s.projectA)
			if err != nil {
				return false, err.Error()
			}
			st, _, err := s.translationState(s.projectA)
			if err != nil {
				return false, err.Error()
			}
			return st == "needs_review", fmt.Sprintf("instance in `%s`, translation `%s`", inst.State, st)
		})
		if !ok {
			return fmt.Errorf("%s", state)
		}
		return nil
	}) {
		s.unreached(id, "A: one reviewer's approval makes it `approved`")
		return false
	}
	return s.step(id, "A: one reviewer's approval makes it `approved`, by that reviewer in the revision log", func() error {
		appr, err := s.approvalOf(s.reviewer1, s.projectA)
		if err != nil {
			return err
		}
		if err := s.decide(s.reviewer1, appr); err != nil {
			return fmt.Errorf("the reviewer's decision: %w", err)
		}
		ok, state := softly(15*time.Second, func() (bool, string) {
			st, _, err := s.translationState(s.projectA)
			if err != nil {
				return false, err.Error()
			}
			return st == "approved", "`" + st + "`"
		})
		if !ok {
			return fmt.Errorf("after the approval the translation is %s, want `approved`", state)
		}
		return s.reviewedBy(s.projectA, s.reviewer1.actor)
	})
}

// projectBPath: vendor, then four eyes.
func (s *scenario) projectBPath() bool {
	const id = "12.1"
	var assignment string
	if !s.step(id, "B: the `de` unit is assigned to the vendor and appears in the vendor member's work", func() error {
		ok, state := softly(15*time.Second, func() (bool, string) {
			items, err := list[struct {
				ID       string   `json:"id"`
				Messages []string `json:"messages"`
				State    string   `json:"state"`
			}](s.vendor, s.assignmentsPath(), url.Values{"project": {s.projectB}, "message": {sharedKey}})
			if err != nil {
				return false, missing("listing the vendor member's assignments", err).Error()
			}
			for _, a := range items {
				assignment = a.ID
				return true, ""
			}
			return false, "the vendor member has no assignment covering it"
		})
		if !ok {
			return fmt.Errorf("%s", state)
		}
		return nil
	}) {
		s.unreachedWorkflow("B: the vendor member writes and completes it")
		return false
	}
	if !s.step(id, "B: the vendor member writes it and completes the assignment", func() error {
		if err := s.vendor.putTranslation(s.projectPathOf(s.projectB, "/messages/"+sharedKey+"/translations/de"),
			map[string]any{"text": "Schön, dass du wieder da bist, {$name}! " + canaries[4], "syntax": "mf2", "origin": "human"}); err != nil {
			return fmt.Errorf("the vendor member's translation: %w", err)
		}
		if _, err := s.vendor.try(http.MethodPost, s.assignmentPath(assignment)+"/completion", map[string]any{}, http.StatusOK, nil); err != nil {
			return missing("completing the assignment", err)
		}
		return nil
	}) {
		s.unreachedWorkflow("B: the author's own approval is refused")
		return false
	}
	var appr string
	if !s.step(id, "B: the author's own approval is refused", func() error {
		var err error
		ok, state := softly(15*time.Second, func() (bool, string) {
			appr, err = s.approvalOf(s.owner, s.projectB)
			return err == nil, fmt.Sprint(err)
		})
		if !ok {
			return fmt.Errorf("%s", state)
		}
		if err := s.decide(s.vendor, appr); err == nil {
			return fmt.Errorf("the author (the vendor member) approved their own text")
		} else if statusOf(err) != http.StatusForbidden && statusOf(err) != http.StatusConflict {
			return fmt.Errorf("the author's approval was refused as %v, want 403 or 409", err)
		}
		return nil
	}) {
		s.unreachedWorkflow("B: a token's approval is refused")
		return false
	}
	s.step(id, "B: a token's approval is refused (no scope grants `approvals.decide`)", func() error {
		if err := s.decide(s.ownerToken, appr); err == nil {
			return fmt.Errorf("an API token approved")
		}
		return nil
	})
	if !s.step(id, "B: after one reviewer the translation is still not `approved`", func() error {
		if err := s.decide(s.reviewer1, appr); err != nil {
			return fmt.Errorf("the first reviewer's decision: %w", err)
		}
		// Give the outbox every chance to do the wrong thing, and watch
		// the whole window rather than look once at its end.
		return staysFor(3*time.Second, func() (string, error) {
			st, _, err := s.translationState(s.projectB)
			if err != nil {
				return "", err
			}
			if st == "approved" {
				return "one approval made it `approved`", nil
			}
			return "", nil
		})
	}) {
		s.unreachedWorkflow("B: two distinct reviewers make it `approved`")
		return false
	}
	return s.step(id, "B: the second reviewer's approval makes it `approved`, by that reviewer in the revision log", func() error {
		if err := s.decide(s.reviewer2, appr); err != nil {
			return fmt.Errorf("the second reviewer's decision: %w", err)
		}
		ok, state := softly(15*time.Second, func() (bool, string) {
			st, _, err := s.translationState(s.projectB)
			if err != nil {
				return false, err.Error()
			}
			return st == "approved", "`" + st + "`"
		})
		if !ok {
			return fmt.Errorf("after two approvals the translation is %s, want `approved`", state)
		}
		return s.reviewedBy(s.projectB, s.reviewer2.actor)
	})
}

// reviewedBy reads the revision log: the newest review revision's
// author is the person whose approval completed the definition (§2.5:
// actions run as the actor whose event moved the instance).
func (s *scenario) reviewedBy(project, actor string) error {
	revs, err := list[struct {
		Kind   string `json:"kind"`
		State  string `json:"state"`
		Author string `json:"author"`
	}](s.owner, s.projectPathOf(project, "/messages/"+sharedKey+"/translations/de/revisions"), nil)
	if err != nil {
		return err
	}
	for _, r := range revs {
		if r.Kind == "review" && r.State == "approved" {
			if !strings.HasSuffix(actor, r.Author) && r.Author != actor {
				return fmt.Errorf("the approving revision's author is %q, want %s", r.Author, actor)
			}
			return nil
		}
	}
	return fmt.Errorf("the revision log has no approving review revision")
}

// transitionLogs reads both instances' logs: each reached a final state,
// and every action ran as a person — never as Workflow's principal.
func (s *scenario) transitionLogs() {
	s.step("12.1", "both instances reached a final state, every action ran as a person, never as Workflow's principal", func() error {
		for _, p := range []string{s.projectA, s.projectB} {
			inst, err := s.instanceOf(p)
			if err != nil {
				return err
			}
			if inst.Status != instanceFinished {
				return fmt.Errorf("instance %s stranded in `%s` (%s)", short(inst.ID), inst.State, inst.Status)
			}
			ts, err := list[transition](s.owner, s.workflowTransitionsPath(p, inst.ID), nil)
			if err != nil {
				return missing("reading the transition log", err)
			}
			for _, tr := range ts {
				s.workflowLog = append(s.workflowLog, fmt.Sprintf("%s: %s —%s→ %s (%s)", short(inst.ID), tr.From, tr.Event, tr.To, tr.Actor))
				for _, a := range tr.Actions {
					if strings.HasPrefix(tr.Actor, "system:") && a.Name == "approve" {
						return fmt.Errorf("the `approve` action in %s ran as %s", short(inst.ID), tr.Actor)
					}
				}
			}
		}
		return nil
	})
}

// architectureTest runs §2.1's architecture test, which the Workflow
// domain slice writes: ReviewState has exactly four values, and no
// domain or app package of Localization, Release or Intelligence
// imports internal/workflow.
func (s *scenario) architectureTest() string {
	const id = "12.1"
	if _, err := os.Stat(filepath.Join(platformDir(), "internal", "workflow")); err != nil {
		s.step(id, "§2.1's architecture test passes", func() error {
			return fmt.Errorf("there is no `internal/workflow` package, so there is no architecture test to run")
		})
		return "not run: no internal/workflow"
	}
	// The §2.1 rules, by the names they were written under, each of
	// which must report its own pass: a -run pattern that matches
	// nothing passes vacuously, which is how this step once "ran" no
	// test at all.
	rules := []string{
		"TestReviewStateHasExactlyFourValues",      // rule 1
		"TestNoProcessIsCompiledIntoGo",            // rule 2
		"TestGuardedContextsDoNotBranchOnWho",      // rule 3
		"TestGuardedContextsDoNotDependOnWorkflow", // the dependency rule
		"TestTheVocabularyIsClosed",                // no process-named primitive
	}
	cmd := exec.Command("go", "test", "-count=1", "-v", "-run", "^("+strings.Join(rules, "|")+")$",
		"./internal/workflow/...")
	cmd.Dir = platformDir()
	out, err := cmd.CombinedOutput()
	verdict := strings.TrimSpace(string(out))
	s.step(id, "§2.1's architecture test passes", func() error {
		if err != nil {
			return fmt.Errorf("§2.1's architecture tests failed: %s", lastLines(verdict, 6))
		}
		var missing []string
		for _, r := range rules {
			if !strings.Contains(verdict, "--- PASS: "+r+" ") {
				missing = append(missing, r)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("§2.1's architecture tests did not all run: %s reported no pass", strings.Join(missing, ", "))
		}
		return nil
	})
	return verdict
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], " / ")
}
