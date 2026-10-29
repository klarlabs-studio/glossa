package cli

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// ── output shapes (cmd/glossa/README.md, "JSON output") ─────────────

type translateFilterJSON struct {
	Namespace string `json:"namespace,omitempty"`
	KeyPrefix string `json:"key_prefix,omitempty"`
	Missing   bool   `json:"missing"`
	Outdated  bool   `json:"outdated"`
}

// translatePlanJSON is what a dry run would queue in one locale, as the
// server's fill preview decides it.
type translatePlanJSON struct {
	Locale string   `json:"locale"`
	Queue  int      `json:"queue"`
	Keys   []string `json:"keys"`
	// Existing jobs would be reused; TMExact messages reuse an exact
	// translation-memory match; Provider messages would call a provider.
	Existing int `json:"existing"`
	TMExact  int `json:"tm_exact"`
	Provider int `json:"provider"`
	// Refused counts the messages that would not reach a provider, by
	// reason: sensitive, provider_consent, no_route, budget_exceeded.
	Refused map[string]int `json:"refused"`
	Cost    costJSON       `json:"cost"`
}

// costJSON prices a dry run's provider calls (micro-USD).
type costJSON struct {
	Estimated int64 `json:"estimated_micro_usd"`
	Max       int64 `json:"max_micro_usd"`
	Unpriced  bool  `json:"unpriced"`
}

// refusalJSON is a reason jobs will do little: consent off, no budget,
// no provider.
type refusalJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

type fillJSON struct {
	ID           string         `json:"id"`
	Locales      []string       `json:"locales"`
	Keys         int            `json:"keys,omitempty"`
	JobsCreated  int            `json:"jobs_created"`
	JobsExisting int            `json:"jobs_existing"`
	Skipped      map[string]int `json:"skipped"`
	JobStates    map[string]int `json:"job_states"`
	Warnings     []string       `json:"warnings"`
}

type failedJobJSON struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Locale      string `json:"locale"`
	State       string `json:"state"`
	FailureCode string `json:"failure_code,omitempty"`
	Error       string `json:"error,omitempty"`
}

type waitJSON struct {
	ElapsedMS int64           `json:"elapsed_ms"`
	JobStates map[string]int  `json:"job_states"`
	Failed    []failedJobJSON `json:"failed"`
}

type translateJSON struct {
	Schema   string              `json:"schema"`
	DryRun   bool                `json:"dry_run"`
	Locales  []string            `json:"locales"`
	Filter   translateFilterJSON `json:"filter"`
	Plan     []translatePlanJSON `json:"plan"`
	Skipped  map[string]int      `json:"skipped"`
	Refusals []refusalJSON       `json:"refusals"`
	// Cost is the dry run's total (absent without --dry-run).
	Cost  *costJSON  `json:"cost,omitempty"`
	Fills []fillJSON `json:"fills"`
	// Wait is null without --wait.
	Wait *waitJSON `json:"wait"`
}

const translateUsage = `translate --locale L [--locale L]... [--namespace NS] [--key-prefix P] [--missing] [--outdated]
                 [--dry-run] [--wait [--timeout 10m]]

Fills locales with AI suggestions (RFC 0003): one job per message missing (default) or outdated
(--outdated; both flags: both) in each locale. Messages in sensitive namespaces are never sent.
Suggestions are applied or queued for review by the project's routing policy
(` + "`glossa review list`" + ` shows the queue).

--dry-run reports what would be queued, and why jobs would do little (consent off, no budget,
no provider): it exits 1 when there is such a refusal. --wait polls the jobs until they finish
and exits 4 when some failed.`

type translateArgs struct {
	locales                         listFlag
	namespace, keyPrefix            string
	missing, outdated, dryRun, wait bool
	timeout, pollInterval           time.Duration
	idempotencyKey                  string
}

func parseTranslateArgs(inv *invocation, args []string) (translateArgs, error) {
	fs := inv.flags(translateUsage)
	var a translateArgs
	fs.Var(&a.locales, "locale", "a target locale (repeatable, or comma-separated)")
	fs.StringVar(&a.namespace, "namespace", "", "only messages in this namespace")
	fs.StringVar(&a.keyPrefix, "key-prefix", "", "only messages whose key starts with this")
	fs.BoolVar(&a.missing, "missing", false, "translate messages without a translation (the default)")
	fs.BoolVar(&a.outdated, "outdated", false, "translate messages whose translation is outdated")
	fs.BoolVar(&a.dryRun, "dry-run", false, "report what would be queued; queue nothing")
	fs.BoolVar(&a.wait, "wait", false, "wait for the jobs to finish")
	fs.DurationVar(&a.timeout, "timeout", 10*time.Minute, "with --wait: give up after this long")
	fs.DurationVar(&a.pollInterval, "poll-interval", 2*time.Second, "with --wait: how often to ask for progress")
	fs.StringVar(&a.idempotencyKey, "idempotency-key", "", "the Idempotency-Key (default: a new one per invocation)")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if err := noMore(inv, pos); err != nil {
		return a, err
	}
	if len(a.locales) == 0 {
		return a, usageError(inv.name, "translate needs --locale <locale> (repeatable)")
	}
	if !a.missing && !a.outdated {
		a.missing = true
	}
	if a.timeout <= 0 || a.pollInterval <= 0 {
		return a, usageError(inv.name, "--timeout and --poll-interval must be positive")
	}
	if a.idempotencyKey != "" && !idempotencyKeyPattern.MatchString(a.idempotencyKey) {
		return a, usageError(inv.name, "--idempotency-key must be 1-255 printable ASCII characters without spaces")
	}
	if a.dryRun && a.wait {
		return a, usageError(inv.name, "--dry-run queues nothing to --wait for")
	}
	return a, nil
}

func runTranslate(ctx context.Context, inv *invocation, args []string) error {
	a, err := parseTranslateArgs(inv, args)
	if err != nil {
		return err
	}
	locales, err := normalizeLocales(inv, "--locale", a.locales)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	if err := inv.checkTargetLocales(ctx, p, locales); err != nil {
		return err
	}
	out := translateJSON{Schema: "glossa.cli.translate/v1", DryRun: a.dryRun, Locales: locales,
		Filter: translateFilterJSON{Namespace: a.namespace, KeyPrefix: a.keyPrefix, Missing: a.missing, Outdated: a.outdated},
		Plan:   []translatePlanJSON{}, Skipped: map[string]int{}, Refusals: []refusalJSON{}, Fills: []fillJSON{}}
	if a.dryRun {
		return inv.translateDryRun(ctx, p, a, out)
	}
	return inv.translateFill(ctx, p, a, out)
}

// checkTargetLocales refuses locales the project lacks and its source.
func (inv *invocation) checkTargetLocales(ctx context.Context, p *project, locales []string) error {
	ls, err := p.client.Locales(ctx, p.scope)
	if err != nil {
		return inv.apiError(err, "can't list locales")
	}
	for _, l := range locales {
		if l == p.info.SourceLocale {
			return usageError(inv.name, "%s is the source locale; translate into the others", l)
		}
		if !slices.ContainsFunc(ls, func(pl remote.ProjectLocale) bool { return pl.Code == l }) {
			return &Error{Exit: ExitUsage, Code: "locale_not_found", What: "the project has no locale " + l,
				Fix: "add it in Studio first, or check --locale (`glossa locales` lists them)"}
		}
	}
	return nil
}

// refusals explains what keeps jobs from reaching a provider, from the
// fill's warning codes.
func refusals(codes []string) []refusalJSON {
	out := []refusalJSON{}
	for _, c := range codes {
		r := refusalJSON{Code: c}
		switch c {
		case "provider_consent_off":
			r.Message = "sending text to AI providers is off for the tenant: only exact translation-memory matches are reused, the other jobs fail (provider_consent)"
			r.Fix = "a tenant admin turns on provider consent in Studio (AI settings); `glossa ai status` shows it"
		case "no_budget":
			r.Message = "the tenant's monthly AI budget is 0: no provider calls are made"
			r.Fix = "a tenant admin sets a monthly budget in Studio (AI settings)"
		case "budget_exhausted":
			r.Message = "this month's AI budget is spent: provider calls are refused (budget_exceeded)"
			r.Fix = "wait for next month, or a tenant admin raises the budget; `glossa ai status` shows the spend"
		case "no_provider":
			r.Message = "no AI provider is configured and enabled: jobs fail (no_route)"
			r.Fix = "a tenant admin adds a provider with its API key in Studio (AI settings)"
		default:
			r.Message = c
		}
		out = append(out, r)
	}
	return out
}

// fillBody is the fill (or preview) request: the locales and filters,
// and which translations to fill by their state.
func fillBody(a translateArgs, locales []string) remote.CreateAIFill {
	sel := remote.AIFillSelect("missing")
	switch {
	case a.missing && a.outdated:
		sel = "missing_or_outdated"
	case a.outdated:
		sel = "outdated"
	}
	return remote.CreateAIFill{Locales: locales, Namespace: optionalStr(a.namespace), KeyPrefix: optionalStr(a.keyPrefix), Select: &sel}
}

// previewRefusals are the fill's warnings, plus budget_exhausted when
// the preview refuses calls for the budget although one is set.
func previewRefusals(pv remote.AIFillPreview) []string {
	codes := slices.Clone(pv.Warnings)
	exhausted := slices.ContainsFunc(pv.Locales, func(l remote.AIFillPreviewLocale) bool { return l.Refused["budget_exceeded"] > 0 })
	if exhausted && !slices.Contains(codes, "no_budget") {
		at := 0
		if len(codes) > 0 && codes[0] == "provider_consent_off" {
			at = 1
		}
		codes = slices.Insert(codes, at, "budget_exhausted")
	}
	return codes
}

func toCostJSON(c remote.AICostEstimate) costJSON {
	return costJSON{Estimated: c.EstimatedMicroUsd, Max: c.MaxMicroUsd, Unpriced: c.Unpriced}
}

// translateDryRun asks the server what the fill would do: its preview
// decides every message as its job would, and queues nothing.
func (inv *invocation) translateDryRun(ctx context.Context, p *project, a translateArgs, out translateJSON) error {
	pv, err := p.client.PreviewAIFill(ctx, p.scope, fillBody(a, out.Locales))
	if err != nil {
		return inv.m2Error(err, "can't preview the AI fill")
	}
	for _, l := range pv.Locales {
		plan := translatePlanJSON{Locale: l.Locale, Queue: len(l.Keys), Keys: nonNilList(l.Keys), Existing: l.Existing,
			TMExact: l.TmExact, Provider: l.Provider, Refused: map[string]int{}, Cost: toCostJSON(l.Cost)}
		for reason, n := range l.Refused {
			if reason == "sensitive" {
				out.Skipped["sensitive"] += n
				continue
			}
			plan.Refused[reason] = n
		}
		for reason, n := range l.Skipped {
			out.Skipped[reason] += n
		}
		out.Plan = append(out.Plan, plan)
	}
	cost := toCostJSON(pv.Cost)
	out.Cost = &cost
	out.Refusals = refusals(previewRefusals(pv))
	if err := inv.emit(out, func(pr *printer) { printTranslatePlan(pr, out) }); err != nil {
		return err
	}
	if len(out.Refusals) > 0 {
		return silentExit(ExitCheckFailed, "translate_refused")
	}
	return nil
}

func printTranslatePlan(pr *printer, out translateJSON) {
	total := 0
	for _, pl := range out.Plan {
		total += pl.Queue
	}
	pr.line("Dry run: %s would be queued %s", plural(total, "AI job", "AI jobs"), pr.dim("(nothing was queued)"))
	for _, pl := range out.Plan {
		pr.line("  %s  %s%s", pl.Locale, plural(pl.Queue, "message", "messages"), planDetail(pl))
		for i, k := range pl.Keys {
			if i == maxFindingsPerGroup {
				pr.line("    %s", pr.dim(fmt.Sprintf("… and %d more (--json lists all)", len(pl.Keys)-i)))
				break
			}
			pr.line("    %s", k)
		}
	}
	if n := out.Skipped["sensitive"]; n > 0 {
		pr.line("  %s", pr.dim(fmt.Sprintf("%s skipped: sensitive namespaces are never sent to a provider", plural(n, "message", "messages"))))
	}
	if c := out.Cost; c != nil && (c.Max > 0 || c.Unpriced) {
		line := fmt.Sprintf("  estimated cost $%.4f (at most $%.4f)", float64(c.Estimated)/1e6, float64(c.Max)/1e6)
		if c.Unpriced {
			line += "; a model has no price and counts as $0"
		}
		pr.line("%s", pr.dim(line))
	}
	printRefusals(pr, out.Refusals)
}

// planDetail says how a locale's jobs would run: reused, from
// translation memory, by a provider, or refused.
func planDetail(pl translatePlanJSON) string {
	var parts []string
	if pl.Existing > 0 {
		parts = append(parts, fmt.Sprintf("%d already queued or done", pl.Existing))
	}
	if pl.TMExact > 0 {
		parts = append(parts, fmt.Sprintf("%d from translation memory", pl.TMExact))
	}
	if pl.Provider > 0 {
		parts = append(parts, fmt.Sprintf("%d by a provider", pl.Provider))
	}
	reasons := make([]string, 0, len(pl.Refused))
	for r := range pl.Refused {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		parts = append(parts, fmt.Sprintf("%d refused (%s)", pl.Refused[r], r))
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, ", ")
}

func printRefusals(pr *printer, rs []refusalJSON) {
	for _, r := range rs {
		pr.line("%s %s", pr.caution(), r.Message)
		pr.line("  %s %s", pr.dim("fix:"), r.Fix)
	}
}

func toFillJSON(f remote.AIFill) fillJSON {
	out := fillJSON{ID: f.Id, Locales: f.Locales, JobsCreated: f.JobsCreated, JobsExisting: f.JobsExisting,
		Skipped: f.Skipped, JobStates: f.JobStates, Warnings: nonNilList(f.Warnings)}
	if f.Keys != nil {
		out.Keys = len(*f.Keys)
	}
	if out.Skipped == nil {
		out.Skipped = map[string]int{}
	}
	if out.JobStates == nil {
		out.JobStates = map[string]int{}
	}
	return out
}

func (inv *invocation) translateFill(ctx context.Context, p *project, a translateArgs, out translateJSON) error {
	key := orDefault(a.idempotencyKey, newIdempotencyKey())
	f, err := p.client.CreateAIFill(ctx, p.scope, fillBody(a, out.Locales), key)
	if err != nil {
		return inv.m2Error(err, "can't request the AI fill")
	}
	fj := toFillJSON(f)
	out.Fills = append(out.Fills, fj)
	for reason, n := range fj.Skipped {
		out.Skipped[reason] += n
	}
	warnings := fj.Warnings
	out.Refusals = refusals(warnings)
	if !a.wait {
		return inv.emit(out, func(pr *printer) { printFills(pr, out) })
	}
	if !inv.json {
		printFills(inv.out, out)
	}
	w, err := inv.waitForFills(ctx, p, &out, a)
	if err != nil {
		return err
	}
	out.Wait = w
	if err := inv.emit(out, func(pr *printer) { printWait(pr, *w, out.Locales) }); err != nil {
		return err
	}
	if len(w.Failed) > 0 {
		return silentExit(ExitPartial, "jobs_failed")
	}
	return nil
}

func printFills(pr *printer, out translateJSON) {
	created, existing := 0, 0
	for _, f := range out.Fills {
		created += f.JobsCreated
		existing += f.JobsExisting
	}
	if len(out.Fills) == 0 {
		pr.line("%s Nothing to translate in %s.", pr.pass(), strings.Join(out.Locales, ", "))
		return
	}
	extra := ""
	if existing > 0 {
		extra = fmt.Sprintf(" (%d already queued or done)", existing)
	}
	ids := make([]string, len(out.Fills))
	for i, f := range out.Fills {
		ids[i] = f.ID
	}
	pr.line("%s Queued %s for %s%s %s", pr.pass(), plural(created, "AI job", "AI jobs"), strings.Join(out.Locales, ", "), extra,
		pr.dim("(fill "+strings.Join(ids, ", ")+")"))
	var skipped []string
	for reason, n := range out.Skipped {
		skipped = append(skipped, fmt.Sprintf("%d %s", n, strings.ReplaceAll(reason, "_", " ")))
	}
	sort.Strings(skipped)
	if len(skipped) > 0 {
		pr.line("  %s", pr.dim("skipped: "+strings.Join(skipped, ", ")))
	}
	printRefusals(pr, out.Refusals)
}

// finalJobStates are the states a job doesn't leave.
func finalJobState(s string) bool { return s != "queued" && s != "running" }

// waitForFills polls the fills until their jobs are final or the
// timeout passes, printing progress to stderr.
func (inv *invocation) waitForFills(ctx context.Context, p *project, out *translateJSON, a translateArgs) (*waitJSON, error) {
	start := time.Now()
	deadline := start.Add(a.timeout)
	progress := !inv.json && !inv.quiet
	last := ""
	for {
		states := map[string]int{}
		for i := range out.Fills {
			f, err := p.client.AIFill(ctx, p.scope.Tenant, out.Fills[i].ID)
			if err != nil {
				return nil, inv.m2Error(err, "can't read the fill's progress")
			}
			out.Fills[i] = toFillJSON(f)
			for s, n := range out.Fills[i].JobStates {
				states[s] += n
			}
		}
		total, final := 0, 0
		for s, n := range states {
			total += n
			if finalJobState(s) {
				final += n
			}
		}
		if progress {
			line := fmt.Sprintf("  %d/%d jobs finished (%d running, %d queued)", final, total, states["running"], states["queued"])
			if line != last {
				if inv.env.ColorOutput {
					fmt.Fprintf(inv.env.Stderr, "\r%s", line)
				} else {
					fmt.Fprintln(inv.env.Stderr, line)
				}
				last = line
			}
		}
		if final == total {
			if progress && inv.env.ColorOutput {
				fmt.Fprintln(inv.env.Stderr)
			}
			w := &waitJSON{ElapsedMS: time.Since(start).Milliseconds(), JobStates: states, Failed: []failedJobJSON{}}
			failed, err := inv.failedJobs(ctx, p, out.Fills)
			if err != nil {
				return nil, err
			}
			w.Failed = failed
			return w, nil
		}
		if time.Now().After(deadline) {
			return nil, &Error{Exit: ExitNetwork, Code: "wait_timeout",
				What: fmt.Sprintf("the AI jobs didn't finish within %s", a.timeout),
				Why:  fmt.Sprintf("%d of %d jobs finished (%d running, %d queued)", final, total, states["running"], states["queued"]),
				Fix:  "wait longer (--timeout), or check the jobs in Studio; they keep running on the server"}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(a.pollInterval):
		}
	}
}

func (inv *invocation) failedJobs(ctx context.Context, p *project, fills []fillJSON) ([]failedJobJSON, error) {
	out := []failedJobJSON{}
	for _, f := range fills {
		for _, state := range []string{"failed", "dead"} {
			if f.JobStates[state] == 0 {
				continue
			}
			jobs, err := p.client.AIJobs(ctx, p.scope.Tenant, remote.JobFilter{Fill: f.ID, State: state})
			if err != nil {
				return nil, inv.m2Error(err, "can't list the failed jobs")
			}
			for _, j := range jobs {
				out = append(out, failedJobJSON{ID: j.Id, Key: j.MessageKey, Locale: j.Locale, State: string(j.State),
					FailureCode: derefStr(j.FailureCode), Error: derefStr(j.LastError)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Locale != out[j].Locale {
			return out[i].Locale < out[j].Locale
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

func printWait(pr *printer, w waitJSON, locales []string) {
	total := 0
	var parts []string
	for _, s := range []string{"succeeded", "skipped", "failed", "dead", "cancelled"} {
		if n := w.JobStates[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
		total += w.JobStates[s]
	}
	mark := pr.pass()
	if len(w.Failed) > 0 {
		mark = pr.fail()
	}
	pr.line("%s %s finished: %s", mark, plural(total, "job", "jobs"), orDefault(strings.Join(parts, ", "), "none"))
	for i, f := range w.Failed {
		if i == maxFindingsPerGroup {
			pr.line("  %s", pr.dim(fmt.Sprintf("… and %d more (--json lists all)", len(w.Failed)-i)))
			break
		}
		pr.line("  %s %s %s  %s: %s", pr.bad(f.State), f.Locale, f.Key, orDefault(f.FailureCode, "error"), f.Error)
	}
	if w.JobStates["succeeded"] > 0 {
		pr.line("  %s", pr.dim(fmt.Sprintf("`glossa review list --locale %s` shows the suggestions waiting for review", strings.Join(locales, ","))))
	}
}
