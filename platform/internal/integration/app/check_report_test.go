package app_test

import (
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// The report is a pure function from what the contexts said to the
// check run's output, so its rules are pinned here rather than through
// GitHub.

func TestTheReportLocatesAnInvalidMessageFromItsUsage(t *testing.T) {
	rep := app.BuildCheckReport(app.CheckInput{
		Status: app.BranchStatus{
			Name:     "feat/copy",
			NewKeys:  []string{"checkout.pay"},
			Invalid:  []app.InvalidMessage{{Key: "checkout.pay", Code: "invalid_content", Detail: "unmatched '{'"}},
			Outdated: map[string]int{"de": 3},
		},
		Quality: app.BranchQuality{Locales: []string{"de"}, Untranslated: map[string]int{"de": 1}},
		Usages: app.BranchUsages{
			Builds:  1,
			Unknown: []app.UnknownKey{{Key: "checkout.pay", File: "src/Pay.vue", Line: 9}},
		},
	})
	if rep.Conclusion != app.ConclusionFailure {
		t.Fatalf("conclusion = %q, want failure", rep.Conclusion)
	}
	// The invalid message, the missing translation and the unknown key.
	if rep.Errors != 2 || rep.Warnings != 2 {
		t.Fatalf("errors = %d, warnings = %d: %+v", rep.Errors, rep.Warnings, rep.Findings)
	}
	var invalid, unknown bool
	for _, a := range rep.Annotations {
		if a.Path != "src/Pay.vue" || a.StartLine != 9 {
			t.Fatalf("annotation without the usage's location: %+v", a)
		}
		switch {
		case strings.HasPrefix(a.Title, checkpolicy.CodeInvalidMessage):
			invalid = true
		case strings.HasPrefix(a.Title, checkpolicy.CodeUnknownKey):
			unknown = true
		}
	}
	if !invalid || !unknown {
		t.Fatalf("annotations = %+v, want the invalid message and the unknown key", rep.Annotations)
	}
	// A finding with no location stays in the summary.
	if !strings.Contains(rep.Summary, "| de |") || !strings.Contains(rep.Summary, "| Locale |") {
		t.Fatalf("summary has no per-locale table:\n%s", rep.Summary)
	}
}

func TestAReadyBranchPassesAndSaysSo(t *testing.T) {
	rep := app.BuildCheckReport(app.CheckInput{
		Status:  app.BranchStatus{Name: "feat/copy", NewKeys: []string{"a"}, Outdated: map[string]int{}},
		Quality: app.BranchQuality{Locales: []string{"de"}, Untranslated: map[string]int{"de": 0}},
		Usages:  app.BranchUsages{Builds: 1},
	})
	if rep.Conclusion != app.ConclusionSuccess || rep.Errors != 0 || rep.Warnings != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if len(rep.Annotations) != 0 {
		t.Fatalf("annotations = %+v, want none", rep.Annotations)
	}
	if !strings.Contains(rep.Title, "ready") {
		t.Fatalf("title = %q", rep.Title)
	}
}

// TestAnUnrequiredLocaleIsOnlyAWarning: the policy is `glossa check`'s,
// so `require_complete` decides here exactly as it does on a terminal.
func TestAnUnrequiredLocaleIsOnlyAWarning(t *testing.T) {
	in := app.CheckInput{
		Policy:  checkpolicy.Policy{RequireComplete: []string{"de"}},
		Status:  app.BranchStatus{Name: "feat/copy", NewKeys: []string{"a"}, Outdated: map[string]int{}},
		Quality: app.BranchQuality{Locales: []string{"de", "fr"}, Untranslated: map[string]int{"fr": 1}},
	}
	if c := app.BuildCheckReport(in).Conclusion; c != app.ConclusionSuccess {
		t.Fatalf("conclusion = %q, want success: fr is not required", c)
	}
	// fail_on: warning turns the same report red, again as the command does.
	in.Policy.FailOn = checkpolicy.Warning
	if c := app.BuildCheckReport(in).Conclusion; c != app.ConclusionFailure {
		t.Fatalf("conclusion = %q with fail_on=warning, want failure", c)
	}
}

// TestTheProjectPolicyDecidesTheConclusion: the same branch, the same
// findings, three stored policies. This is the whole point of storing
// one — the default fails the pull request, and a project that only
// warns about untranslated keys goes green while still listing them.
func TestTheProjectPolicyDecidesTheConclusion(t *testing.T) {
	branch := app.CheckInput{
		Status:  app.BranchStatus{Name: "feat/copy", NewKeys: []string{"a"}, Outdated: map[string]int{}},
		Quality: app.BranchQuality{Locales: []string{"de", "fr"}, Untranslated: map[string]int{"de": 1, "fr": 1}},
		Usages:  app.BranchUsages{Builds: 1},
	}
	tests := []struct {
		name          string
		policy        checkpolicy.Policy
		want          string
		errors, warns int
	}{
		{name: "the default fails", want: app.ConclusionFailure, errors: 2},
		{
			name:   "only warn about untranslated keys",
			policy: checkpolicy.Policy{MissingTranslations: checkpolicy.Warning},
			want:   app.ConclusionSuccess, warns: 2,
		},
		{
			name:   "no locale has to be complete",
			policy: checkpolicy.Policy{RequireComplete: []string{}},
			want:   app.ConclusionSuccess, warns: 2,
		},
		{
			name:   "never fails, whatever it finds",
			policy: checkpolicy.Policy{FailOn: checkpolicy.Never},
			want:   app.ConclusionSuccess, errors: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := branch
			in.Policy = tc.policy
			rep := app.BuildCheckReport(in)
			if rep.Conclusion != tc.want {
				t.Fatalf("conclusion = %q, want %q", rep.Conclusion, tc.want)
			}
			if rep.Errors != tc.errors || rep.Warnings != tc.warns {
				t.Fatalf("errors = %d, warnings = %d, want %d and %d", rep.Errors, rep.Warnings, tc.errors, tc.warns)
			}
			// Green or not, the check still reports the gap: the table
			// says one key is untranslated in each locale.
			if !strings.Contains(rep.Summary, "| de | 0 | 1 | 0 |") {
				t.Fatalf("summary does not list the gap:\n%s", rep.Summary)
			}
		})
	}
}

// TestAnnotationsGoOncePerCheckRun is the rule GitHub's append-only
// annotations force on us.
func TestAnnotationsGoOncePerCheckRun(t *testing.T) {
	all := []app.CheckAnnotation{
		{Path: "a.vue", StartLine: 1, Message: "one"},
		{Path: "a.vue", StartLine: 2, Message: "two"},
		// The same annotation twice in one report is still one.
		{Path: "a.vue", StartLine: 1, Message: "one"},
	}
	var t0 domain.CheckTarget
	send, prints := app.UnsentAnnotations(t0, all)
	if len(send) != 2 || len(prints) != 2 {
		t.Fatalf("first send = %d annotations, want 2: %+v", len(send), send)
	}
	t0.Record(prints)
	if send, _ := app.UnsentAnnotations(t0, all); len(send) != 0 {
		t.Fatalf("a repeat sent %d annotations, want none", len(send))
	}
	// A new line is new; the run's ledger keeps the rest.
	next := append(all, app.CheckAnnotation{Path: "b.vue", StartLine: 7, Message: "three"})
	send, _ = app.UnsentAnnotations(t0, next)
	if len(send) != 1 || send[0].Path != "b.vue" {
		t.Fatalf("send = %+v, want only the new annotation", send)
	}
	// A new run starts empty, because GitHub made a new run.
	if send, _ := app.UnsentAnnotations(domain.CheckTarget{}, next); len(send) != 3 {
		t.Fatalf("a new run sent %d annotations, want all 3", len(send))
	}
}

// TestTheCommentQuotesWhatItDidNotWrite: a key or a detail is content,
// not Markdown, and a comment is a public surface.
func TestTheCommentQuotesWhatItDidNotWrite(t *testing.T) {
	in := app.CheckInput{
		Status: app.BranchStatus{
			Name:     "feat/copy",
			Invalid:  []app.InvalidMessage{{Key: "a`b", Code: "x", Detail: "<img onerror=1> **bold** | pipe"}},
			Outdated: map[string]int{},
		},
		Quality: app.BranchQuality{Locales: []string{"de"}, Untranslated: map[string]int{}},
	}
	rep := app.BuildCheckReport(in)
	if strings.Contains(rep.Summary, "<img") {
		t.Fatalf("raw HTML reached the summary:\n%s", rep.Summary)
	}
	if strings.Contains(rep.Summary, "**bold**") {
		t.Fatalf("a detail became Markdown:\n%s", rep.Summary)
	}
	// A key with a backtick still renders as code.
	if !strings.Contains(rep.Summary, "``a`b``") {
		t.Fatalf("a key holding a backtick broke its fence:\n%s", rep.Summary)
	}
	body := app.StickyComment("acme/shop", app.CommentLinks{Studio: "https://studio.example/x"}, in, rep)
	for _, want := range []string{"acme/shop", "[Branch in Studio](https://studio.example/x)", "| Locale |"} {
		if !strings.Contains(body, want) {
			t.Fatalf("comment does not carry %q:\n%s", want, body)
		}
	}
}

func TestStudioBranchURLEscapesTheBranch(t *testing.T) {
	got := app.StudioBranchURL("https://studio.example/", tenantOne, projectID, "feat/a b&c")
	want := "https://studio.example/t/" + tenantOne.String() + "/p/" + projectID.String() +
		"/translate?branch=feat%2Fa+b%26c"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	if app.StudioBranchURL("", tenantOne, projectID, "x") != "" {
		t.Fatal("a deployment that names no Studio should link to none")
	}
}
