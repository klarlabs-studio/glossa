package app_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/qa"
	"go.klarlabs.de/glossa/platform/internal/cli/snapshot"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	quality "go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// Two surfaces, one verdict (RFC 0005 §12.3).
//
// This is the milestone's exit criterion, not a nice-to-have: the same
// commit, graded by `glossa check` and by the Glossa pull-request check
// through the fake GitHub of RFC 0004 §12, must reach the same
// conclusion, the same error count and the same counts per layer.
//
// It is reached by construction rather than by agreement. The two
// surfaces used to compute two different things and be tested for
// equality: `glossa check` ran every layer over the whole project,
// while the pull request read the warnings the server happened to hold
// for the branch's own keys. Two computations tested for agreement will
// disagree, and did. Now `glossa check` records what it found
// (`createCheckRun`) and the pull request **renders that run**. One
// computation, two presentations. What the tests below pin is that the
// pull request adds nothing to the run, subtracts nothing from it, and
// says plainly when there is no run to render.

// checkoutBranch is the commit both surfaces grade: three new keys, one
// of which does not parse, translations that are missing or
// incompatible in two locales, and a usage of a key the catalog never
// had.
func checkoutBranch(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	msg := func(key, text string) snapshot.Message {
		model, args, invalid := snapshot.Parse("mf1", text, "en")
		return snapshot.Message{Key: key, Text: text, Model: model, Arguments: args, Invalid: invalid}
	}
	tr := func(key, locale, text string) snapshot.Translation {
		model, _, invalid := snapshot.Parse("mf1", text, locale)
		return snapshot.Translation{
			Key: key, Locale: locale, Text: text, Model: model, Invalid: invalid, State: "approved",
		}
	}
	s := &snapshot.Snapshot{
		Origin: "server", SourceLocale: "en",
		Locales: []snapshot.Locale{{Code: "en", IsSource: true}, {Code: "de"}, {Code: "fr"}},
		Messages: []snapshot.Message{
			msg("cart.items", "{count, plural, one {# item} other {# items}}"),
			msg("checkout.pay", "Pay {amount, number}"),
			// Rejected by the kernel: the brace never closes.
			msg("checkout.total", "Total: {total, number"),
		},
		Translations: map[string]map[string]snapshot.Translation{
			"de": {
				"cart.items": tr("cart.items", "de", "{count, plural, one {# Artikel} other {# Artikel}}"),
				// Drops {amount}: a parity error.
				"checkout.pay": tr("checkout.pay", "de", "Bezahlen"),
			},
			"fr": {
				"cart.items": tr("cart.items", "fr", "{count, plural, one {# article} other {# articles}}"),
				// A translation of a key no source message has.
				"legacy.banner": tr("legacy.banner", "fr", "Bannière"),
			},
		},
	}
	if s.Messages[2].Invalid == nil {
		t.Fatal("the fixture's invalid message parsed; the structure layer has nothing to find")
	}
	return s
}

// waivedTerm is the finding a waiver has accepted. The terminology
// layer is the server's — `glossa check --terminology` fetches it — so
// both surfaces are handed the one the server stored, which is what a
// waiver being honoured on both looks like.
func waivedTerm() quality.Finding {
	f := quality.New(quality.Finding{
		Layer: quality.LayerTerminology, Code: "term_forbidden", Severity: quality.Error,
		Locus: quality.Locus{
			Key: "cart.items", Locale: "de", File: "src/cart/CartList.vue", Line: 18,
		},
		Subject: "Artikel", Message: `"Artikel" is forbidden in de; the term is "Position"`,
	})
	return f.Waive("w_cart_items")
}

// storedFindings is what the Quality context holds for this branch:
// every deterministic layer's findings, computed by the same code
// `glossa check` links, plus the server-only terminology layer.
func storedFindings(s *snapshot.Snapshot, policy checkpolicy.Policy) []quality.Finding {
	p := qa.Project(s)
	var out []quality.Finding
	for _, c := range layers.Default() {
		out = append(out, c.Check(p, policy)...)
	}
	return append(out, waivedTerm())
}

// serverView is what Catalog, Localization and Context report for this
// commit, the way the real adapters would: the branch's keys, the
// messages the push could not accept, the per-locale gap, the stored
// findings, and the usages Context ingested.
func serverView(m *memSources, s *snapshot.Snapshot, policy checkpolicy.Policy) {
	m.policy = policy
	var keys []string
	for _, msg := range s.Messages {
		keys = append(keys, msg.Key)
		if msg.Invalid != nil {
			m.status.Invalid = append(m.status.Invalid, app.InvalidMessage{
				Key: msg.Key, Code: msg.Invalid.Code, Detail: msg.Invalid.Detail,
			})
		}
	}
	sort.Strings(keys)
	m.status.NewKeys = keys
	m.quality.Locales = nil
	m.quality.Untranslated = map[string]int{}
	for _, l := range s.Locales {
		if l.IsSource {
			continue
		}
		m.quality.Locales = append(m.quality.Locales, l.Code)
		missing := 0
		for _, msg := range s.Messages {
			if t, ok := s.Translations[l.Code][msg.Key]; !ok || t.State == "rejected" {
				missing++
			}
		}
		m.quality.Untranslated[l.Code] = missing
	}
	m.quality.Findings = storedFindings(s, policy)
	// Context knows where the product asks for the key the catalog never
	// had, which is what turns that finding into an annotation.
	m.usages.Unknown = []app.UnknownKey{{Key: "legacy.banner", File: "src/Banner.vue", Line: 4}}
	m.usages.Captured, m.usages.NotCaptured = 2, 1
}

// layerTable is the per-layer breakdown as the check run's summary
// renders it, built here from the CLI's own findings.
func layerTable(r qa.Report) string {
	var b strings.Builder
	b.WriteString("| Layer | Errors | Warnings | Waived |\n")
	b.WriteString("| --- | ---: | ---: | ---: |\n")
	for _, l := range quality.ByLayer(r.Findings) {
		fmt.Fprintf(&b, "| %s | %d | %d | %d |\n", l.Layer, l.Counts.Errors, l.Counts.Warnings, l.Counts.Waived)
	}
	fmt.Fprintf(&b, "| **Total** | **%d** | **%d** | **%d** |\n",
		r.Counts.Errors, r.Counts.Warnings, r.Counts.Waived)
	return b.String()
}

// TestThePullRequestAndTheTerminalReachTheSameVerdict is RFC 0005
// §12.3, the wave's exit criterion.
func TestThePullRequestAndTheTerminalReachTheSameVerdict(t *testing.T) {
	policy := checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Error}
	commit := checkoutBranch(t)

	// The terminal. This is `runCheck`'s own line, with the layers the
	// command runs plus the terminology findings it fetches.
	cli := qa.Run(commit, policy, append(qa.Default(),
		qa.Precomputed(quality.LayerTerminology, []quality.Finding{waivedTerm()}))...)
	if cli.Conclusion != quality.ConclusionFailure {
		t.Fatalf("the fixture passes `glossa check` (%+v); there is nothing for the two surfaces to agree about",
			cli.Counts)
	}
	if cli.Counts.Waived != 1 {
		t.Fatalf("counts = %+v, want the waiver honoured on the terminal too", cli.Counts)
	}

	// The pull request, through the fake GitHub. CI records what the
	// terminal found, and the check renders that run — while the read
	// model still says everything it used to, so what is pinned here is
	// that the recorded run is what the report is made of and the
	// narrower view no longer leaks into it.
	f := newFixture(t)
	f.connected(t)
	f.openPR(t, "pull_request.opened", "d-open")
	f.ci(headSHA)
	f.sources.set(func(m *memSources) { serverView(m, commit, policy) })
	f.recorded(headSHA, recordedRunOf(cli, branchName))
	f.runCheck(t)

	run := f.theCheck(t)
	if run.Status != app.CheckCompleted {
		t.Fatalf("check run = %+v, want it completed", run)
	}
	if !strings.Contains(run.Summary, "the `glossa check` run CI recorded for this commit") {
		t.Fatalf("the summary does not say it is rendering the recorded run:\n%s", run.Summary)
	}
	if strings.Contains(run.Summary, app.ReducedViewNotice) {
		t.Fatalf("a rendered run called itself a reduced view:\n%s", run.Summary)
	}
	if run.Conclusion != string(cli.Conclusion) {
		t.Fatalf("the pull request concluded %q and the terminal %q", run.Conclusion, cli.Conclusion)
	}
	if want := layerTable(cli); !strings.Contains(run.Summary, want) {
		t.Fatalf("the pull request's counts per layer are not the terminal's.\nwant:\n%s\ngot:\n%s",
			want, run.Summary)
	}

	// The located findings are on the diff, and the waived one is a
	// notice rather than a warning: visible, never counted.
	var notices int
	for _, a := range run.Annotations {
		if a.Path == "" || a.StartLine == 0 {
			t.Fatalf("an annotation points at no line: %+v", a)
		}
		if a.AnnotationLevel == "notice" {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("annotations = %+v, want the waived finding as the one notice", run.Annotations)
	}
	// It is listed on its own, and it did not fail the run.
	if !strings.Contains(run.Summary, "**Accepted by a waiver** (1)") {
		t.Fatalf("the waived finding is not shown separately:\n%s", run.Summary)
	}
	// And the summary says which policy graded the commit.
	if !strings.Contains(run.Summary, "Graded against the project's check policy v7.") {
		t.Fatalf("the summary does not name the policy version:\n%s", run.Summary)
	}
	f.theComment(t) // still exactly one
}

// TestTheTwoSurfacesAgreeWhenTheBranchIsFixed: agreement is not a
// property of a red build. The same commit with its translations
// landed goes green on both, and the counts still match — including
// the waiver, which survives a green run rather than disappearing with
// the failures.
func TestTheTwoSurfacesAgreeWhenTheBranchIsFixed(t *testing.T) {
	policy := checkpolicy.Policy{Version: 7, FailOn: checkpolicy.Error}
	commit := checkoutBranch(t)
	fixed := fixBranch(t, commit)

	cli := qa.Run(fixed, policy, append(qa.Default(),
		qa.Precomputed(quality.LayerTerminology, []quality.Finding{waivedTerm()}))...)
	if cli.Conclusion != quality.ConclusionSuccess {
		t.Fatalf("the fixed fixture still fails `glossa check`: %+v", cli.Findings)
	}

	f := newFixture(t)
	f.connected(t)
	f.openPR(t, "pull_request.opened", "d-open")
	f.ci(headSHA)
	f.sources.set(func(m *memSources) {
		serverView(m, fixed, policy)
		m.usages.Unknown = nil
	})
	f.recorded(headSHA, recordedRunOf(cli, branchName))
	f.runCheck(t)

	run := f.theCheck(t)
	if run.Conclusion != string(cli.Conclusion) {
		t.Fatalf("the pull request concluded %q and the terminal %q", run.Conclusion, cli.Conclusion)
	}
	if want := layerTable(cli); !strings.Contains(run.Summary, want) {
		t.Fatalf("the pull request's counts per layer are not the terminal's.\nwant:\n%s\ngot:\n%s",
			want, run.Summary)
	}
	if !strings.Contains(run.Summary, "**Accepted by a waiver** (1)") {
		t.Fatalf("a green check dropped the waived finding:\n%s", run.Summary)
	}
}

// fixBranch is the same commit with the message fixed, the parity
// restored and every locale complete — the second push of a pull
// request that was red.
func fixBranch(t *testing.T, s *snapshot.Snapshot) *snapshot.Snapshot {
	t.Helper()
	msg := func(key, text string) snapshot.Message {
		model, args, invalid := snapshot.Parse("mf1", text, "en")
		if invalid != nil {
			t.Fatalf("the fixed %s does not parse: %s", key, invalid.Detail)
		}
		return snapshot.Message{Key: key, Text: text, Model: model, Arguments: args}
	}
	tr := func(key, locale, text string) snapshot.Translation {
		model, _, invalid := snapshot.Parse("mf1", text, locale)
		if invalid != nil {
			t.Fatalf("the fixed %s/%s does not parse: %s", locale, key, invalid.Detail)
		}
		return snapshot.Translation{Key: key, Locale: locale, Text: text, Model: model, State: "approved"}
	}
	out := &snapshot.Snapshot{
		Origin: s.Origin, SourceLocale: s.SourceLocale, Locales: s.Locales,
		Messages: []snapshot.Message{
			s.Messages[0], s.Messages[1], msg("checkout.total", "Total: {total, number}"),
		},
		Translations: map[string]map[string]snapshot.Translation{
			"de": {
				"cart.items":     s.Translations["de"]["cart.items"],
				"checkout.pay":   tr("checkout.pay", "de", "Bezahlen {amount, number}"),
				"checkout.total": tr("checkout.total", "de", "Summe: {total, number}"),
			},
			"fr": {
				"cart.items":     s.Translations["fr"]["cart.items"],
				"checkout.pay":   tr("checkout.pay", "fr", "Payer {amount, number}"),
				"checkout.total": tr("checkout.total", "fr", "Total : {total, number}"),
			},
		},
	}
	return out
}
