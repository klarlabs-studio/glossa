//go:build system

package m5_test

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// failsIf is how each criterion can fail, from RFC 0006 §12. The report
// prints it beside the verdict so a reader knows what green would mean.
var failsIf = map[string]string{
	"12.1": "B needs a code path A doesn't; an instance strands short of a final state; the author's or a token's grant counts; Workflow's principal approves anything.",
	"12.2": "any generated operation leaks an id outside the assignment, or an operation in the spec has no verdict in the coverage table.",
	"12.3": "any pointer moves before the second approval as seen at the edge, or a rollback waits.",
	"12.4": "any runtime disagrees with the generator on any id, the share is outside 9–11 %, or an aborted installation stays on the candidate. Runtimes are compared with the generator, never with each other.",
	"12.5": "a call the harness recorded has no entry (compared with the harness's own log, not the outbox), an entry has the wrong actor, a tampered export verifies, or a canary leaks.",
	"12.6": "any rendering differs between v0.3's formatter and @klarlabs-studio/glossa (two implementations that share no code) other than by v0.3's known apostrophe defect, which is reported with its count and every row, or a carried field is missing.",
	"12.7": "any earlier exit criterion fails. A failure here blocks the M5 verdict whatever 12.1–12.6 say.",
}

// report renders REPORT.md: the seven criteria of RFC 0006 §12, each
// with the steps it took and, where it does not hold, exactly what is
// missing. It is written whether or not the criteria hold — in wave 1,
// precisely because they don't.
func (s *scenario) report() []byte {
	var b bytes.Buffer
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	met := 0
	for _, c := range s.criteria {
		if c.met() {
			met++
		}
	}

	w("# M5 exit test — report\n\n")
	w("Written by `TestM5Exit` (`make system-m5`, or `go test -tags=system ./internal/systemtest/m5/...` in\n")
	w("`platform/`) against a real glossa-server on Postgres and MinIO that sends its mail over SMTP to a\n")
	w("capturing test mailer, a real glossa-edge on the same bucket, a v0.3 server built from `apps/api` on its\n")
	w("own Postgres, and the JS, Go and Dart runtimes. Every observation below comes from the public API, the\n")
	w("edge, a runtime or the CLI. RFC 0006 §12.\n\n")
	w("This test exists from M5's first wave with every criterion written and red (RFC 0006 §11.1): each later\n")
	w("wave turns some of it green, and its merge note says which. A criterion that cannot hold says what is\n")
	w("missing; the steps after the first missing one are listed as *not reached*.\n\n")

	w("## The verdict\n\n")
	w("**%d of the 7 exit criteria hold.**\n\n", met)
	w("| § | Criterion | Verdict | Fails if |\n|---|---|---|---|\n")
	for _, c := range s.criteria {
		w("| %s | %s | %s | %s |\n", c.id, c.title, c.verdict(), failsIf[c.id])
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
			w("- **§%s**: %s\n", c.id, oneLine(c.gaps[0]))
			for _, g := range c.gaps[1:] {
				w("  - %s\n", oneLine(g))
			}
		}
		w("\n")
	}

	s.reportFixture(&b)
	for _, c := range s.criteria {
		w("## §%s — %s\n\n", c.id, strings.ToLower(c.title[:1])+c.title[1:])
		if len(c.steps) > 0 {
			w("| | Step | What happened |\n|---|---|---|\n")
			for _, st := range c.steps {
				mark := map[string]string{held: "✅", failed: "❌", notReached: "·"}[st.State]
				detail := st.Detail
				if st.State == notReached {
					detail = "_not reached_"
				} else if detail == "" {
					detail = "held"
				}
				w("| %s | %s | %s |\n", mark, st.Name, oneLine(detail))
			}
			w("\n")
		}
		for _, n := range c.notes {
			w("%s\n\n", n)
		}
		switch c.id {
		case "12.1":
			s.reportWorkflow(&b)
		case "12.2":
			s.reportSweep(&b)
		case "12.3":
			s.reportEdge(&b)
		case "12.4":
			s.reportRollout(&b)
		case "12.5":
			s.reportAudit(&b)
		case "12.6":
			s.reportV03(&b)
		case "12.7":
			s.reportEarlier(&b)
		}
	}

	s.reportCannotProve(&b)
	s.reportFlakeReview(&b)
	return b.Bytes()
}

// cannotProve is RFC 0006 §12's list, then what this harness itself
// fakes. Each item says what the test stands in for, so a green report
// is not read as more than it is.
var cannotProve = []struct{ claim, why string }{
	{"Production rendering in real products (RFC 0006 §1.3, §12).",
		"The renderings compared in §12.6 are of a generated fixture of 300 keys, not of any Klarlabs product's strings, locales or arguments."},
	{"A real vendor can be onboarded (§12).",
		"The invitation, verification link and sign-in travel over SMTP to a capturing **test mailer** the harness runs. No real mail provider, spam filter, link rewriting or delay was involved."},
	{"Real users land in rollout cohorts in the proportions the fixture shows (§12).",
		"§12.4 drives the runtimes with 10,000 installation ids from a fixture through a **fake transport** over the edge's real manifest. Real installation ids, real refresh timing and real network failure were not."},
	{"v0.3's namespace can be deleted without someone noticing (§12).",
		"§12.6 imports a **seeded** v0.3 server built from `apps/api` on its own Postgres. Nothing here shuts a production v0.3 down or finds who still calls it."},
	{"Anything a real GitHub does.",
		"The Git connection of §12.2 talks to a **fake GitHub** on loopback: no App installation, webhook signature, rate limit or permission model of the real one."},
	{"Anything a real AI provider does.",
		"The AI fill of §12.2 is drafted by a **fake provider** on loopback. Quality, latency, errors and cost of a real model are untested."},
	{"Studio in a browser.",
		"This test reads the public API, the edge, the runtimes and the CLI. Studio's own end-to-end tests are a separate suite; nothing here shows a user can reach any of this through the UI."},
	{"That the access surface is closed beyond what the spec and the MCP tool list say.",
		"§12.2's sweep generates **GET** operations from `platform/api/openapi.yaml` and calls them with fixture ids; writes are checked by a fixed list. A leak through a route that is not in the spec, through an id the fixture did not make, or by timing is invisible to it. A tool that needs an argument the fixture cannot fill is marked ∅ and fails the criterion."},
	{"The audit export is complete for calls the harness did not make.",
		"§12.5 compares the export with the harness's own log of the calls *it* made in §12.1–§12.4. Entries for system actions, other callers or calls the harness did not record are not checked against anything."},
	{"Behaviour under load, partial failure, clock skew or restarts.",
		"One server, one edge, one Postgres and one bucket, started once on loopback, with no faults injected."},
	{"The Dart, Go and JS runtimes on real devices.",
		"They are run as host processes against a fake transport; no mobile OS, app lifecycle or storage is involved."},
}

func (s *scenario) reportCannotProve(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## What this test cannot prove\n\n")
	w("The first four are RFC 0006 §12's list; the rest are what this harness fakes or does not look at. All of\n")
	w("it is the dogfood phase's to prove (§7.4), or another suite's.\n\n")
	for _, c := range cannotProve {
		w("- **%s** %s\n", c.claim, c.why)
	}
	// What this particular run could not do, from the run itself.
	if !s.vendorAsVendor {
		w("- **In this run, a vendor member.** The platform could not make the translator a vendor member, so §12.2 shows what an ordinary `de` translator sees, not a vendor.\n")
	}
	for _, c := range s.criteria {
		if c.skipped != "" {
			w("- **In this run, §%s.** It was not run: %s\n", c.id, oneLine(c.skipped))
		}
	}
	w("\n")
}

func (s *scenario) reportFlakeReview(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## Flake review\n\n")
	w("What the harness does about timing and shared state, so a red run can be told from a flaky one:\n\n")
	w("- **Positive waits** (a pointer arrives, a state is reached, a service is ready) are polls with a stated deadline\n")
	w("  (`softly`, `edgeServes`): 10–90 s, the last observed state in the failure. No positive claim rests on a fixed sleep.\n")
	w("- **Negative claims** (the edge has *not* moved, the translation is *not yet* approved) are watched for their whole\n")
	w("  window by `staysFor`, which fails on the first violation and treats a check it cannot make as a failure, not a pass.\n")
	w("  The window (3 s) must cover the outbox and the edge's refresh; a window too short could only make such a step pass\n")
	w("  wrongly, never fail, so it is the one place a wait is a lower bound on rigour. It is never the only evidence: each\n")
	w("  is followed by the positive step that the next approval does move it.\n")
	w("- **No retries.** No step is repeated until it passes; an HTTP call that fails once fails the step.\n")
	w("- **Order.** §12.1–§12.4 share one server, one fixture and one `production` environment and run in that order;\n")
	w("  §12.4 starts from the stable release §12.3 leaves, and §12.5 compares the export with the calls of §12.1–§12.4.\n")
	w("  Each criterion records its own gaps, so a failure in one does not stop the next, but a later one that needs an\n")
	w("  earlier one's state reports *not reached* with the reason. §12.7 runs first and alone, because the earlier exit\n")
	w("  tests collide at sign-in with a second running server.\n")
	w("- **Known platform-side flake, not fixed here.** In M3's PR-check queue, `integration_github_checks.available_at`\n")
	w("  is set with the application clock but claimed with Postgres `now()`; with a skewed clock a check can be claimed\n")
	w("  early or late. M5's harness does not touch that queue (§12.2 creates a check *run* through the API, not a pull-request\n")
	w("  check), but §12.7 runs M3's exit test, so a red §12.7 naming a PR check is this, not M5.\n\n")
}

func (s *scenario) reportFixture(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	w("## The fixture\n\n")
	w("- **An organisation of three people and a vendor's translator**, all signed in through mail the server\n")
	w("  delivered over SMTP (`GLOSSA_MAIL_DRIVER=smtp`, with credentials) to the test mailer: the owner\n")
	w("  (`%s`), two `de` reviewers (`%s`, `%s`), and the vendor's translator (`%s`), who registers\n", ownerEmail, reviewer1Email, reviewer2Email, vendorEmail)
	w("  with a password, follows the verification link the mailer received, and so accepts the invitation.\n")
	if s.d != nil {
		w("  The test mailer accepted %d messages and refused %d unauthenticated sessions.\n", s.d.mail.count(), s.d.mail.refusedCount())
	}
	w("- **Project A** (`ledger`) and **project B** (`portal`), source `en`, target `de`, %d messages each plus\n", messagesPerProject)
	w("  `%s`, which both hold and §12.1 revises in both. Project B's first %d `de` units are the vendor's\n", sharedKey, assignedUnits)
	w("  assignment (§12.2); the rest, and all of project A, are outside it.\n")
	if s.vendorAsVendor {
		w("- The vendor's translator is a vendor member with visibility `assigned`, scoped to project B.\n")
	} else {
		w("- The platform could not make the vendor's translator a vendor member (§12.2), so they were invited as an\n")
		w("  ordinary `de` translator: the sweep shows what such a member can read today.\n")
	}
	w("- **Something to address in each project** (§12.2), made through the API as the owner: an application,\n")
	w("  a check policy, a staging release and one of its artifacts, an import job, a Git connection (one\n")
	w("  repository of a fake GitHub, under a path per project), a branch push, a capture upload, a check run,\n")
	w("  a linguistic job, and an AI fill of `%s` / `%s` with its job and suggestion, drafted by a fake\n", unitKey("a", sweepUnit), unitKey("b", sweepUnit))
	w("  provider on loopback. Project B's are the sweep's ids inside the assignment, project A's outside it.\n")
	w("- **Canaries**, words that exist only in this fixture's text, in source and translation text of both projects:\n")
	w("  `%s`. §12.5 fails if any of them reaches the audit export.\n", strings.Join(canaries, "`, `"))
	w("- **A v0.3 server built from `apps/api`** (§12.6), migrated with its own migrations, seeded through its own API.\n\n")
}

func (s *scenario) reportWorkflow(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	if s.archTest != "" {
		w("§2.1's architecture test: %s\n\n", oneLine(lastLines(s.archTest, 3)))
	}
	if len(s.workflowLog) > 0 {
		w("The transition logs:\n\n```text\n%s\n```\n\n", strings.Join(s.workflowLog, "\n"))
	}
}

func (s *scenario) reportSweep(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	if len(s.sweep) == 0 {
		return
	}
	ok, leaks, unexercised := 0, 0, 0
	for _, r := range s.sweep {
		switch {
		case r.OK:
			ok++
		case r.Unexercised != "":
			unexercised++
		default:
			leaks++
		}
	}
	w("### The coverage table\n\n")
	w("Every GET operation of `platform/api/openapi.yaml`, called as the vendor's translator — inside the\n")
	w("assignment (project B, an assigned unit) and, where the operation is addressed by a project or a\n")
	w("message, outside it (project A, an unassigned unit, which must answer 404). **%d hold, %d show something\n", ok, leaks)
	w("outside the assignment or answer undocumented, %d have no verdict** (no fixture id to address them).\n", unexercised)
	w("An operation of the spec with no row at all, or a row marked ∅, fails §12.2.\n\n")
	w("| | Operation | Inside | Outside | Why |\n|---|---|---|---|---|\n")
	for _, r := range s.sweep {
		mark, why := "✅", r.Why
		switch {
		case r.Unexercised != "":
			mark, why = "∅", r.Unexercised
		case !r.OK:
			mark = "❌"
		}
		w("| %s | `%s` | %s | %s | %s |\n", mark, r.Operation, orDash(r.Inside), orDash(r.Outside), oneLine(why))
	}
	w("\n")
	if len(s.mcpSweep) > 0 {
		w("The MCP read tools, as the same member:\n\n| | Tool | Answer | Why |\n|---|---|---|---|\n")
		for _, r := range s.mcpSweep {
			mark, why := "✅", r.Why
			switch {
			case r.Unexercised != "":
				mark, why = "∅", r.Unexercised
			case !r.OK:
				mark = "❌"
			}
			w("| %s | `%s` | %s | %s |\n", mark, r.Operation, orDash(r.Inside), oneLine(why))
		}
		w("\n")
	}
}

func (s *scenario) reportEdge(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	if len(s.edgeRows) == 0 {
		return
	}
	w("What `glossa-edge` served for project B's `production`, read over HTTP from the edge process:\n\n")
	w("| | When | The edge served |\n|---|---|---|\n")
	for _, r := range s.edgeRows {
		mark := "✅"
		if !r.OK {
			mark = "❌"
		}
		w("| %s | %s | %s |\n", mark, r.When, r.Served)
	}
	w("\n")
}

func (s *scenario) reportRollout(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	if s.rollout.IDs > 0 {
		w("The generator's table (`%s`): %d installation ids; under its salt `%s`, %d are in the candidate at 10 %%.\n",
			cohortTablePath, s.rollout.IDs, s.rollout.TableSalt, s.rollout.ExpAt10)
		if s.rollout.ManifestSalt != "" {
			w("The edge's manifest carried salt `%s`; the expected cohorts under it come from `generate.py`.\n", s.rollout.ManifestSalt)
		}
		w("\n")
	}
	if len(s.rollout.Rows) == 0 {
		return
	}
	w("| | Phase | Runtime | In the candidate | Disagree with the generator | Note |\n|---|---|---|---:|---:|---|\n")
	for _, r := range s.rollout.Rows {
		mark := "✅"
		if !r.OK {
			mark = "❌"
		}
		note := r.FirstDisagree
		if r.Err != "" {
			note = r.Err
		}
		w("| %s | %s | %s | %d | %d | %s |\n", mark, r.Phase, r.Runtime, r.Candidates, r.Disagree, oneLine(note))
	}
	w("\n")
}

func (s *scenario) reportAudit(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	if s.audit.Entries > 0 {
		w("The export held %d entries.\n\n", s.audit.Entries)
	}
	if len(s.audit.Unmatched) > 0 {
		w("Recorded calls with no entry (first 20):\n\n")
		for _, u := range first(s.audit.Unmatched, 20) {
			w("- %s\n", u)
		}
		w("\n")
	}
	if s.audit.Recorded != nil {
		var ids []string
		for k := range s.audit.Recorded {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		w("| § | Successful mutating calls the harness recorded |\n|---|---:|\n")
		for _, k := range ids {
			w("| %s | %d |\n", k, s.audit.Recorded[k])
		}
		w("\n")
	}
}

func (s *scenario) reportV03(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	if s.v03.Renderings > 0 {
		w("Imported by %s; **%d renderings** of %d keys in de/en/es: **%d differ only by v0.3's known apostrophe defect**, "+
			"**%d differ otherwise**.\n\n", s.v03.Mode, s.v03.Renderings, s.v03.Keys, len(s.v03.KnownDefects), len(s.v03.Mismatches))
	}
	table := func(title string, rows []renderRow) {
		if len(rows) == 0 {
			return
		}
		w("%s\n\n| Key | Locale | Arguments | v0.3's formatter | @klarlabs-studio/glossa |\n|---|---|---|---|---|\n", title)
		for i, m := range rows {
			if i == 40 {
				w("| … | | | %d more | |\n", len(rows)-40)
				break
			}
			w("| `%s` | %s | `%v` | %s | %s |\n", m.Key, m.Locale, m.Args,
				oneLine(quoteOr(m.V0, m.V0Error)), oneLine(quoteOr(m.Runtime, m.RuntimeError)))
		}
		w("\n")
	}
	table("Known v0.3 defect (`v0_bare_apostrophe`): v0.3's formatter reads a bare apostrophe as opening a quoted run. "+
		"Each row is in this category only because v0.3's own formatter, given the same text with its apostrophes requoted "+
		"the ICU way, renders exactly the runtime's output.", s.v03.KnownDefects)
	table("Every other difference:", s.v03.Mismatches)
}

func (s *scenario) reportEarlier(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	for _, e := range s.earlier {
		verdict := "**passed**"
		if !e.Pass {
			verdict = "**failed** (" + e.Err + ")"
		}
		w("### %s — %s in %.0fs\n\n", e.Name, verdict, e.Took.Seconds())
		if len(e.Verdicts) > 0 {
			w("```text\n%s\n```\n\n", strings.Join(e.Verdicts, "\n"))
		}
	}
}

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
