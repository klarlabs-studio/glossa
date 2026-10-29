package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/cli/qa"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/cli/snapshot"
	"go.klarlabs.de/glossa/platform/internal/cli/terminology"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// `glossa check` on the Quality library (RFC 0005 §13 wave 3).
//
// The command runs internal/quality's layers over the project and lets
// the check policy grade what they found. It reports domain.Finding
// itself — not a flattened copy of one — because the terminal and the
// pull request may not be able to disagree about what was found or
// where (§14 decision 1), and a second shape is the way they start to.
//
// What is the command's own is the *run*: which layers to compute
// (`--layer`), where the policy comes from (the server, the cache, or
// the built-in default), what to say about the decisions
// (`--explain-policy`), and which of the five documented exit codes to
// leave behind (§4.4).

// maxFindingsPerGroup caps human output; --json has everything.
const maxFindingsPerGroup = 20

// checkSchema is the command's own document. v2 is the Quality library:
// a finding is a glossa.finding/v1 finding, with the locus, the spans
// and the evidence that the flattened v1 shape dropped.
const checkSchema = "glossa.cli.check/v2"

type policyJSON struct {
	// RequireComplete is null when every locale is required.
	RequireComplete     []string `json:"require_complete"`
	FailOn              string   `json:"fail_on"`
	MissingTranslations string   `json:"missing_translations"`
	// Source says where the document came from: "server" (fetched this
	// run), "cache" (.glossa/policy.json) or "default" (the built-in
	// one, because there was neither).
	Source policyOrigin `json:"source"`
	// Version is the version of the document the run graded against; 0
	// for a policy that has none, which is every policy stored before
	// M4.
	Version int `json:"version,omitempty"`
	// Overridden says `glossa.yaml` or a flag changed the policy for
	// this run. The pull-request check ignores both (RFC 0005 §4.2), so
	// an overridden run is a local answer and says so.
	Overridden bool `json:"overridden,omitempty"`
	// Offline says the run graded against the cache because the server
	// was out of reach, or because --offline asked it to.
	Offline bool `json:"offline,omitempty"`
	// FetchedAt is when a cached document was fetched.
	FetchedAt string `json:"fetched_at,omitempty"`
	// Grace, when set, is when the previous version stops grading the
	// pull requests that predate this one (RFC 0005 §4.3).
	Grace *graceJSON `json:"grace,omitempty"`
}

// graceJSON is a rollout still in progress: two versions are live, and
// a check has to be able to say which it used.
type graceJSON struct {
	PreviousVersion int    `json:"previous_version"`
	Until           string `json:"until"`
}

// unavailableJSON is a layer the run was asked for and could not
// compute. Naming it is the point: silently dropping a layer is the one
// behaviour a check may never have (RFC 0005 §4.4).
type unavailableJSON struct {
	Layer domain.Layer `json:"layer"`
	Why   string       `json:"why"`
}

// ruleJSON is the policy rule that decided a finding's severity.
type ruleJSON struct {
	// Index is the rule's position in the document, which is how the
	// policy names it and how a person finds it again.
	Index    int                  `json:"index"`
	Selector checkpolicy.Selector `json:"selector"`
	Severity checkpolicy.Severity `json:"severity"`
	Mode     checkpolicy.Mode     `json:"mode"`
}

// explainJSON is `--explain-policy`: per finding, which selector
// matched, why that one, and whether it could fail the run. "Why did
// this fail?" has a mechanical answer (RFC 0005 §4.3).
type explainJSON struct {
	Fingerprint string          `json:"fingerprint"`
	Layer       domain.Layer    `json:"layer"`
	Code        string          `json:"code"`
	Severity    domain.Severity `json:"severity"`
	// Rule is the deciding rule, or null where none matched and the
	// layer's own severity stands.
	Rule *ruleJSON `json:"rule"`
	Mode string    `json:"mode"`
	// Fails says the finding can fail this run.
	Fails bool `json:"fails"`
	// Why says it in words.
	Why string `json:"why"`
}

type checkLocaleJSON struct {
	Code     string `json:"code"`
	IsSource bool   `json:"is_source"`
	Required bool   `json:"required"`
	Messages int    `json:"messages"`
	// Translated counts messages with a usable translation (any review
	// state but rejected).
	Translated int  `json:"translated"`
	Missing    int  `json:"missing"`
	Outdated   int  `json:"outdated"`
	Errors     int  `json:"errors"`
	Warnings   int  `json:"warnings"`
	Waived     int  `json:"waived"`
	Complete   bool `json:"complete"`
}

type checkJSON struct {
	Schema string     `json:"schema"`
	Policy policyJSON `json:"policy"`
	Origin string     `json:"origin"`
	// Messages is the project's active messages.
	Messages int `json:"messages"`
	// Invalid counts the source messages that don't parse.
	Invalid int               `json:"invalid_messages"`
	Locales []checkLocaleJSON `json:"locales"`
	// Layers are the layers the run computed, so a reader can tell
	// "clean" from "not looked at".
	Layers []domain.Layer `json:"layers"`
	// Skipped are the layers a policy rule or --layer left out.
	Skipped []domain.Layer `json:"skipped_layers"`
	// Unavailable are the layers this run could not compute at all.
	Unavailable []unavailableJSON `json:"unavailable_layers"`
	// Findings are glossa.finding/v1 findings, the same shape the pull
	// request and the server report.
	Findings []domain.Finding `json:"findings"`
	Explain  []explainJSON    `json:"explain,omitempty"`
	// Fixes are --fix's: every finding that carried a structured fix,
	// whether it was applied, and why not where it wasn't. The counts
	// and the conclusion below are the run's *before* them — the check
	// graded what it found, and claiming otherwise would mean grading a
	// project nobody has checked.
	Fixes []fixJSON `json:"fixes,omitempty"`
	// Record is what became of putting this run on the project's record
	// (check_record.go); absent when recording was never asked for.
	Record *recordJSON `json:"record,omitempty"`
	// Waivers is what the project's waivers did to this run, and — when
	// they could not be read — why they did nothing (check_waivers.go).
	Waivers  checkWaiversJSON `json:"waivers"`
	Errors   int              `json:"errors"`
	Warnings int              `json:"warnings"`
	Waived   int              `json:"waived"`
	// Conclusion is the run's verdict, spelled as a check run spells it.
	Conclusion domain.Conclusion `json:"conclusion"`
	Passed     bool              `json:"passed"`
}

// checkFlags is one invocation of `glossa check`.
type checkFlags struct {
	offline     bool
	terminology bool
	explain     bool
	// fix applies the structured fixes the findings carry (check_fix.go).
	fix bool
	// record decides whether the run is put on the project's record
	// (check_record.go). nil is the default — in CI yes, elsewhere no —
	// and a set --record or --record=false decides outright.
	record *bool
	// layers is what --layer selected, nil when it selected nothing and
	// every layer the policy leaves on runs.
	layers []string
	// require and failOn are the local overrides.
	require, failOn string
}

// wantsLayer reports whether --layer left the layer in, before the
// policy is consulted. It asks the overrides rather than deciding for
// itself: `--layer` is a local override like the other two, and a
// second implementation of "which layers does this run compute" is how
// the terminal and the pull request start to disagree.
func (f checkFlags) wantsLayer(l domain.Layer) bool {
	return checkpolicy.Overrides{Layers: f.layers}.Selects(string(l))
}

// wantsTerminology reports whether the run asks the server's termbase:
// --terminology, or --layer terminology, which says the same thing.
func (f checkFlags) wantsTerminology() bool {
	return f.terminology || (f.layers != nil && f.wantsLayer(domain.LayerTerminology))
}

func runCheck(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("check [--offline] [--terminology] [--layer=<layer>] [--explain-policy] [--fix] " +
		"[--record] [--require-complete=de,en|none] [--fail-on=error|warning|never]")
	offline := fs.Bool("offline", false, "check the local catalogs instead of the server's project")
	record := fs.Bool("record", false,
		"record this run and its findings on the server, so it reaches Studio, the findings list and the "+
			"summary (default: in CI; --record=false never)")
	terms := fs.Bool("terminology", false, "also check the translations against the termbase (needs the server)")
	var layers listFlag
	fs.Var(&layers, "layer", "only run these QA layers (repeatable, or comma-separated; "+
		"default: every layer the policy leaves on)")
	explain := fs.Bool("explain-policy", false,
		"say, per finding, which policy rule gave it its severity and whether that rule can fail the run")
	fix := fs.Bool("fix", false,
		"apply the structured fixes the findings carry, to the local catalogs — only the ones that name "+
			"exact text; the rest are reported with why")
	require := fs.String("require-complete", "",
		"locales that must be complete (comma-separated, or none; default: glossa.yaml's check.require_complete, else the project's check policy)")
	failOn := fs.String("fail-on", "",
		"lowest severity that fails the check: error, warning or never (default: glossa.yaml's check.fail_on, else the project's check policy)")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	cfg, err := inv.loadConfig()
	if err != nil {
		return err
	}
	f := checkFlags{offline: *offline, terminology: *terms, explain: *explain, fix: *fix,
		require: *require, failOn: *failOn}
	// A flag that was actually typed decides; one that was not leaves
	// the default to CI (check_record.go), which flag.Bool alone cannot
	// express.
	fs.Visit(func(set *flag.Flag) {
		if set.Name == "record" {
			asked := *record
			f.record = &asked
		}
	})
	if f.layers, err = selectedLayers(inv, layers); err != nil {
		return err
	}
	if f.wantsTerminology() && f.offline {
		return usageError(inv.name, "the terminology layer reads the server's termbase: drop --offline")
	}
	run, err := inv.checkRun(ctx, cfg, f)
	if err != nil {
		return err
	}
	policy, overrides, err := checkPolicy(inv, cfg, run.policy.Policy, f)
	if err != nil {
		return err
	}
	// The style layer's guides are resolved after the policy, because
	// the policy decides whether the layer runs at all and a layer
	// nobody is going to compute is not worth a read per locale.
	inv.readStyles(ctx, run, f, policy)
	checkers, unavailable := run.checkers(f)
	run.unavailable = append(run.unavailable, unavailable...)
	run.startedAt = time.Now().UTC()
	report := run.waive(qa.RunProject(run.project(), policy, checkers...), policy)
	out := checkDocument(run, report, policy, overrides, f)
	// The record is filed before --fix edits anything: the check graded
	// what it found, and a record of the fixed catalog would be a record
	// of a project nobody has checked.
	out.Record = inv.recordCheck(ctx, run, out, f, detectBuild(ctx, inv.env.getenv, cfg.Dir()))
	if f.fix {
		// After the run is graded and before it is printed: --fix edits
		// what the check found, and what the check found is what it
		// reports. The exit code stays the run's own (checkExit).
		out.Fixes = inv.applyFixes(cfg, report.Findings)
	}
	if err := inv.emit(out, func(p *printer) { printCheck(p, run, out, report) }); err != nil {
		return err
	}
	return checkExit(out)
}

// checkExit is RFC 0005 §4.4, the half a finished run decides: 1 when
// the policy failed it, 4 when it lost a layer, 0 otherwise.
//
// A run that both failed the policy and lost a layer exits 1. 1 is the
// code CI branches on, and a failed check is a failed check whatever
// else went wrong; reporting 4 there would turn a red build green for
// anyone who only tests for 1.
func checkExit(out checkJSON) error {
	switch {
	case !out.Passed:
		return silentExit(ExitCheckFailed, "check_failed")
	case len(out.Unavailable) > 0:
		return silentExit(ExitPartial, "layers_unavailable")
	}
	return nil
}

// selectedLayers parses --layer, given repeatedly or comma-separated.
//
// An unknown name is a usage error (exit 2) and not a quietly ignored
// flag: a CI job that asks for a layer it spelled wrong must be told,
// not silently given a smaller check than it thinks it ran.
func selectedLayers(inv *invocation, flags listFlag) ([]string, error) {
	var out []string
	for _, v := range flags {
		for _, part := range strings.Split(v, ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			if !domain.Layer(name).Valid() {
				return nil, usageError(inv.name, "%q is not a QA layer; the layers are %s",
					name, strings.Join(layerNames(), ", "))
			}
			if !contains(out, name) {
				out = append(out, name)
			}
		}
	}
	return out, nil
}

func layerNames() []string {
	out := make([]string, 0, len(domain.Layers))
	for _, l := range domain.Layers {
		out = append(out, string(l))
	}
	return out
}

// checkSubject is what a run checks and what it grades against: the
// catalog, the policy and where each came from.
type checkSubject struct {
	snapshot *snapshot.Snapshot
	label    string
	policy   resolvedPolicy
	// extra are the layers only the server can compute, already
	// computed (terminology).
	extra []qa.Checker
	// styles are the effective style guides by target locale, which the
	// style layer grades against and only the server can resolve
	// (check_style.go). nil where this run could not read them, and the
	// layer is named in unavailable rather than left to look clean.
	styles map[string]qa.StyleGuide
	// unavailable are the layers the run was asked for and could not
	// compute.
	unavailable []unavailableJSON
	// offline says the run read the local catalogs.
	offline bool
	// degraded says the server was out of reach and the cached policy
	// stood in for it.
	degraded bool
	// deselected are the layers --layer left out. They are named beside
	// the ones the policy switched off, because "not looked at" and
	// "clean" are different answers however a layer came to be left out.
	deselected []domain.Layer
	// client and scope are the server this run read the project from,
	// kept so the run can be recorded against the same project it
	// graded (check_record.go). Both are nil offline.
	client *remote.Client
	scope  remote.Scope
	// waivers are the project's live waivers, applied to what this run
	// found (check_waivers.go).
	waivers []domain.Waiver
	// waiversWhy says why they could not be applied; "" when they were.
	waiversWhy string
	// ref is the branch this run is of, which is how far a
	// branch-scoped waiver reaches.
	ref string
	// startedAt is when the layers began, which the recorded run keeps.
	startedAt time.Time
}

// project is what this run grades: the snapshot, plus what a snapshot
// cannot carry.
func (s *checkSubject) project() *layers.Project {
	return qa.Project(s.snapshot, qa.WithStyles(s.styles))
}

// checkers are the layers this run computes, and the ones it was asked
// for and cannot.
//
// The policy still decides whether a selected layer runs: a rule that
// switches a layer off means the project does not pay for it, and
// `--layer` cannot buy it back (checkpolicy.Overrides.Selects). What is
// decided here is the other half — a layer nothing in this run can
// compute, which is named rather than dropped.
func (s *checkSubject) checkers(f checkFlags) ([]qa.Checker, []unavailableJSON) {
	available := append(qa.Default(), s.extra...)
	var out []qa.Checker
	for _, c := range available {
		// A layer already named as one this run could not compute does
		// not run: a checker with nothing to check against would report
		// nothing, and "nothing" from a layer that never ran is exactly
		// the green nobody may be shown (RFC 0005 §4.4).
		if hasUnavailable(s.unavailable, c.Layer()) {
			continue
		}
		if f.wantsLayer(c.Layer()) {
			out = append(out, c)
			continue
		}
		s.deselected = append(s.deselected, c.Layer())
	}
	var missing []unavailableJSON
	for _, name := range f.layers {
		layer := domain.Layer(name)
		if hasChecker(available, layer) || hasUnavailable(s.unavailable, layer) {
			continue
		}
		missing = append(missing, unavailableJSON{Layer: layer,
			Why: "`glossa check` does not compute this layer; its findings come from the server"})
	}
	return out, missing
}

func hasChecker(cs []qa.Checker, l domain.Layer) bool {
	for _, c := range cs {
		if c.Layer() == l {
			return true
		}
	}
	return false
}

func hasUnavailable(us []unavailableJSON, l domain.Layer) bool {
	for _, u := range us {
		if u.Layer == l {
			return true
		}
	}
	return false
}

// checkRun reads what the check runs over and the policy it grades
// against.
//
// Offline there is no project to ask, so the policy is the cache's or
// the built-in default. Online the policy is fetched and cached — and
// if the server cannot be reached at all, a cached policy lets the run
// go ahead against the local catalogs (RFC 0005 §4.4: exit 3 is the
// server out of reach *and* no cache).
func (inv *invocation) checkRun(ctx context.Context, cfg *config.Config, f checkFlags) (*checkSubject, error) {
	if f.offline {
		run, err := inv.localRun(cfg, f, nil)
		if err == nil {
			run.waiversWhy = "--offline has no server to read the project's waivers from"
		}
		return run, err
	}
	p, err := inv.connectWith(ctx, cfg)
	if err != nil {
		cached, ok, cerr := inv.offlineFallback(cfg, err)
		if cerr != nil {
			return nil, cerr
		}
		if !ok {
			return nil, err
		}
		run, lerr := inv.localRun(cfg, f, &cached)
		if lerr != nil {
			return nil, lerr
		}
		run.degraded = true
		run.waiversWhy = "the server was out of reach, so the project's waivers could not be read"
		return run, nil
	}
	policy, err := inv.fetchPolicy(ctx, cfg, projectPolicySource{client: p.client, scope: p.scope, settings: p.info.Settings.CheckPolicy})
	if err != nil {
		return nil, err
	}
	// Orphans: a translation whose message the catalog obsoleted is the
	// server's `unknown-key`, and the server's own snapshot reads the
	// same bounded page of them, so the terminal and every other surface
	// grade the same project.
	s, err := snapshot.FromServer(ctx, p.client, p.scope, p.info.SourceLocale, snapshot.Options{Orphans: true})
	if err != nil {
		return nil, inv.apiError(err, "can't read the project from the server")
	}
	run := &checkSubject{
		snapshot: s, label: fmt.Sprintf("%s on %s", p.info.Slug, cfg.Server), policy: policy,
		client: p.client, scope: p.scope, ref: detectBuild(ctx, inv.env.getenv, cfg.Dir()).Branch,
	}
	inv.readWaivers(ctx, run)
	if f.wantsTerminology() {
		extra, err := inv.terminologyCheckers(ctx, p, s)
		switch {
		case err == nil:
			run.extra = append(run.extra, extra)
		case isNetworkError(err):
			// Exit 4's case: the termbase was unreachable. The other
			// layers still ran, and the one that didn't is named.
			run.unavailable = append(run.unavailable, unavailableJSON{
				Layer: domain.LayerTerminology, Why: "the termbase couldn't be read: " + asError(err).Error()})
		default:
			return nil, err
		}
	}
	return run, nil
}

// offlineFallback reports whether a cached policy may stand in for a
// server this run couldn't reach. Only an unreachable or refusing
// server qualifies: a project that isn't there, or a glossa.yaml naming
// a tenant that can't be resolved, is a configuration mistake and stays
// one.
func (inv *invocation) offlineFallback(cfg *config.Config, connErr error) (resolvedPolicy, bool, error) {
	if !isNetworkError(connErr) {
		return resolvedPolicy{}, false, nil
	}
	return inv.cachedPolicy(cfg)
}

func isNetworkError(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Exit == ExitNetwork
}

// localRun checks the local catalogs. pol is the policy a degraded run
// already resolved; otherwise the cache decides, and the built-in
// default stands where there is none.
func (inv *invocation) localRun(cfg *config.Config, f checkFlags, pol *resolvedPolicy) (*checkSubject, error) {
	s, err := loadLocal(cfg)
	if err != nil {
		return nil, err
	}
	run := &checkSubject{snapshot: s, label: "local catalogs", offline: true}
	switch {
	case pol != nil:
		run.policy = *pol
	default:
		cached, ok, err := inv.cachedPolicy(cfg)
		if err != nil {
			return nil, err
		}
		run.policy = cached
		if !ok {
			run.policy = resolvedPolicy{Origin: policyFromDefault}
		}
	}
	if f.wantsTerminology() {
		run.unavailable = append(run.unavailable, unavailableJSON{Layer: domain.LayerTerminology,
			Why: "the termbase is the server's, and this run has no server"})
	}
	return run, nil
}

// checkPolicy is the resolved document with this run's local overrides
// applied: flags > glossa.yaml > the fetched or cached document > the
// built-in default.
//
// The overrides are local and stay local. `glossa.yaml`'s `check:`
// block and the flags are honoured for this run and **ignored by the
// pull-request check** (RFC 0005 §4.2, §14 decision 3): a developer can
// tighten or loosen their own loop and cannot change what CI decides.
// They reach fail_on, require_complete and which layers run, and
// nothing else — the rules, the environments and the version are the
// project's.
//
// missing_translations has no flag and no glossa.yaml key for the same
// reason, one layer down: whether an untranslated key blocks is the
// project's call, or a pull request and the terminal would part ways on
// the one question the check exists to answer.
func checkPolicy(inv *invocation, cfg *config.Config, stored checkpolicy.Policy, f checkFlags) (
	checkpolicy.Policy, checkpolicy.Overrides, error,
) {
	p, o := stored, checkpolicy.Overrides{Layers: f.layers}
	if v := orDefault(f.failOn, cfg.Check.FailOn); v != "" {
		sev, err := checkpolicy.ParseFailOn(v)
		if err != nil {
			return p, o, usageError(inv.name, "--fail-on must be error, warning or never, not %q", v)
		}
		o.FailOn = sev
	}
	var list []string
	switch {
	case f.require == "none":
		o.RequireComplete = checkpolicy.RequiredLocales()
		list = nil
	case f.require != "":
		list = strings.Split(f.require, ",")
	case cfg.Check.RequireComplete != nil:
		list = cfg.Check.RequireComplete
	}
	if list != nil {
		req := []string{}
		for _, l := range list {
			tag, err := bcp47.Parse(strings.TrimSpace(l))
			if err != nil {
				return p, o, usageError(inv.name, "%q in --require-complete is not a locale", l)
			}
			req = append(req, tag.String())
		}
		o.RequireComplete = checkpolicy.RequiredLocales(req...)
	}
	p = p.Override(o)
	if p.FailOn == "" {
		p.FailOn = checkpolicy.Error
	}
	return p, o, nil
}

// checkDocument is the run as `--json` prints it.
func checkDocument(
	run *checkSubject, r qualityapp.Report, policy checkpolicy.Policy,
	o checkpolicy.Overrides, f checkFlags,
) checkJSON {
	out := checkJSON{
		Schema: checkSchema, Origin: r.Origin, Messages: r.Messages, Invalid: r.Invalid,
		Layers: nonNilList(r.Layers), Skipped: nonNilList(append(r.Skipped, run.deselected...)),
		Unavailable: nonNilList(run.unavailable), Findings: nonNilList(r.Findings),
		Errors: r.Counts.Errors, Warnings: r.Counts.Warnings, Waived: r.Counts.Waived,
		Conclusion: r.Conclusion, Passed: r.Passed(), Waivers: run.waiversDocument(r),
		Policy: policyJSON{
			RequireComplete: policy.RequireComplete, FailOn: string(policy.FailOn),
			MissingTranslations: string(policy.MissingTranslations), Source: run.policy.Origin,
			Version: r.PolicyVersion, Overridden: !o.Empty(),
			Offline: run.offline && run.policy.Origin == policyFromCache,
		},
	}
	if out.Policy.Source == "" {
		out.Policy.Source = policyFromDefault
	}
	if !run.policy.FetchedAt.IsZero() && run.policy.Origin == policyFromCache {
		out.Policy.FetchedAt = run.policy.FetchedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if p := run.policy.Policy; p.Previous != nil && p.GraceUntil != nil {
		out.Policy.Grace = &graceJSON{
			PreviousVersion: p.Previous.Version, Until: p.GraceUntil.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	for _, l := range r.Locales {
		out.Locales = append(out.Locales, checkLocaleJSON{
			Code: l.Code, IsSource: l.IsSource, Required: l.Required, Messages: l.Messages,
			Translated: l.Translated, Missing: l.Missing, Outdated: l.Outdated,
			Errors: l.Errors, Warnings: l.Warnings, Waived: l.Waived, Complete: l.Complete,
		})
	}
	if f.explain {
		out.Explain = explainDecisions(policy, r)
	}
	return out
}

// explainDecisions is --explain-policy: one explanation per finding, in
// the order the findings come out.
func explainDecisions(policy checkpolicy.Policy, r qualityapp.Report) []explainJSON {
	out := make([]explainJSON, 0, len(r.Findings))
	for i, fnd := range r.Findings {
		var d checkpolicy.Decision
		if i < len(r.Decisions) {
			d = r.Decisions[i]
		}
		e := explainJSON{
			Fingerprint: fnd.Fingerprint, Layer: fnd.Layer, Code: fnd.Code, Severity: fnd.Severity,
			Mode: string(d.Mode), Fails: policy.FailsDecision(d),
		}
		if d.Rule >= 0 && d.Rule < len(policy.Rules) {
			rule := policy.Rules[d.Rule]
			e.Rule = &ruleJSON{Index: d.Rule, Selector: rule.Selector, Severity: rule.Severity, Mode: ruleMode(rule)}
		}
		e.Why = explainWhy(policy, fnd, d, e.Rule)
		out = append(out, e)
	}
	return out
}

// explainWhy says in words why a finding has the severity it has: which
// rule matched, why that rule beat the others, and what stops it from
// failing the run.
func explainWhy(policy checkpolicy.Policy, f domain.Finding, d checkpolicy.Decision, rule *ruleJSON) string {
	if f.Severity == domain.Waived {
		return "waived: an accepted finding is still reported and can never fail a run"
	}
	var b strings.Builder
	if rule == nil {
		fmt.Fprintf(&b, "no rule matched, so the %s layer's own severity stands", f.Layer)
	} else {
		fmt.Fprintf(&b, "rule %d (%s) is the most specific match (%d of 5 selector fields); ties go to the later rule",
			rule.Index, selectorText(rule.Selector), rule.Selector.Specificity())
	}
	switch {
	case d.Mode == checkpolicy.ModeWarn:
		b.WriteString("; mode: warn, so it reports and can't change the conclusion")
	case policy.FailsDecision(d):
		fmt.Fprintf(&b, "; fail_on is %s, so it fails the run", policy.FailOn)
	default:
		fmt.Fprintf(&b, "; fail_on is %s, so it doesn't fail the run", policy.FailOn)
	}
	switch {
	case d.Clamped && f.Provisional():
		fmt.Fprintf(&b, "; clamped to warning: seen %s, and a visual finding is evidence only once the same"+
			" fingerprint comes back in the next capture of the same route, viewport and locale",
			plural(f.Sightings(), "time", "times"))
	case d.Clamped:
		b.WriteString("; clamped to warning: a build never fails on a model's opinion")
	}
	return b.String()
}

// ruleMode is a rule's mode with "" spelled out, which is what a
// document that leaves it unsaid means.
func ruleMode(r checkpolicy.Rule) checkpolicy.Mode {
	if r.Mode == checkpolicy.ModeWarn {
		return checkpolicy.ModeWarn
	}
	return checkpolicy.ModeEnforce
}

// selectorText prints a selector's named fields the way the document
// writes them.
func selectorText(s checkpolicy.Selector) string {
	var parts []string
	for _, kv := range [][2]string{
		{"layer", s.Layer}, {"code", s.Code}, {"locale", s.Locale},
		{"namespace", s.Namespace}, {"environment", s.Environment},
	} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	if len(parts) == 0 {
		return "everything"
	}
	return strings.Join(parts, ", ")
}

// terminologyCheckers is the terminology layer: every translation but
// rejected ones, checked against the server's termbase.
//
// The findings are identified against the snapshot before they are
// reported. The termbase check answers by key, and a finding's identity
// is hashed over the catalog message ID (RFC 0005 §2.1) — which the PR
// check's own terminology findings carry, so without this the terminal
// and the pull request would print two fingerprints for one finding. The
// layer reads the server, so there is always an ID to resolve.
func (inv *invocation) terminologyCheckers(ctx context.Context, p *project, s *snapshot.Snapshot) (qa.Checker, error) {
	var locales []string
	for _, l := range s.TargetLocales() {
		locales = append(locales, l.Code)
	}
	report, err := inv.terminology(ctx, p, terminology.Options{Locales: locales})
	if err != nil {
		return nil, err
	}
	return qa.Precomputed(domain.LayerTerminology, qa.Project(s).Identify(report.QA())), nil
}

// snapshot reads the project from the server, or the local catalogs.
func (inv *invocation) snapshot(ctx context.Context, cfg *config.Config, offline bool, opts snapshot.Options) (*snapshot.Snapshot, string, error) {
	if offline {
		s, err := loadLocal(cfg)
		return s, "local catalogs", err
	}
	p, err := inv.connectWith(ctx, cfg)
	if err != nil {
		return nil, "", err
	}
	s, err := snapshot.FromServer(ctx, p.client, p.scope, p.info.SourceLocale, opts)
	if err != nil {
		return nil, "", inv.apiError(err, "can't read the project from the server")
	}
	return s, fmt.Sprintf("%s on %s", p.info.Slug, cfg.Server), nil
}

func printCheck(p *printer, run *checkSubject, out checkJSON, r qualityapp.Report) {
	mark := func(ok bool) string {
		if ok {
			return p.pass()
		}
		return p.fail()
	}
	byLocale := map[string][]domain.Finding{}
	var structural, parity, other []domain.Finding
	for _, f := range r.Findings {
		switch {
		case f.Layer == domain.LayerStructure && f.Locus.Locale == run.snapshot.SourceLocale:
			structural = append(structural, f)
		case f.Layer == domain.LayerParity:
			parity = append(parity, f)
		case f.Locus.Locale == "":
			other = append(other, f)
		default:
			byLocale[f.Locus.Locale] = append(byLocale[f.Locus.Locale], f)
		}
	}
	p.line("%s %s discovered (%s)", p.pass(), plural(r.Messages, "message", "messages"), run.label)
	printPolicyLine(p, run, out)
	if len(out.Layers) > 0 {
		p.line("  %s", p.dim("layers: "+layerList(out.Layers)))
	}
	if len(structural) == 0 {
		p.line("%s message structures valid", p.pass())
	} else {
		p.line("%s %s", p.fail(), plural(len(structural), "invalid message", "invalid messages"))
		printFindings(p, structural, false)
	}
	printParity(p, parity)
	for _, l := range r.Locales {
		if l.IsSource {
			continue
		}
		fs := byLocale[l.Code]
		delete(byLocale, l.Code)
		errs := countSeverity(fs, domain.Error)
		switch {
		case len(fs) == 0:
			p.line("%s %s complete", mark(true), l.Code)
		case errs == 0:
			p.line("%s %s %s", p.caution(), l.Code, localeSummary(l))
		default:
			p.line("%s %s %s", mark(false), l.Code, localeSummary(l))
		}
		printFindings(p, fs, false)
	}
	// What is left is a locale the per-locale loop did not print: a
	// required locale the project lacks, or the source locale, which a
	// layer that reads a screen rather than a translation can find
	// something in. In a stable order, and marked by what was found —
	// the visual layer puts warnings here, and a warning is not a ✗.
	for _, locale := range slices.Sorted(maps.Keys(byLocale)) {
		fs := byLocale[locale]
		if countSeverity(fs, domain.Error) == 0 {
			p.line("%s %s", p.caution(), locale)
		} else {
			p.line("%s %s", p.fail(), locale)
		}
		printFindings(p, fs, false)
	}
	printFindings(p, other, true)
	printLayerGaps(p, out)
	if out.Explain != nil {
		printExplain(p, out.Explain)
	}
	printFixes(p, out.Fixes)
	printCheckWaivers(p, out.Waivers)
	printRecord(p, out.Record)
	p.line("")
	if out.Passed {
		p.line("%s", p.ok("Localization check passed."))
	} else {
		p.line("%s", p.bad(fmt.Sprintf("Localization check failed: %s, %s.",
			plural(out.Errors, "error", "errors"), plural(out.Warnings, "warning", "warnings"))))
	}
}

// printPolicyLine says which policy graded the run. A check that does
// not say what it graded against is a check nobody can argue with
// (RFC 0005 §4.2).
func printPolicyLine(p *printer, run *checkSubject, out checkJSON) {
	var b strings.Builder
	b.WriteString("policy: ")
	switch out.Policy.Source {
	case policyFromServer:
		b.WriteString("server" + versionSuffix(out.Policy.Version))
	case policyFromCache:
		b.WriteString("cached" + versionSuffix(out.Policy.Version) + " from " + policyCachePath)
		if run.degraded {
			b.WriteString(" (the server is out of reach)")
		}
	default:
		b.WriteString("built-in default (no server, no cache): every locale required, fail_on: error")
	}
	if out.Policy.Overridden {
		b.WriteString(" + local overrides (the pull-request check ignores them)")
	}
	p.line("  %s", p.dim(b.String()))
	if g := out.Policy.Grace; g != nil {
		p.line("  %s", p.dim(fmt.Sprintf("rollout: v%d still grades pull requests opened before this version, until %s",
			g.PreviousVersion, g.Until)))
	}
}

func versionSuffix(v int) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprintf(" v%d", v)
}

func printParity(p *printer, fs []domain.Finding) {
	errs := countSeverity(fs, domain.Error)
	switch {
	case len(fs) == 0:
		p.line("%s parity valid", p.pass())
	case errs == 0:
		p.line("%s parity: %s", p.caution(), plural(len(fs), "warning", "warnings"))
	default:
		p.line("%s parity: %s, %s", p.fail(), plural(errs, "error", "errors"),
			plural(len(fs)-errs, "warning", "warnings"))
	}
	if len(fs) > 0 {
		printFindings(p, fs, true)
	}
}

// printLayerGaps names the layers the run did not compute. A check that
// lost a layer in silence is the one behaviour a check may never have
// (RFC 0005 §4.4).
func printLayerGaps(p *printer, out checkJSON) {
	if len(out.Skipped) > 0 {
		p.line("%s %s", p.dim("·"), p.dim("not computed: "+layerList(out.Skipped)))
	}
	for _, u := range out.Unavailable {
		p.line("%s %s not checked: %s", p.caution(), u.Layer, u.Why)
	}
}

func printExplain(p *printer, es []explainJSON) {
	p.line("")
	p.line("%s", p.bold("Why each finding has its severity"))
	for i, e := range es {
		if i == maxFindingsPerGroup {
			p.line("  %s", p.dim(fmt.Sprintf("… and %d more (--json lists all)", len(es)-i)))
			return
		}
		rule := "no rule"
		if e.Rule != nil {
			rule = fmt.Sprintf("rule %d", e.Rule.Index)
		}
		p.line("  %s %s  %s → %s (%s)", e.Layer, e.Code, rule, e.Severity, e.Mode)
		p.line("    %s", p.dim(e.Why))
	}
}

func layerList(ls []domain.Layer) string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, string(l))
	}
	return strings.Join(out, ", ")
}

func localeSummary(l qualityapp.LocaleReport) string {
	var parts []string
	if l.Missing > 0 {
		parts = append(parts, fmt.Sprintf("%d missing", l.Missing))
	}
	if l.Outdated > 0 {
		parts = append(parts, fmt.Sprintf("%d outdated", l.Outdated))
	}
	if !l.Required && l.Missing > 0 {
		parts = append(parts, "not required")
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d of %d translated", l.Translated, l.Messages)
	}
	return strings.Join(parts, ", ")
}

func countSeverity(fs []domain.Finding, s domain.Severity) int {
	n := 0
	for _, f := range fs {
		if f.Severity == s {
			n++
		}
	}
	return n
}

func printFindings(p *printer, fs []domain.Finding, withLocale bool) {
	for i, f := range fs {
		if i == maxFindingsPerGroup {
			p.line("  %s", p.dim(fmt.Sprintf("… and %d more (--json lists all)", len(fs)-i)))
			return
		}
		label := f.Locus.Key
		if withLocale && f.Locus.Locale != "" {
			label = f.Locus.Locale + " " + f.Locus.Key
		}
		text := f.Message
		if f.Code != checkpolicy.CodeMissingTranslation {
			text = f.Code + ": " + f.Message
		}
		sev := p.bad("error")
		switch f.Severity {
		case domain.Warning:
			sev = p.warn("warn ")
		case domain.Waived:
			sev = p.dim("waive")
		}
		p.line("  %s %s  %s", sev, label, text)
	}
}
