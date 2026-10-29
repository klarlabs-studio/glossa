package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa status --quality` (RFC 0005 §8, §13 wave 6): the same seven
// numbers the dashboard shows, in a terminal.
//
// Intent §46 says the CLI is first-class and §45 that a surface only a
// browser can reach is not a surface. The numbers come from wave 5's
// summary endpoint — one request, computed by the server, cached — so
// the terminal and the dashboard cannot disagree about how healthy a
// project is.
//
// The property this command exists to keep is narrower than that, and
// it is the one worth reading the code for: **"not measured" is not
// zero.** The summary leaves a number it could not compute *out*, and
// names it in `unmeasured` with why. So does this document — the field
// is absent, not 0 — and so does the human output, which prints "not
// measured" and the reason where a number would go. A CLI that printed
// `0 %` for "nobody has measured this" would be telling the same lie a
// dashboard would, in a place where it is harder to notice.

// statusQualitySchema is the command's document.
const statusQualitySchema = "glossa.cli.status.quality/v1"

// ── the document ────────────────────────────────────────────────────

type qualityCoverageJSON struct {
	Messages   int `json:"messages"`
	Translated int `json:"translated"`
	Outdated   int `json:"outdated"`
	Missing    int `json:"missing"`
}

type qualityPercentilesJSON struct {
	Samples    int     `json:"samples"`
	P50Seconds float64 `json:"p50_seconds"`
	P90Seconds float64 `json:"p90_seconds"`
}

type qualityAcceptanceJSON struct {
	Decisions        int     `json:"decisions"`
	Accepted         int     `json:"accepted"`
	Edited           int     `json:"edited"`
	Rejected         int     `json:"rejected"`
	AcceptanceRate   float64 `json:"acceptance_rate"`
	MeanEditDistance float64 `json:"mean_edit_distance"`
}

type qualityQueueJSON struct {
	Depth int `json:"depth"`
	// Age is absent for an empty queue: nothing has waited, which is not
	// a wait of zero.
	Age *qualityPercentilesJSON `json:"age,omitempty"`
}

type qualityContextJSON struct {
	ActiveMessages int `json:"active_messages"`
	WithUsage      int `json:"with_usage"`
	WithRegion     int `json:"with_region"`
}

type qualityChecksJSON struct {
	Runs      int `json:"runs"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Neutral   int `json:"neutral"`
	// PassRate and MedianSeconds are absent where nothing concluded.
	PassRate      *float64                `json:"pass_rate,omitempty"`
	MedianSeconds *float64                `json:"median_seconds,omitempty"`
	Latency       *qualityPercentilesJSON `json:"latency,omitempty"`
}

type qualityLayerCountsJSON struct {
	Layer  domain.Layer `json:"layer"`
	Counts countsJSON   `json:"counts"`
}

// qualityNumbersJSON is the seven numbers. Every one is a pointer, and
// that is the contract: a number nothing measured is absent here and
// named in Unmeasured, never rendered as a zero.
type qualityNumbersJSON struct {
	Coverage *qualityCoverageJSON     `json:"coverage,omitempty"`
	Findings *countsJSON              `json:"findings,omitempty"`
	AI       *qualityAcceptanceJSON   `json:"ai,omitempty"`
	Queue    *qualityQueueJSON        `json:"queue,omitempty"`
	Context  *qualityContextJSON      `json:"context,omitempty"`
	LeadTime *qualityPercentilesJSON  `json:"lead_time,omitempty"`
	Checks   *qualityChecksJSON       `json:"checks,omitempty"`
	ByLayer  []qualityLayerCountsJSON `json:"by_layer,omitempty"`
}

type qualityLayerJSON struct {
	Layer     domain.Layer `json:"layer"`
	Available bool         `json:"available"`
	// Checked says the newest run actually computed it, which is what
	// tells "clean" from "not looked at".
	Checked bool `json:"checked"`
	// Unavailable says why it cannot run here.
	Unavailable string      `json:"unavailable,omitempty"`
	Findings    *countsJSON `json:"findings,omitempty"`
}

type qualityLocaleJSON struct {
	Code      string `json:"code"`
	Direction string `json:"direction"`
	IsSource  bool   `json:"is_source"`
	// The per-locale numbers, absent for the same reason as the
	// project's.
	Coverage *qualityCoverageJSON    `json:"coverage,omitempty"`
	Findings *countsJSON             `json:"findings,omitempty"`
	AI       *qualityAcceptanceJSON  `json:"ai,omitempty"`
	Queue    *qualityQueueJSON       `json:"queue,omitempty"`
	LeadTime *qualityPercentilesJSON `json:"lead_time,omitempty"`
	// Layers is every layer, available or not: a layer a locale cannot
	// run must never read as a clean one (intent §41).
	Layers []qualityLayerJSON `json:"layers"`
}

// qualityUnmeasuredJSON names a number nobody computed, and why. It is
// the half of the document that makes the other half honest.
type qualityUnmeasuredJSON struct {
	Number string `json:"number"`
	Reason string `json:"reason"`
}

type qualityRunJSON struct {
	ID            string         `json:"id"`
	Ref           string         `json:"ref"`
	Commit        string         `json:"commit,omitempty"`
	Conclusion    string         `json:"conclusion,omitempty"`
	PolicyVersion int            `json:"policy_version"`
	Layers        []domain.Layer `json:"layers"`
	StartedAt     string         `json:"started_at"`
}

type statusQualityJSON struct {
	Schema      string `json:"schema"`
	ProjectID   string `json:"project_id"`
	Environment string `json:"environment"`
	// Locale is set when --locale narrowed the per-locale numbers.
	Locale string `json:"locale,omitempty"`
	// Since is the start of the window the windowed numbers cover.
	Since      string `json:"since"`
	ComputedAt string `json:"computed_at"`
	// Cached says the server served a previous computation again.
	Cached bool `json:"cached"`
	// Run is the check run the findings came from; null when the project
	// has never been checked.
	Run        *qualityRunJSON         `json:"run"`
	Numbers    qualityNumbersJSON      `json:"numbers"`
	Locales    []qualityLocaleJSON     `json:"locales"`
	Unmeasured []qualityUnmeasuredJSON `json:"unmeasured"`
}

// ── the command ─────────────────────────────────────────────────────

func (inv *invocation) statusQuality(ctx context.Context, q remote.SummaryQuery) error {
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	s, err := p.client.QualitySummaryFor(ctx, p.scope, q)
	if err != nil {
		return inv.qualityError(err, "can't read the project's quality summary")
	}
	out := qualityDocument(s)
	label := fmt.Sprintf("%s on %s", p.info.Slug, p.cfg.Server)
	return inv.emit(out, func(pr *printer) { printStatusQuality(pr, label, out) })
}

func qualityDocument(s remote.QualitySummary) statusQualityJSON {
	out := statusQualityJSON{
		Schema: statusQualitySchema, ProjectID: s.ProjectId, Environment: s.Environment,
		Locale: derefStr(s.Locale), Since: s.Since.UTC().Format(time.RFC3339),
		ComputedAt: s.ComputedAt.UTC().Format(time.RFC3339), Cached: s.Cached,
		Numbers: numbersOf(s.Project), Locales: make([]qualityLocaleJSON, 0, len(s.Locales)),
		Unmeasured: make([]qualityUnmeasuredJSON, 0, len(s.Unmeasured)),
	}
	if r := s.Project.Run; r != nil {
		run := &qualityRunJSON{
			ID: r.Id, Ref: r.Ref, Commit: derefStr(r.Commit), PolicyVersion: r.PolicyVersion,
			Layers: make([]domain.Layer, 0, len(r.Layers)), StartedAt: r.StartedAt.UTC().Format(time.RFC3339),
		}
		if r.Conclusion != nil {
			run.Conclusion = string(*r.Conclusion)
		}
		for _, l := range r.Layers {
			run.Layers = append(run.Layers, domain.Layer(l))
		}
		out.Run = run
	}
	for _, l := range s.Locales {
		out.Locales = append(out.Locales, localeOf(l))
	}
	for _, u := range s.Unmeasured {
		out.Unmeasured = append(out.Unmeasured, qualityUnmeasuredJSON{Number: string(u.Number), Reason: u.Reason})
	}
	return out
}

func numbersOf(h apiclient.QualityProjectHealth) qualityNumbersJSON {
	out := qualityNumbersJSON{
		Coverage: coverageOf(h.Coverage), Findings: countsOf(h.Findings), AI: acceptanceOf(h.Ai),
		Queue: queueOf(h.Queue), Context: contextOf(h.Context), LeadTime: percentilesOf(h.LeadTime),
		Checks: checksOf(h.Checks),
	}
	if h.ByLayer != nil {
		out.ByLayer = make([]qualityLayerCountsJSON, 0, len(*h.ByLayer))
		for _, l := range *h.ByLayer {
			out.ByLayer = append(out.ByLayer, qualityLayerCountsJSON{
				Layer: domain.Layer(l.Layer), Counts: countsFrom(l.Counts),
			})
		}
	}
	return out
}

func localeOf(l apiclient.QualityLocaleHealth) qualityLocaleJSON {
	out := qualityLocaleJSON{
		Code: l.Code, Direction: string(l.Direction), IsSource: l.IsSource,
		Coverage: coverageOf(l.Coverage), Findings: countsOf(l.Findings), AI: acceptanceOf(l.Ai),
		Queue: queueOf(l.Queue), LeadTime: percentilesOf(l.LeadTime),
		Layers: make([]qualityLayerJSON, 0, len(l.Layers)),
	}
	for _, h := range l.Layers {
		layer := qualityLayerJSON{
			Layer: domain.Layer(h.Layer), Available: h.Available, Checked: h.Checked,
			Findings: countsOf(h.Findings),
		}
		if h.Unavailable != nil {
			layer.Unavailable = string(*h.Unavailable)
		}
		out.Layers = append(out.Layers, layer)
	}
	return out
}

// Every converter below answers nil with nil. That is the whole of the
// "not measured is not zero" rule in code: there is nowhere a missing
// number could pick up a value.

func countsFrom(c apiclient.CheckRunCounts) countsJSON {
	return countsJSON{Errors: c.Errors, Warnings: c.Warnings, Waived: c.Waived}
}

func countsOf(c *apiclient.CheckRunCounts) *countsJSON {
	if c == nil {
		return nil
	}
	out := countsFrom(*c)
	return &out
}

func coverageOf(c *apiclient.QualityCoverage) *qualityCoverageJSON {
	if c == nil {
		return nil
	}
	return &qualityCoverageJSON{
		Messages: c.Messages, Translated: c.Translated, Outdated: c.Outdated, Missing: c.Missing,
	}
}

func percentilesOf(p *apiclient.QualityPercentiles) *qualityPercentilesJSON {
	if p == nil {
		return nil
	}
	return &qualityPercentilesJSON{Samples: p.Samples, P50Seconds: p.P50Seconds, P90Seconds: p.P90Seconds}
}

func acceptanceOf(a *apiclient.QualityAcceptance) *qualityAcceptanceJSON {
	if a == nil {
		return nil
	}
	return &qualityAcceptanceJSON{
		Decisions: a.Decisions, Accepted: a.Accepted, Edited: a.Edited, Rejected: a.Rejected,
		AcceptanceRate: a.AcceptanceRate, MeanEditDistance: a.MeanEditDistance,
	}
}

func queueOf(q *apiclient.QualityQueue) *qualityQueueJSON {
	if q == nil {
		return nil
	}
	return &qualityQueueJSON{Depth: q.Depth, Age: percentilesOf(q.Age)}
}

func contextOf(c *apiclient.QualityContextCoverage) *qualityContextJSON {
	if c == nil {
		return nil
	}
	return &qualityContextJSON{
		ActiveMessages: c.ActiveMessages, WithUsage: c.WithUsage, WithRegion: c.WithRegion,
	}
}

func checksOf(h *apiclient.QualityCheckHealth) *qualityChecksJSON {
	if h == nil {
		return nil
	}
	return &qualityChecksJSON{
		Runs: h.Runs, Succeeded: h.Succeeded, Failed: h.Failed, Neutral: h.Neutral,
		PassRate: h.PassRate, MedianSeconds: h.MedianSeconds, Latency: percentilesOf(h.Latency),
	}
}

// ── human output ────────────────────────────────────────────────────

// The seven numbers, in RFC 0005 §8's order, each with the name the
// summary uses for it in `unmeasured` — which is how a row that has no
// value finds its reason.
const (
	numberCoverage = "coverage"
	numberFindings = "findings"
	numberAI       = "ai"
	numberQueue    = "queue"
	numberContext  = "context"
	numberLeadTime = "lead_time"
	numberChecks   = "checks"
)

func printStatusQuality(p *printer, label string, out statusQualityJSON) {
	reasons := map[string]string{}
	for _, u := range out.Unmeasured {
		reasons[u.Number] = u.Reason
	}
	p.line("%s %s", p.bold(label), p.dim(qualityWindow(out)))
	if out.Run != nil {
		p.line("  %s", p.dim(fmt.Sprintf("newest run: %s, policy v%d, layers: %s",
			runConclusion(out.Run), out.Run.PolicyVersion, layerList(out.Run.Layers))))
	} else {
		p.line("  %s", p.dim("newest run: none — nothing has been checked yet"))
	}
	p.line("")
	n := out.Numbers
	rows := [][]string{{"NUMBER", "VALUE"}}
	row := func(name, title string, value string, measured bool) {
		rows = append(rows, []string{title, qualityValue(p, name, value, measured, reasons)})
	}
	row(numberCoverage, "1 coverage", coverageText(n.Coverage), n.Coverage != nil)
	row(numberFindings, "2 findings", findingsText(n.Findings), n.Findings != nil)
	row(numberAI, "3 AI acceptance", acceptanceText(n.AI), n.AI != nil)
	row(numberQueue, "4 review queue", queueText(n.Queue), n.Queue != nil)
	row(numberContext, "5 context coverage", contextText(n.Context), n.Context != nil)
	row(numberLeadTime, "6 lead time", percentilesText(n.LeadTime), n.LeadTime != nil)
	row(numberChecks, "7 check health", checksText(n.Checks), n.Checks != nil)
	p.table(rows)

	if len(n.ByLayer) > 0 {
		p.line("")
		p.line("%s", p.bold("Findings by layer"))
		layerRows := [][]string{{"LAYER", "ERRORS", "WARNINGS", "WAIVED"}}
		for _, l := range n.ByLayer {
			layerRows = append(layerRows, []string{string(l.Layer), strconv.Itoa(l.Counts.Errors),
				strconv.Itoa(l.Counts.Warnings), strconv.Itoa(l.Counts.Waived)})
		}
		p.table(layerRows)
	}
	printQualityLocales(p, out)
	if len(out.Unmeasured) > 0 {
		p.line("")
		p.line("%s", p.bold("Not measured"))
		for _, u := range out.Unmeasured {
			p.line("  %s %s", u.Number, p.dim(u.Reason))
		}
	}
}

// qualityValue is where the rule lives: a number with no value prints
// why, never a zero.
func qualityValue(p *printer, name, value string, measured bool, reasons map[string]string) string {
	if measured {
		return value
	}
	if why := reasons[name]; why != "" {
		return p.dim("not measured — " + why)
	}
	return p.dim("not measured")
}

func qualityWindow(out statusQualityJSON) string {
	parts := []string{"since " + out.Since, "environment " + out.Environment}
	if out.Locale != "" {
		parts = append(parts, "locale "+out.Locale)
	}
	if out.Cached {
		parts = append(parts, "cached, computed at "+out.ComputedAt)
	}
	return strings.Join(parts, " · ")
}

func runConclusion(r *qualityRunJSON) string {
	out := r.Ref
	if r.Commit != "" {
		out += "@" + shortCommit(r.Commit)
	}
	if r.Conclusion != "" {
		return out + " " + r.Conclusion
	}
	return out + " (in flight)"
}

func coverageText(c *qualityCoverageJSON) string {
	if c == nil {
		return ""
	}
	// The share is printed only where there is something to be a share
	// of: a project with no active messages is neither 0 % nor 100 %.
	if c.Messages == 0 {
		return "no active messages"
	}
	return fmt.Sprintf("%.1f%% translated (%d of %d) · %d outdated · %d missing",
		100*float64(c.Translated)/float64(c.Messages), c.Translated, c.Messages, c.Outdated, c.Missing)
}

func findingsText(c *countsJSON) string {
	if c == nil {
		return ""
	}
	return countsText(*c)
}

func acceptanceText(a *qualityAcceptanceJSON) string {
	if a == nil {
		return ""
	}
	return fmt.Sprintf("%.0f%% accepted of %s · mean edit distance %.2f",
		100*a.AcceptanceRate, plural(a.Decisions, "decision", "decisions"), a.MeanEditDistance)
}

func queueText(q *qualityQueueJSON) string {
	if q == nil {
		return ""
	}
	out := plural(q.Depth, "item waiting", "items waiting")
	if q.Age == nil {
		// An empty queue has no age, and so does one whose age could not
		// be pooled across locales. Both are "nothing measured", not 0 s.
		return out + " · age not measured"
	}
	return out + " · age " + percentilesText(q.Age)
}

func contextText(c *qualityContextJSON) string {
	if c == nil {
		return ""
	}
	if c.ActiveMessages == 0 {
		return "no active messages"
	}
	return fmt.Sprintf("%.0f%% with a usage, %.0f%% with a visible region (of %d)",
		100*float64(c.WithUsage)/float64(c.ActiveMessages),
		100*float64(c.WithRegion)/float64(c.ActiveMessages), c.ActiveMessages)
}

func percentilesText(p *qualityPercentilesJSON) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("p50 %s · p90 %s (%s)", duration(p.P50Seconds), duration(p.P90Seconds),
		plural(p.Samples, "sample", "samples"))
}

func checksText(h *qualityChecksJSON) string {
	if h == nil {
		return ""
	}
	var b strings.Builder
	if h.PassRate == nil {
		b.WriteString("pass rate not measured (nothing concluded)")
	} else {
		fmt.Fprintf(&b, "%.0f%% pass of %s", 100*(*h.PassRate), plural(h.Runs, "check", "checks"))
	}
	if h.MedianSeconds != nil {
		fmt.Fprintf(&b, " · median %s", duration(*h.MedianSeconds))
	}
	return b.String()
}

// duration prints seconds the way a person reads them.
func duration(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.0fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%.0fm", d.Minutes())
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	}
	return fmt.Sprintf("%.1fd", d.Hours()/24)
}

func printQualityLocales(p *printer, out statusQualityJSON) {
	if len(out.Locales) == 0 {
		return
	}
	p.line("")
	p.line("%s", p.bold("Per locale"))
	rows := [][]string{{"LOCALE", "COVERAGE", "ERRORS", "WARNINGS", "WAIVED", "QUEUE", "LEAD TIME"}}
	for _, l := range out.Locales {
		code := l.Code
		if l.IsSource {
			code += " (source)"
		}
		coverage, errs, warns, waived := "—", "—", "—", "—"
		if l.Coverage != nil && l.Coverage.Messages > 0 {
			coverage = fmt.Sprintf("%.1f%%", 100*float64(l.Coverage.Translated)/float64(l.Coverage.Messages))
		}
		if c := l.Findings; c != nil {
			errs, warns, waived = strconv.Itoa(c.Errors), strconv.Itoa(c.Warnings), strconv.Itoa(c.Waived)
		}
		queue := "—"
		if l.Queue != nil {
			queue = strconv.Itoa(l.Queue.Depth)
		}
		lead := "—"
		if l.LeadTime != nil {
			lead = duration(l.LeadTime.P50Seconds)
		}
		rows = append(rows, []string{code, coverage, errs, warns, waived, queue, lead})
	}
	p.table(rows)
	p.line("  %s", p.dim("— is not measured, and is not a zero"))
	// A layer a locale cannot run must never read as a clean one
	// (intent §41): naming it is the difference between "nothing wrong
	// here" and "nothing looked here".
	for _, l := range out.Locales {
		if gaps := unavailableLayers(l); len(gaps) > 0 {
			p.line("  %s %s", p.caution(), l.Code+": "+strings.Join(gaps, ", "))
		}
	}
}

func unavailableLayers(l qualityLocaleJSON) []string {
	var out []string
	for _, layer := range l.Layers {
		if layer.Available {
			continue
		}
		why := layer.Unavailable
		if why == "" {
			why = "unavailable"
		}
		out = append(out, string(layer.Layer)+" ("+why+")")
	}
	return out
}
