package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa findings` (RFC 0005 §13 wave 6): the findings the server
// stored, with §2.1's filters.
//
// The command consumes the Quality API and computes nothing. That is
// the point of it: `glossa check` runs the deterministic layers here
// and can say what they find, but the layers that need a capture, a
// termbase or a provider ran on the server, and their findings are
// stored (§2.2 rule 3). A CLI that recomputed what it could and printed
// a smaller truth would be the seventh shape RFC 0005 §14 decision 1
// exists to prevent.
//
// It is also where a fingerprint comes from: the human output prints
// one per finding, because it is the argument `glossa waive` takes.

// findingsSchema is the command's document.
const findingsSchema = "glossa.cli.findings/v1"

// maxFindingsListed caps what --limit defaults to. A terminal is not a
// dashboard; --limit 0 reads every page.
const maxFindingsListed = 100

// findingsRunJSON is the check run the findings came from. Naming it is
// not decoration: a finding is only as current as the run that found
// it, and "there are no errors" from a run of three layers is a
// different sentence from the same words about a run of nine.
type findingsRunJSON struct {
	ID            string         `json:"id"`
	Ref           string         `json:"ref"`
	Commit        string         `json:"commit,omitempty"`
	Trigger       string         `json:"trigger"`
	Conclusion    string         `json:"conclusion,omitempty"`
	PolicyVersion int            `json:"policy_version"`
	Layers        []domain.Layer `json:"layers"`
	StartedAt     string         `json:"started_at"`
}

type countsJSON struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Waived   int `json:"waived"`
}

type findingsJSON struct {
	Schema string `json:"schema"`
	// Run is null when the project has never been checked, which is not
	// the same answer as a run that found nothing.
	Run *findingsRunJSON `json:"run"`
	// Counts are the whole run's, with today's waivers applied, whatever
	// the filters selected.
	Counts countsJSON `json:"counts"`
	// Findings are glossa.finding/v1 findings, the shape `glossa check`,
	// the pull request and Studio all report.
	Findings []domain.Finding `json:"findings"`
	// Truncated says --limit stopped the read short of what the server
	// holds.
	Truncated bool `json:"truncated"`
}

const findingsUsage = `findings [filters]

Filters (RFC 0005 §2.1), all optional:
  --layer <name>        structure, parity, completeness, terminology, style,
                        length, locale, source, visual, linguistic
  --severity <s>        error, warning or waived
  --code <code>         the rule, exactly (missing-argument, term_forbidden)
  --locale <l>          the locale the finding is about
  --namespace <ns>      the message's namespace
  --message <key>       the message key, exactly
  --waived[=false]      only the accepted findings, or only those no waiver accepts
Which run:
  --branch <ref>        the newest run of this branch or environment (default: the newest run)
  --commit <sha>        the newest run of this commit
  --run <id>            exactly this run
  --limit <n>           read at most n findings (default 100; 0 reads them all)`

type findingsFlags struct {
	filter remote.FindingFilter
	limit  int
}

func runFindings(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags(findingsUsage)
	var (
		layer     = fs.String("layer", "", "only this QA layer")
		severity  = fs.String("severity", "", "error, warning or waived")
		code      = fs.String("code", "", "the finding's code, exactly")
		locale    = fs.String("locale", "", "the locale the finding is about")
		namespace = fs.String("namespace", "", "the message's namespace")
		key       = fs.String("message", "", "the message key, exactly")
		waived    = fs.Bool("waived", false, "only the accepted findings (--waived=false: only those no waiver accepts)")
		branch    = fs.String("branch", "", "the newest run of this branch or environment")
		commit    = fs.String("commit", "", "the newest run of this commit")
		run       = fs.String("run", "", "the check run to read, by ID")
		limit     = fs.Int("limit", maxFindingsListed, "read at most this many findings (0: all of them)")
	)
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if err := noMore(inv, pos); err != nil {
		return err
	}
	f := findingsFlags{limit: *limit, filter: remote.FindingFilter{
		Run: *run, Ref: *branch, Commit: *commit, Code: *code,
		Namespace: *namespace, Key: *key,
	}}
	if *layer != "" {
		if !domain.Layer(*layer).Valid() {
			return usageError(inv.name, "%q is not a QA layer; the layers are %s", *layer, strings.Join(layerNames(), ", "))
		}
		f.filter.Layer = *layer
	}
	if *severity != "" {
		if !findingSeverity(*severity) {
			return usageError(inv.name, "--severity is error, warning or waived, not %q", *severity)
		}
		f.filter.Severity = *severity
	}
	if *locale != "" {
		if f.filter.Locale, err = normalizeLocale(inv, "--locale", *locale); err != nil {
			return err
		}
	}
	// The flag is a three-state answer: only the waived, only the
	// unwaived, or — when it was not given at all — both. A waived
	// finding is still reported (§2.3), so "both" is the honest default.
	if isSet(fs, "waived") {
		f.filter.Waived = waived
	}
	if f.limit < 0 {
		return usageError(inv.name, "--limit can't be negative")
	}

	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	page, err := p.client.Findings(ctx, p.scope, f.filter, f.limit)
	if err != nil {
		return inv.qualityError(err, "can't read the project's findings")
	}
	out := findingsDocument(page)
	return inv.emit(out, func(pr *printer) { printFindingsList(pr, out) })
}

func findingSeverity(s string) bool {
	switch domain.Severity(s) {
	case domain.Error, domain.Warning, domain.Waived:
		return true
	}
	return false
}

func findingsDocument(page remote.FindingPage) findingsJSON {
	out := findingsJSON{
		Schema: findingsSchema, Findings: nonNilList(page.Findings), Truncated: page.More,
		Counts: countsJSON{Errors: page.Counts.Errors, Warnings: page.Counts.Warnings, Waived: page.Counts.Waived},
	}
	if r := page.Run; r != nil {
		run := &findingsRunJSON{
			ID: r.Id, Ref: r.Ref, Commit: derefStr(r.Commit), Trigger: string(r.Trigger),
			PolicyVersion: r.PolicyVersion, Layers: make([]domain.Layer, 0, len(r.Layers)),
			StartedAt: r.StartedAt.UTC().Format(time.RFC3339),
		}
		if r.Conclusion != nil {
			run.Conclusion = string(*r.Conclusion)
		}
		for _, l := range r.Layers {
			run.Layers = append(run.Layers, domain.Layer(l))
		}
		out.Run = run
	}
	return out
}

func printFindingsList(p *printer, out findingsJSON) {
	if out.Run == nil {
		p.line("%s nothing has been checked yet", p.caution())
		p.line("  %s", p.dim("run `glossa check` or open a pull request; findings are stored by the run that found them"))
		return
	}
	r := out.Run
	p.line("%s %s", p.bold(runLabel(r)), p.dim(fmt.Sprintf("policy v%d · %s · %s",
		r.PolicyVersion, r.StartedAt, "layers: "+layerList(r.Layers))))
	p.line("  %s", p.dim("the run: "+countsText(out.Counts)))
	if len(out.Findings) == 0 {
		p.line("%s no finding matches", p.pass())
		return
	}
	// By layer, in report order: a reader looks for a kind of problem
	// before they look for a message.
	for _, layer := range domain.Layers {
		group := findingsIn(out.Findings, layer)
		if len(group) == 0 {
			continue
		}
		p.line("")
		p.line("%s %s", p.bold(string(layer)), p.dim(plural(len(group), "finding", "findings")))
		for _, f := range group {
			p.line("  %s %s  %s", severityMark(p, f.Severity), p.dim(f.Fingerprint), findingLabel(f))
			p.line("      %s", f.Code+": "+f.Message)
			if where := locusText(f.Locus); where != "" {
				p.line("      %s", p.dim(where))
			}
		}
	}
	if out.Truncated {
		p.line("")
		p.line("%s", p.dim("… the server holds more; raise --limit, or narrow the filters"))
	}
}

// countsText says the three counts the way the contract keeps them:
// waived on its own, never folded into the other two, so the number a
// reader sees is true.
func countsText(c countsJSON) string {
	return fmt.Sprintf("%s · %s · %d waived",
		plural(c.Errors, "error", "errors"), plural(c.Warnings, "warning", "warnings"), c.Waived)
}

func runLabel(r *findingsRunJSON) string {
	if r.Commit == "" {
		return r.Ref
	}
	return r.Ref + "@" + shortCommit(r.Commit)
}

func findingsIn(fs []domain.Finding, layer domain.Layer) []domain.Finding {
	var out []domain.Finding
	for _, f := range fs {
		if f.Layer == layer {
			out = append(out, f)
		}
	}
	return out
}

func severityMark(p *printer, s domain.Severity) string {
	switch s {
	case domain.Warning:
		return p.warn("warn ")
	case domain.Waived:
		return p.dim("waive")
	}
	return p.bad("error")
}

// findingLabel names what a finding is about: the locale and the key,
// where it has them.
func findingLabel(f domain.Finding) string {
	switch {
	case f.Locus.Locale != "" && f.Locus.Key != "":
		return f.Locus.Locale + " " + f.Locus.Key
	case f.Locus.Key != "":
		return f.Locus.Key
	case f.Locus.Locale != "":
		return f.Locus.Locale
	}
	return "(the project)"
}

// locusText is where you would go and look, from Context's fields.
func locusText(l domain.Locus) string {
	var parts []string
	if l.File != "" {
		where := l.File
		if l.Line > 0 {
			where = fmt.Sprintf("%s:%d", l.File, l.Line)
		}
		parts = append(parts, where)
	}
	if l.Route != "" {
		parts = append(parts, l.Route)
	}
	if l.Component != "" {
		parts = append(parts, l.Component)
	}
	return strings.Join(parts, " · ")
}
