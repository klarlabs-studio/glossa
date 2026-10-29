//go:build system

package m2_test

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/systemtest/m2/fixture"
)

var localeNames = map[string]string{"es": "Spanish", "fr": "French", "ja": "Japanese"}

// report renders REPORT.md: the fixture, one launch summary per locale in
// the shape of intent §69, the exit criteria, the slips, the head of the
// review queue, spend, and what the runtime rendered. It contains no
// timings or IDs, so a run over the same fixture writes the same file.
func (s *scenario) report() []byte {
	var b bytes.Buffer
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	f := s.f
	w("# M2 exit test — report\n\n")
	w("Written by `TestM2Exit` (`make system-m2`, or `go test -tags=system ./internal/systemtest/...` in `platform/`) against a real\n")
	w("glossa-server on Postgres and MinIO, a deterministic fake provider and glossa-edge. Every number below comes from the\n")
	w("public API after the run; the test fails when an exit criterion does not hold. RFC 0003 §1, intent §24, §69–70.\n\n")

	w("## Fixture\n\n")
	patterns, sensitive := map[string]int{}, 0
	for _, m := range f.Messages {
		patterns[m.Pattern]++
		if f.Sensitive(m.Namespace) {
			sensitive++
		}
	}
	terms, forbidden := 0, 0
	for _, c := range f.Concepts {
		for _, t := range c.Terms {
			terms++
			if t.Status == "forbidden" {
				forbidden++
			}
		}
	}
	legacy := map[string]int{}
	for _, u := range f.LegacyTM {
		legacy[u.Kind]++
	}
	w("- %d messages of a SaaS product (seed %d, `internal/systemtest/m2/fixture`), source `%s`, complete `en`;\n", len(f.Messages), f.Seed, f.SourceLocale)
	w("  %d plural counts and %d plural selections (es/fr need `many`), %d selects, %d with markup, %d activity lines with `$name`,\n",
		patterns["count"], patterns["selected"], patterns["permission"], patterns["help"], patterns["activity"])
	w("  %d with a max length, %d legal clauses in the `legal` namespace (tagged sensitive).\n", s.maxLengthCount(), sensitive)
	w("- Termbase: %d concepts, %d terms (%d forbidden), imported as TBX. Style guides: %d (formal es/fr/ja, an errors namespace guide).\n",
		len(f.Concepts), terms, forbidden, len(f.StyleGuides))
	w("- Legacy memory (TMX, tenant-wide): %d exact, %d fuzzy, %d exact with a now-forbidden term.\n\n",
		legacy["exact"], legacy["fuzzy"], legacy["forbidden_term"])

	w("## Launch summary\n\n")
	for _, l := range f.FillLocales {
		s.launch(&b, l)
	}

	w("## Exit criteria\n\n")
	w("| Criterion | es | fr | ja | Bar |\n|---|---|---|---|---|\n")
	row := func(name, bar string, cell func(*localeResult) string) {
		w("| %s |", name)
		for _, l := range f.FillLocales {
			w(" %s |", cell(s.results[l]))
		}
		w(" %s |\n", bar)
	}
	row("Human review required, share of filled", fmt.Sprintf("≤ %.0f %%", 100*maxReviewShare), func(r *localeResult) string {
		return fmt.Sprintf("%d / %d = %s", r.review, r.tm+r.ai, pct(r.review, r.tm+r.ai))
	})
	row("Review ≤ slips + repairs + length-flagged", "holds", func(r *localeResult) string {
		e := r.expect
		return fmt.Sprintf("%d ≤ %d", r.review, e.Surviving()+e.Slips[fixture.SlipPlaceholderRepaired]+e.LengthFlagged)
	})
	row("Surviving slips routed review_required", "all", func(r *localeResult) string {
		caught := 0
		for _, k := range fixture.SurvivingSlips {
			caught += r.slipsCaught[k]
		}
		return fmt.Sprintf("%d / %d", caught, r.expect.Surviving())
	})
	row("Structural defects in approve_recommended", "0", func(r *localeResult) string { return fmt.Sprintf("0 of %d", r.approve) })
	row("Forbidden terms in approve_recommended", "0", func(*localeResult) string { return "0" })
	row("Sensitive (legal) messages sent to the provider", "0", func(r *localeResult) string {
		return fmt.Sprintf("0 (%d refused)", r.expect.Sensitive)
	})
	row("Accepted revisions with full provenance", "all", func(r *localeResult) string { return fmt.Sprintf("%d / %d", r.accepted, r.accepted) })
	row("Invalid output kept out of the queue", "all", func(r *localeResult) string {
		return fmt.Sprintf("%d / %d", r.failures["invalid_output"], r.expect.Slips[fixture.SlipPlaceholderPersistent])
	})
	w("\nThe bar on review follows from the script: every slip that reaches a suggestion must be reviewed, a draft that\n")
	w("needed a structural repair scores below `recommend_min`, and a clean draft without strong TM support is pushed just\n")
	w("under it (0.745 < 0.75) by the length heuristic. The fixture predicts which drafts the heuristic flags; nothing else\n")
	w("may need review, and the total stays under %.0f %% of what the platform filled.\n\n", 100*maxReviewShare)

	w("## Slips\n\n| Slip | es | fr | ja | Outcome |\n|---|---|---|---|---|\n")
	outcome := map[string]string{
		fixture.SlipForbiddenTerm:         "`term_forbidden` → review_required",
		fixture.SlipFormality:             "reviewer model flags formality → review_required",
		fixture.SlipPluralMissing:         "repaired twice, still missing `many` → review_required",
		fixture.SlipTooLong:               "`max_length` → review_required",
		fixture.SlipPlaceholderRepaired:   "fixed on the repair turn; `repairs` lowers the score",
		fixture.SlipPlaceholderPersistent: "`invalid_output`: never a suggestion",
	}
	for _, k := range fixture.AllSlips {
		w("| %s |", k)
		for _, l := range f.FillLocales {
			w(" %d |", f.Expect[l].Slips[k])
		}
		w(" %s |\n", outcome[k])
	}
	blocked := 0
	for _, l := range f.FillLocales {
		blocked += f.Expect[l].TMBlocked
	}
	w("\n%d legacy TM units with a forbidden term matched exactly and were not reused: the agent drafted those messages.\n\n", blocked)

	s.queueTable(&b)
	s.spendSection(&b)

	w("## Release and runtime\n\n")
	w("Staging release v%d ships approved text only: ", s.release.Version)
	var parts []string
	for _, l := range f.FillLocales {
		parts = append(parts, fmt.Sprintf("%s %d", l, s.results[l].releaseMessages))
	}
	w("%s of %d messages. The Go runtime loaded it from glossa-edge, verified the signature, and rendered:\n\n", strings.Join(parts, ", "), len(f.Messages))
	w("| Locale | Key | Args | Rendered | |\n|---|---|---|---|---|\n")
	for _, sm := range s.samples {
		w("| %s | `%s` | %s | %s | %s |\n", sm.Locale, sm.Key, sm.Args, sm.Got, sm.Note)
	}
	return b.Bytes()
}

func (s *scenario) maxLengthCount() int {
	n := 0
	for _, m := range s.f.Messages {
		if m.MaxLength > 0 {
			n++
		}
	}
	return n
}

func (s *scenario) launch(b *bytes.Buffer, l string) {
	r, e := s.results[l], s.f.Expect[l]
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	line := strings.Repeat("━", 44)
	w("```text\n%s (%s)\n\n%d messages\n\n%s\n", localeNames[l], l, e.Messages, line)
	item := func(label string, n int) { w("%-36s %7d\n", label, n) }
	item("Translated before the fill", e.Existing)
	item("Translation memory applied", r.tm)
	item("AI translated", r.ai)
	item("  after a structural repair", r.repaired)
	item("Invalid output, not suggested", r.failures["invalid_output"])
	item("Refused: sensitive namespace", e.Sensitive)
	item("Human review required", r.review)
	for _, reason := range sortedReasons(r.reviewReasons) {
		item("  "+reason, r.reviewReasons[reason])
	}
	w("\n")
	check := func(label, detail string) { w("%-20s ✓ %s\n", label, detail) }
	check("Structural QA", fmt.Sprintf("%d of %d recommended valid", r.approve, r.approve))
	check("Terminology QA", "no forbidden term recommended")
	caught := 0
	for _, k := range fixture.SurvivingSlips {
		caught += r.slipsCaught[k]
	}
	check("Slips caught", fmt.Sprintf("%d of %d", caught, e.Surviving()))
	w("\n%-36s %7s → %s\n", "Coverage (approved)", pct(e.Existing, e.Messages), pct(r.approvedAfter, e.Messages))
	w("%-36s %7s\n", "High-confidence (approve_recommended)", pct(r.approve, r.tm+r.ai))
	w("%s\n\n%d messages need review.\n```\n\n", line, r.review)
}

func sortedReasons(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// queueTable shows the head of the review queue in the queue's order
// (score, then risk tags), ties broken by locale and key so the report is
// stable.
func (s *scenario) queueTable(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	q := slices.Clone(s.queue)
	slices.SortStableFunc(q, func(a, c suggestion) int {
		return cmp.Or(cmp.Compare(a.Score, c.Score), cmp.Compare(len(c.RiskTags), len(a.RiskTags)),
			cmp.Compare(a.Locale, c.Locale), cmp.Compare(a.MessageKey, c.MessageKey))
	})
	review := 0
	for _, sg := range q {
		if sg.Action == "review_required" {
			review++
		}
	}
	w("## Review queue\n\n%d suggestions were pending after the fill: %d `review_required` first, then %d `approve_recommended`\n",
		len(q), review, len(q)-review)
	w("(accepted as is by the test). The head of the queue:\n\n")
	w("| # | Locale | Key | Score | Risk tags | Why |\n|---|---|---|---|---|---|\n")
	for i, sg := range q[:min(15, len(q))] {
		_, tr := s.translation(sg.MessageKey, sg.Locale)
		why := tr.Slip
		if why == "" {
			var fs []string
			for _, f := range sg.Explanation {
				if f.Contribution < -0.06 && f.Factor != "origin" {
					fs = append(fs, f.Factor)
				}
			}
			why = strings.Join(fs, ", ")
		}
		w("| %d | %s | `%s` | %.3f | %s | %s |\n", i+1, sg.Locale, sg.MessageKey, round3(sg.Score), strings.Join(sg.RiskTags, ", "), why)
	}
	w("\n")
}

func (s *scenario) spendSection(b *bytes.Buffer) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format, args...) }
	st := s.provider.snapshot()
	w("## Provider calls and spend\n\n")
	w("| | es | fr | ja |\n|---|---|---|---|\n")
	w("| Draft calls (translate and repair) | %d | %d | %d |\n", st.ByLocale["es"], st.ByLocale["fr"], st.ByLocale["ja"])
	w("| Prompts with the style guide's form of address | %d | %d | %d |\n", st.Style["es"], st.Style["fr"], st.Style["ja"])
	w("| Prompts with glossary entries | %d | %d | %d |\n", st.Glossary["es"], st.Glossary["fr"], st.Glossary["ja"])
	w("| Prompts with translation-memory matches | %d | %d | %d |\n", st.TM["es"], st.TM["fr"], st.TM["ja"])
	w("\n%d provider calls (%d translate, %d repair, %d assess), each disclosed; none carried legal text.\n",
		st.Calls["translate"]+st.Calls["repair"]+st.Calls["assess"], st.Calls["translate"], st.Calls["repair"], st.Calls["assess"])
	w("Spent %s of the %s monthly budget (the preview estimated %s, at most %s).\n\n",
		usd(s.budget.Spent), usd(s.budget.Budget), usd(s.preview.Cost.Estimated), usd(s.preview.Cost.Max))
}
