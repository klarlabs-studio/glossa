//go:build system

package m4_test

import (
	"bytes"
	"fmt"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// report renders REPORT.md: the eight criteria of RFC 0005 §12, each
// with what this run observed and, where it does not hold, exactly what
// is missing.
//
// It is written whether or not the criteria hold. An exit test's report
// is the verdict on the milestone, and a verdict that only exists when
// the answer is yes is not a verdict.
func (s *scenario) report() []byte {
	var b bytes.Buffer
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	met := 0
	for _, c := range s.criteria {
		if c.met() {
			met++
		}
	}

	w("# M4 exit test — report\n\n")
	w("Written by `TestM4Exit` (`make system-m4`, or `go test -tags=system ./internal/systemtest/m4/...` in\n")
	w("`platform/`) against a real glossa-server on Postgres and MinIO, a fake GitHub on loopback, a headless\n")
	w("Chrome the test starts, the Dart SDK, and Studio's own Playwright suite pointed at the same server.\n")
	w("Every number below comes from the public API, from GitHub's own view of the pull request, from the\n")
	w("browser, or from the runtime's own test suites. RFC 0005 §12.\n\n")

	w("## The verdict\n\n")
	w("**%d of the 8 exit criteria hold.**\n\n", met)
	w("| § | Criterion | Verdict |\n|---|---|---|\n")
	for _, c := range s.criteria {
		w("| %s | %s | %s |\n", c.id, c.title, c.verdict())
	}
	w("\n")
	if met < len(s.criteria) {
		w("What is missing, in one line each:\n\n")
		for _, c := range s.criteria {
			if c.met() {
				continue
			}
			if c.skipped != "" {
				w("- **§%s** was not run here: %s\n", c.id, c.skipped)
				continue
			}
			w("- **§%s**: %s\n", c.id, c.gaps[0])
			for _, g := range c.gaps[1:] {
				w("  - %s\n", g)
			}
		}
		w("\n")
	}

	s.reportFixture(&b)
	s.reportLayers(&b)
	s.reportAgreement(&b)
	s.reportWaivers(&b)
	s.reportRelease(&b)
	s.reportMCP(&b)
	s.reportFlutter(&b)
	s.reportDashboard(&b)
	s.reportNotes(&b)
	return b.Bytes()
}

func (s *scenario) reportFixture(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## §12.1 — a fixture repository with real CI\n\n")
	w("`testdata/repo` is the **M3 fixture application** — Brotwerk's shop, 150 messages over eight routes,\n")
	w("source `de`, targets `en`, `es`, `fr`, `ja`, with its committed Vite build — plus what M4 adds: a\n")
	w("`.github/workflows/quality.yml` job that runs `glossa push`, `glossa context push`, `glossa check --json`\n")
	w("and `glossa capture --check --upload`; a capture plan for `/kasse` in German and Japanese; and one\n")
	w("stylesheet that gives the checkout page a fixed-width pay button. The test materializes the repository,\n")
	w("runs the workflow's commands, and never writes to the committed M3 fixture.\n\n")
	w("The project's check policy is **v%d**: `require_complete: [de, en]`, `terminology` an error in the\n", s.gradedVersion)
	w("`legal` namespace, `visual` in `warn`, and a `production` environment that also requires `fr`.\n\n")
	if s.pushed != nil {
		w("`glossa push --translations` reported %s.\n\n", counts(s.pushed))
	}
	w("The seeded defects, and the layer each is for:\n\n")
	w("| Seed | Layer §12.2 names it under |\n|---|---|\n")
	w("| `%s`: the German source gains `{$amount}`; every locale but French follows it | `parity` |\n", keyArgument)
	w("| `%s`: the German source drops its `{#link}`; every locale but Japanese follows it | `parity` |\n", keyMarkup)
	w("| three French translations that were never written (`%s`) | `completeness` |\n", strings.Join(missingFrench, "`, `"))
	w("| two German sources that move under their Japanese (`%s`) | `completeness` |\n", strings.Join(outdatedJapanese, "`, `"))
	w("| `%s`: its message is obsoleted while its translations stay, and the help page still asks for it at `%s:%d` | `completeness` |\n",
		keyOrphan, orphanFile, orphanLine)
	w("| `%s`, used at `%s:%d` and in no catalog — Context's unknown key, which `glossa context push` counts | — |\n",
		keyUnknown, unknownFile, unknownLine)
	w("| `%s` (namespace `legal`), whose French uses the forbidden `%s` | `terminology` |\n", keyForbidden, termForbiddenFR)
	w("| `%s`, whose Spanish does not use the preferred `%s` | `terminology` |\n", keyTermMissing, termPreferredES)
	w("| `%s` with `max_length: %d` and a French translation over it | `length` (see below) |\n", keyMaxLength, maxLength)
	w("| the checkout pay button, 104 px wide and single-line, holding `%s` | `visual` |\n\n", keyButton)
	w("And the pull request §12.4's impact preview has to name: #%d (`%s`), opened and checked by its CI\n",
		greenPRNumber, greenBranch)
	w("while `main` still held the default branch's catalogs. Its newest recorded run passes under v%d; the one\n",
		s.gradedVersion)
	w("finding v4 would fail it on is the Japanese pay button, which clips on every branch.\n\n")
}

func (s *scenario) reportLayers(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## §12.2 — findings across layers\n\n")
	c := s.cliCheck
	w("`glossa check --terminology --explain-policy --json` graded the project against policy v%d from the %s\n",
		c.Policy.Version, c.Policy.Source)
	w("and exited **%s**: conclusion `%s`, **%d errors, %d warnings, %d waived** over %d active messages.\n\n",
		exitOf(c), c.Conclusion, c.Errors, c.Warnings, c.Waived, c.Messages)
	if len(c.Locales) > 0 {
		w("| Locale | Required | Translated | Missing | Outdated | Errors | Warnings | Complete |\n")
		w("|---|---|---:|---:|---:|---:|---:|---|\n")
		for _, l := range c.Locales {
			w("| `%s`%s | %s | %d | %d | %d | %d | %d | %s |\n", l.Code, sourceMark(l.IsSource),
				yesNo(l.Required), l.Translated, l.Missing, l.Outdated, l.Errors, l.Warnings, yesNo(l.Complete))
		}
		w("\n")
	}
	w("The layers the run computed: %s.", layerNames(c.Layers))
	if len(c.Unavailable) > 0 {
		w(" Layers it was asked for and could not compute:")
		for _, u := range c.Unavailable {
			w(" `%s` (%s)", u.Layer, u.Why)
		}
		w(".")
	}
	w("\n\n")

	w("### The nine cases §12.2 names\n\n")
	w("| Layer | What §12.2 asks for | What this run produced | Codes |\n|---|---|---|---|\n")
	for _, v := range s.layerStatus {
		mark := "✅"
		got := v.Got
		if !v.OK {
			mark = "❌"
			got = v.Why
		}
		w("| %s `%s` | %s | %s | %s |\n", mark, v.Layer, v.Want, got, codeList(v.Codes))
	}
	w("\n")

	w("**The `structure` layer** (§12.2 as amended in wave 7). `glossa check --offline` over a local catalog\n")
	w("holding text that does not parse produced **%d** structure finding(s) (%s): the layer works where its\n",
		s.structure.Offline, s.structure.OfflineCodes)
	w("input can exist. A server project cannot hold that input, and the test asserts why rather than saying\n")
	w("so — every write path parses before it stores:\n\n")
	if len(s.structure.Refusals) > 0 {
		w("| Write | Answer |\n|---|---|\n")
		for _, r := range s.structure.Refusals {
			w("| %s | %s, and the stored text is unchanged |\n", r.What, r.Then)
		}
		w("\n")
	}

	w("**The visual layer.** ")
	if s.crop.OK {
		w("`glossa capture --check` drove the headless Chrome this test started over `%s` in German and Japanese\n", s.crop.Route)
		w("at 1280×800, twice (the two-sighting rule of §5.2). The Japanese pay button clipped. The region the\n")
		w("finding names was read back through the API and the stored screenshot cropped to it:\n\n")
		w("| Message | Locale | Capture | Region | Box (CSS px) | Screenshot | Crop | Distinct colours |\n")
		w("|---|---|---|---|---|---|---|---:|\n")
		w("| `%s` | `%s` | `%s` | `%s` | %s | %d×%d (%d bytes) | %d×%d px | %d |\n\n",
			s.crop.Key, s.crop.Locale, short(s.crop.Capture), s.crop.Region, s.crop.Box,
			s.crop.ImageW, s.crop.ImageH, s.crop.ImageBytes, s.crop.CropW, s.crop.CropH, s.crop.Colours)
	} else {
		w("no region was cropped: %s.\n\n", orDash(s.crop.Why))
	}
	for _, note := range s.c("12.2").notes {
		w("- %s\n", note)
	}
	w("\n")
}

func (s *scenario) reportAgreement(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## §12.3 — the PR check agrees (the exit criterion)\n\n")
	w("Pull request #%d on `%s`, head `%s`, opened by a signed `pull_request.opened` webhook the fake GitHub's\n",
		prNumber, repositoryName, short(headCommit))
	w("fixtures sign. Nothing creates a check run through `/v1` — `openCheck` is reached only from webhook\n")
	w("processing — so the test does what a product does and assumes no API shortcut.\n\n")
	if s.prCheck.Name == "" {
		w("**No check run completed**, so there was nothing to compare the terminal's verdict with.\n\n")
		return
	}
	w("The check run: `%s`, %s/`%s`, %q, %d annotations over %d PATCHes, %d sticky comment.\n\n",
		s.prCheck.Name, s.prCheck.Status, s.prCheck.Conclusion, s.prCheck.Title,
		s.prCheck.Annotations, s.prCheck.Patches, s.stickyFound)
	w("The terminal's side is %s — the workflow's run that carries every layer the server can have.\n\n", s.agreedWith)
	w("| | the terminal | the pull request's check run | |\n|---|---|---|---|\n")
	for _, r := range s.agreeRows {
		mark := "✅"
		if !r.Agrees {
			mark = "❌"
		}
		w("| %s | %s | %s | %s |\n", r.What, r.CLI, r.PR, mark)
	}
	w("\n")
	if allAgree(s.agreeRows) {
		w("Two surfaces, one verdict.\n\n")
	} else {
		w("The two surfaces do not agree. That is the criterion M4 is decided by, and it is the row(s) marked ❌\n")
		w("above that decide it.\n\n")
	}
}

func (s *scenario) reportWaivers(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## §12.4 — waivers and the policy rollout\n\n")
	w("### The waiver\n\n")
	if len(s.waiverSteps) == 0 {
		w("Nothing was waived: %s\n\n", orDash(firstGap(s.c("12.4"))))
	} else {
		w("| What | What happened |\n|---|---|\n")
		for _, st := range s.waiverSteps {
			w("| %s | %s |\n", st.What, st.Then)
		}
		w("\n")
	}
	w("### The rollout\n\n")
	if len(s.policySteps) == 0 {
		w("The rollout did not run.\n\n")
	} else {
		w("| What | What happened |\n|---|---|\n")
		for _, st := range s.policySteps {
			w("| %s | %s |\n", st.What, st.Then)
		}
		w("\n")
	}
	for _, note := range s.c("12.4").notes {
		w("%s\n\n", note)
	}
}

func (s *scenario) reportRelease(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## §12.5 — the release gate\n\n")
	if len(s.releaseRows) == 0 {
		w("The gate was not exercised: %s\n\n", orDash(firstGap(s.c("12.5"))))
		return
	}
	w("| What | What happened |\n|---|---|\n")
	for _, st := range s.releaseRows {
		w("| %s | %s |\n", st.What, st.Then)
	}
	w("\n")
}

func (s *scenario) reportMCP(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## §12.6 — MCP\n\n")
	w("An `mcp-go` client (`github.com/modelcontextprotocol/go-sdk`) over streamable HTTP at `/mcp`, on the same\n")
	w("server, with `GLOSSA_MCP_ENABLED=true` — the endpoint is off by default and this deployment turns it on.\n\n")
	if len(s.mcpRows) == 0 {
		w("Nothing was exercised: %s\n\n", orDash(firstGap(s.c("12.6"))))
		return
	}
	w("| Credential | Session | Call | Outcome |\n|---|---|---|---|\n")
	for _, r := range s.mcpRows {
		w("| %s | %s | `%s` | %s |\n", r.Credential, r.Session, r.Tool, r.Outcome)
	}
	w("\nThe CI token above is a well-formed `glossa_ci_…` secret rather than one minted through the GitHub OIDC\n")
	w("exchange: the refusal is by credential kind in `mcp/adapters/identity.Authenticate`, before any lookup, so\n")
	w("that is the path being exercised. The in-context grant **is** a live one, minted through\n")
	w("`POST …/in-context-grants` for the fixture deployment's origin and refused all the same.\n\n")
}

func (s *scenario) reportFlutter(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	d := s.dart
	w("## §12.7 — the Flutter runtime\n\n")
	if !d.Ran {
		w("The Dart suites did not run: %s\n\n", orDash(d.Why))
		return
	}
	w("`%s`%s.\n\n", d.SDK, orEmpty(d.Flutter, " · "+d.Flutter))
	w("| Suite | Fixtures | Passed | Failed | Skipped |\n|---|---|---:|---:|---:|\n")
	for _, su := range d.Suites {
		w("| `%s` | %s | %d | %d | %d |\n", su.Name, orDash(su.Fixtures), su.Passed, su.Failed, su.Skipped)
	}
	w("| **Total** | | **%d** | **%d** | **%d** |\n\n", d.Passed, d.Failed, d.Skipped)
	w("`explain()` is asserted field for field against SPEC §6 inside `scenarios_test.dart` — locale, chain,\n")
	w("`resolvedFrom`, release id and version, source and every step — and the unsigned manifest is\n")
	w("`runtimes/testdata/loading/signatures.json`'s \"release 2 unsigned\" step, which runs with public keys\n")
	w("configured and expects a `signature` error and the previous release still serving.\n\n")

	w("### The §6.4 budgets\n\n")
	w("**Size** — `flutter build --analyze-size` against a fixture app with and without the package, which is\n")
	w("the method §6.4 names; gated on the delta over a realistic baseline at ≤ 400 kB (§15 question 6).\n\n")
	writeBudget(b, d.Size)
	w("**Startup** — measured and recorded, not gated on wall clock: §6.4's numbers are written for a mid-range\n")
	w("Android device and neither a laptop nor a CI runner is one. What the tool does enforce are the two\n")
	w("properties that hold on any machine.\n\n")
	writeBudget(b, d.Startup)
}

func writeBudget(b *bytes.Buffer, bu budget) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	if !bu.Ran {
		w("> Not measured here: %s\n\n", orDash(bu.Why))
		return
	}
	w("```\n")
	for _, l := range bu.Lines {
		w("%s\n", l)
	}
	w("```\n\n")
	if bu.OK {
		w("Measured in %.0f s on this machine.\n\n", bu.Seconds)
	} else {
		w("**Over budget**: %s (measured in %.0f s).\n\n", bu.Why, bu.Seconds)
	}
}

func (s *scenario) reportDashboard(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## §12.8 — the dashboard\n\n")
	w("| Number (RFC 0005 §8) | What the API reported |\n|---|---|\n")
	for _, n := range s.studio.Numbers {
		v := n.Value
		if !n.Measured && n.Reason != "" {
			v = "not measured — " + n.Reason
		}
		w("| %s | %s |\n", n.Name, v)
	}
	w("\n")
	switch {
	case !s.studio.Ran:
		w("The Playwright spec did not run: %s\n\n", orDash(s.studio.Why))
	case s.studio.Passed:
		w("`studio/%s` signed in against **this** server, opened `/t/…/p/…/quality`, found seven stats in the\n", s.studio.Spec)
		w("health header, and asserted each rendered value against the summary above — computing the expected\n")
		w("rendering from the API's JSON with plain `Intl` rather than by calling Studio's own formatters, so the\n")
		w("comparison cannot agree with itself. A number the API did not measure has to read \"Not measured\" and\n")
		w("carry `data-measured=\"false\"`; a zero there would be a claim the API never made. (%.0f s)\n\n", s.studio.Seconds)
	default:
		w("The Playwright spec failed:\n\n```\n%s\n```\n\n", s.studio.Output)
	}
}

func (s *scenario) reportNotes(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## No provider, no network\n\n")
	w("RFC 0005 §14 decision 2: a check never calls an AI provider, and none of §12's nine layers is the\n")
	w("model-backed one. The tenant's only configured provider is a loopback endpoint that refuses every\n")
	w("request and counts it. **It was asked %d times.**", s.guardCalls)
	if s.guardCalls > 0 {
		w(" That is not zero: %v.", s.guardPaths)
	}
	w("\n\nThe GitHub is the fake of RFC 0004 §12, on loopback; Postgres and MinIO are testcontainers; the browser\n")
	w("is a headless Chrome the test starts and attaches to over CDP. Nothing in this run reaches the network.\n")
}

func exitOf(c checkJSON) string {
	if c.Passed {
		return "0"
	}
	return "1"
}

func counts(m map[string]int) string {
	if len(m) == 0 {
		return "nothing"
	}
	var parts []string
	for _, k := range sortedKeys(m) {
		parts = append(parts, fmt.Sprintf("%d %s", m[k], k))
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(m map[string]int) []string {
	out := keysOf(m)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func firstGap(c *criterion) string {
	if len(c.gaps) == 0 {
		return ""
	}
	return c.gaps[0]
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func sourceMark(isSource bool) string {
	if isSource {
		return " _(source)_"
	}
	return ""
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func orEmpty(cond, s string) string {
	if cond == "" {
		return ""
	}
	return s
}

var _ = domain.LayerVisual
