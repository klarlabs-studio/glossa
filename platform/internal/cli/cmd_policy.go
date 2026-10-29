package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// `glossa policy` (RFC 0005 §13 wave 6): the check policy as a file.
//
// The server is the source of truth (§4.2), and this does not change
// that: `export` and `import` are how the document becomes reviewable
// in a pull request without making the policy a property of a commit.
// A policy in Git that CI read would grade the same commit differently
// depending on which branch of glossa.yaml was checked out; a document
// exported, reviewed and imported grades everything the same and still
// leaves a diff someone approved.
//
// `diff` is the other half of that, and the reason the command exists
// at all. §4.3's failure mode is precise: someone adds `terminology:
// error`, and forty open pull requests go red for something their
// authors did not do. `diff` asks the server what the candidate would
// change — how many stored findings move, and how many open pull
// requests would newly fail, per rule — and prints it before anyone
// saves anything.

const (
	policyShowSchema   = "glossa.cli.policy/v1"
	policyDiffSchema   = "glossa.cli.policy.diff/v1"
	policyExportSchema = "glossa.cli.policy.export/v1"
	policyImportSchema = "glossa.cli.policy.import/v1"
)

const policyUsage = `policy <action> [flags]

Actions:
  show                        the policy in force, its version, and the rollout still running
  diff --file policy.yaml     what saving that document would change: the impact preview
                              (findings that move severity, and open pull requests that
                              would newly fail, per rule). Stores nothing.
  export [--file policy.yaml] the document as YAML (--json: as JSON); without --file, to stdout
  import --file policy.yaml   save it as the next version
                                --grace-days <n>  how long open pull requests keep grading
                                                  against the version they were opened under
                                                  (default 14; 0 pins nothing)
                                --dry-run         the impact preview again, storing nothing

--file - reads stdin (import, diff) or writes stdout (export).`

type policyArgs struct {
	action    string
	file      string
	graceDays *int
	dryRun    bool
}

func parsePolicyArgs(inv *invocation, args []string) (policyArgs, error) {
	fs := inv.flags(policyUsage)
	var a policyArgs
	fs.StringVar(&a.file, "file", "", "the policy document (YAML or JSON); - is stdin or stdout")
	grace := fs.Int("grace-days", checkpolicyDefaultGraceDays,
		"how long the save pins the pull requests that predate it (0 pins nothing)")
	fs.BoolVar(&a.dryRun, "dry-run", false, "import: answer the impact preview and store nothing")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, usageError(inv.name, "missing action: show, diff, export or import")
	}
	a.action = pos[0]
	if err := noMore(inv, pos[1:]); err != nil {
		return a, err
	}
	if isSet(fs, "grace-days") {
		if *grace < 0 {
			return a, usageError(inv.name, "--grace-days can't be negative")
		}
		a.graceDays = grace
	}
	switch a.action {
	case "show", "export":
		return a, nil
	case "diff", "import":
		if a.file == "" {
			return a, usageError(inv.name, "%s needs --file <policy.yaml> (- for stdin)", a.action)
		}
		return a, nil
	}
	return a, usageError(inv.name, "unknown action %q (show, diff, export, import)", a.action)
}

// checkpolicyDefaultGraceDays mirrors checkpolicy.DefaultGrace so the
// flag's help says a number rather than "the server's default". It is
// only the printed default: the flag is sent only when it was given,
// and the server decides otherwise.
const checkpolicyDefaultGraceDays = 14

func runPolicy(ctx context.Context, inv *invocation, args []string) error {
	a, err := parsePolicyArgs(inv, args)
	if err != nil {
		return err
	}
	// The file is read before the server is called: a document that
	// cannot be read is a configuration mistake, and finding that out
	// after a round trip helps nobody.
	var candidate checkpolicy.Policy
	if a.action == "diff" || a.action == "import" {
		if candidate, err = inv.readPolicyFile(a.file); err != nil {
			return err
		}
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	switch a.action {
	case "show":
		return inv.showPolicy(ctx, p)
	case "export":
		return inv.exportPolicy(ctx, p, a.file)
	case "diff":
		return inv.diffPolicy(ctx, p, a, candidate)
	}
	return inv.importPolicy(ctx, p, a, candidate)
}

// policyError says what a 404 on the policy resource means.
//
// `inv.connect` has already resolved the project, so a 404 here is not
// "check tenant and project": it is a server whose Quality context
// predates the policy endpoint. Saying the first would send someone to
// edit a glossa.yaml that is correct.
func (inv *invocation) policyError(err error, what string) error {
	var ae *remote.APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return &Error{Exit: ExitNetwork, Code: "no_check_policy",
			What: what, Where: ae.Method + " " + ae.URL,
			Why: "this server has no check policy for the project; its Quality context predates the endpoint",
			Fix: "upgrade glossa-server, or set the policy in Studio (Settings → Pull request check)"}
	}
	return inv.qualityError(err, what)
}

// ── show ────────────────────────────────────────────────────────────

type policyShowJSON struct {
	Schema  string `json:"schema"`
	Version int    `json:"version"`
	// Policy is the glossa.check-policy/v1 document, exactly as `export`
	// writes it and `import` reads it.
	Policy    checkpolicy.Policy `json:"policy"`
	CreatedBy string             `json:"created_by,omitempty"`
	CreatedAt string             `json:"created_at,omitempty"`
	// Grace is a rollout still in progress: two versions are live, and
	// the pull requests that predate this one grade against the other.
	Grace *graceJSON `json:"grace,omitempty"`
}

func (inv *invocation) showPolicy(ctx context.Context, p *project) error {
	st, err := p.client.CheckPolicyState(ctx, p.scope)
	if err != nil {
		return inv.policyError(err, "can't read the project's check policy")
	}
	out := policyShowJSON{Schema: policyShowSchema, Version: st.Policy.Version, Policy: documentOf(st.Policy)}
	out.CreatedBy = st.CreatedBy
	if !st.CreatedAt.IsZero() {
		out.CreatedAt = st.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if st.PinnedVersion > 0 && st.Policy.GraceUntil != nil {
		out.Grace = &graceJSON{
			PreviousVersion: st.PinnedVersion,
			Until:           st.Policy.GraceUntil.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	return inv.emit(out, func(pr *printer) { printPolicyShow(pr, out) })
}

// documentOf is the policy without the bookkeeping: what it says,
// which is what a file holds.
func documentOf(p checkpolicy.Policy) checkpolicy.Policy {
	doc := p
	doc.Version, doc.EffectiveFrom, doc.GraceUntil, doc.Previous = 0, nil, nil, nil
	if doc.Schema == "" {
		doc.Schema = checkpolicy.Schema
	}
	if doc.FailOn == "" {
		doc.FailOn = checkpolicy.Error
	}
	if doc.MissingTranslations == "" {
		doc.MissingTranslations = checkpolicy.Error
	}
	return doc
}

func printPolicyShow(p *printer, out policyShowJSON) {
	p.line("%s %s", p.bold("check policy v"+fmt.Sprint(out.Version)), p.dim(policyProvenance(out)))
	printPolicyBody(p, out.Policy)
	if g := out.Grace; g != nil {
		p.line("")
		p.line("%s %s", p.caution(), fmt.Sprintf(
			"rollout: v%d still grades pull requests opened before this version, until %s", g.PreviousVersion, g.Until))
	}
}

func policyProvenance(out policyShowJSON) string {
	switch {
	case out.Version == 0:
		return "(never saved: the project's defaults)"
	case out.CreatedBy != "" && out.CreatedAt != "":
		return "saved by " + out.CreatedBy + " at " + out.CreatedAt
	case out.CreatedAt != "":
		return "saved at " + out.CreatedAt
	}
	return ""
}

func printPolicyBody(p *printer, doc checkpolicy.Policy) {
	p.line("  %s %s", p.dim("require_complete:     "), requireCompleteText(doc.RequireComplete))
	p.line("  %s %s", p.dim("fail_on:              "), string(doc.FailOn))
	p.line("  %s %s", p.dim("missing_translations: "), string(doc.MissingTranslations))
	for _, name := range sortedKeys(doc.Environments) {
		p.line("  %s %s", p.dim("environment "+name+":"), environmentText(doc.Environments[name]))
	}
	if len(doc.Rules) == 0 {
		p.line("  %s", p.dim("no rules"))
		return
	}
	rows := [][]string{{"RULE", "SELECTS", "SEVERITY", "MODE"}}
	for i, r := range doc.Rules {
		rows = append(rows, []string{fmt.Sprint(i), selectorText(r.Selector), string(r.Severity), string(ruleMode(r))})
	}
	p.table(rows)
}

// requireCompleteText says the three answers apart. nil is not an empty
// list: "every locale" and "no locale" are opposite policies.
func requireCompleteText(required []string) string {
	switch {
	case required == nil:
		return "every locale"
	case len(required) == 0:
		return "no locale"
	}
	return strings.Join(required, ", ")
}

func environmentText(e checkpolicy.Environment) string {
	var parts []string
	if e.RequireComplete.Set {
		if e.RequireComplete.All {
			parts = append(parts, "require_complete: every locale")
		} else {
			parts = append(parts, "require_complete: "+requireCompleteText(e.RequireComplete.Locales))
		}
	}
	if e.RequireReview != "" {
		parts = append(parts, "require_review: "+e.RequireReview)
	}
	if len(parts) == 0 {
		return "(inherits the document)"
	}
	return strings.Join(parts, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ── export ──────────────────────────────────────────────────────────

type policyExportJSON struct {
	Schema string             `json:"schema"`
	File   string             `json:"file"`
	Policy checkpolicy.Policy `json:"policy"`
}

func (inv *invocation) exportPolicy(ctx context.Context, p *project, file string) error {
	doc, err := p.client.ExportCheckPolicy(ctx, p.scope)
	if err != nil {
		return inv.policyError(err, "can't export the project's check policy")
	}
	doc = documentOf(doc)
	if file == "" || file == "-" {
		// The document itself is the output: what comes out here is
		// exactly what `import --file -` reads back.
		if inv.json {
			return writeJSON(inv.env.Stdout, doc)
		}
		body, err := policyYAML(doc)
		if err != nil {
			return err
		}
		inv.out.line("%s", strings.TrimRight(string(body), "\n"))
		return nil
	}
	body, err := policyYAML(doc)
	if err != nil {
		return err
	}
	path := inv.resolvePath(file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return &Error{Exit: ExitUsage, Code: "cant_write_policy", What: "can't write the policy file",
			Where: path, Why: err.Error()}
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return &Error{Exit: ExitUsage, Code: "cant_write_policy", What: "can't write the policy file",
			Where: path, Why: err.Error()}
	}
	out := policyExportJSON{Schema: policyExportSchema, File: relPath(p.cfg, path), Policy: doc}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s wrote %s", pr.pass(), out.File)
		pr.line("  %s", pr.dim("review it, then `glossa policy import --file "+out.File+"`"))
	})
}

// policyYAML renders the document as YAML through its JSON form.
//
// The JSON form is the document: checkpolicy.Policy's tags are the
// wire's names, and require_complete's three answers (a list, an empty
// list, null) are spelled by its own marshaller. Going through it
// rather than giving the struct a second set of YAML tags is what keeps
// there being one spelling of the policy — a second one would be free
// to drift from the first.
func policyYAML(doc checkpolicy.Policy) ([]byte, error) {
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(body, &generic); err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(generic)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ── the file ────────────────────────────────────────────────────────

// resolvePath makes a path from the command line absolute, against the
// working directory rather than glossa.yaml's: the person typed it
// where they are standing.
func (inv *invocation) resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(inv.env.Dir, p)
}

// readPolicyFile reads a policy document from YAML or JSON.
func (inv *invocation) readPolicyFile(file string) (checkpolicy.Policy, error) {
	var (
		raw   []byte
		err   error
		where string
	)
	if file == "-" {
		where = "stdin"
		raw, err = io.ReadAll(io.LimitReader(inv.env.Stdin, 1<<20))
	} else {
		where = inv.resolvePath(file)
		raw, err = os.ReadFile(where) //nolint:gosec // a path the person named
	}
	if err != nil {
		return checkpolicy.Policy{}, &Error{Exit: ExitUsage, Code: "policy_file_unreadable",
			What: "can't read the policy file", Where: where, Why: err.Error(),
			Fix: "`glossa policy export --file policy.yaml` writes one to start from"}
	}
	return decodePolicyDocument(where, raw)
}

// decodePolicyDocument reads the document, YAML or JSON.
//
// It refuses a file that carries the bookkeeping — the version, when a
// version took effect, what it pins. Those are the server's: a file
// asserting `version: 7` is a misunderstanding of what a policy file
// is, and quietly dropping it would leave the author believing they had
// pinned a version.
func decodePolicyDocument(where string, raw []byte) (checkpolicy.Policy, error) {
	bad := func(why string) error {
		return &Error{Exit: ExitUsage, Code: "invalid_policy_file",
			What: "the policy file isn't a check-policy document", Where: where, Why: why,
			Fix: "the document is RFC 0005 §4.1's: schema, require_complete, fail_on, " +
				"missing_translations, environments, rules. `glossa policy export` writes one"}
	}
	var generic any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return checkpolicy.Policy{}, bad("not valid YAML or JSON: " + err.Error())
	}
	if generic == nil {
		return checkpolicy.Policy{}, bad("the file is empty")
	}
	body, err := json.Marshal(generic)
	if err != nil {
		return checkpolicy.Policy{}, bad(err.Error())
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var doc checkpolicy.Policy
	if err := dec.Decode(&doc); err != nil {
		return checkpolicy.Policy{}, bad(err.Error())
	}
	switch {
	case doc.Schema != "" && doc.Schema != checkpolicy.Schema:
		return checkpolicy.Policy{}, bad(fmt.Sprintf("schema is %q, not %q", doc.Schema, checkpolicy.Schema))
	case doc.Version != 0 || doc.EffectiveFrom != nil || doc.GraceUntil != nil || doc.Previous != nil:
		return checkpolicy.Policy{}, &Error{Exit: ExitUsage, Code: "invalid_policy_file",
			What: "the policy file carries the server's bookkeeping", Where: where,
			Why: "version, effective_from, grace_until and previous are the server's: it assigns a version to " +
				"every save and decides what a save pins",
			Fix: "remove them; `--grace-days` is how a save says what it pins"}
	}
	return documentOf(doc), nil
}

// ── diff ────────────────────────────────────────────────────────────

type policyChangeJSON struct {
	// What names the part of the document that changed, the way the
	// document names it: `fail_on`, `rules[2]`, `environments.production`.
	What string `json:"what"`
	From string `json:"from"`
	To   string `json:"to"`
}

// policyRuleImpactJSON is one rule's share of the preview.
type policyRuleImpactJSON struct {
	Rule     int    `json:"rule"`
	Selector string `json:"selector"`
	Severity string `json:"severity"`
	Mode     string `json:"mode"`
	// Matched are the findings this rule decided; Changed the ones it
	// decided differently from the policy in force; NewlyFailing the
	// ones it turned into a reason a run fails.
	Matched      int `json:"matched"`
	Changed      int `json:"changed"`
	NewlyFailing int `json:"newly_failing"`
}

// policyImpactJSON is §4.3's impact preview: what the candidate would
// change about what the project already has.
type policyImpactJSON struct {
	// Findings are the stored findings it was measured against, and Runs
	// the runs they came from — one per ref, the newest.
	Findings int `json:"findings"`
	Runs     int `json:"runs"`
	Raised   int `json:"raised"`
	Lowered  int `json:"lowered"`
	Silenced int `json:"silenced"`
	// NewlyFailing are findings that fail under the candidate and did
	// not before.
	NewlyFailing    int `json:"newly_failing"`
	NoLongerFailing int `json:"no_longer_failing"`
	// OpenPullRequests is the number of people who would wake up to a
	// red pull request they did not cause. It is the number that decides
	// whether this policy ships with a grace.
	OpenPullRequests int      `json:"open_pull_requests"`
	NewlyFailingRefs []string `json:"newly_failing_refs"`
	// PullRequests names the ones OpenPullRequests counts, so whoever
	// saves the policy can go and tell their authors.
	PullRequests        []policyPullRequestJSON `json:"newly_failing_pull_requests"`
	NoLongerFailingRefs []string                `json:"no_longer_failing_refs"`
	Rules               []policyRuleImpactJSON  `json:"rules"`
}

// policyPullRequestJSON is one open pull request the candidate would
// newly fail: its branch, its number, and where it is when the server
// knows.
type policyPullRequestJSON struct {
	Ref    string `json:"ref"`
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
}

type policyDiffJSON struct {
	Schema string `json:"schema"`
	// FromVersion is the version in force; From and To are the two
	// documents.
	FromVersion int                `json:"from_version"`
	From        checkpolicy.Policy `json:"from"`
	To          checkpolicy.Policy `json:"to"`
	Changes     []policyChangeJSON `json:"changes"`
	Impact      policyImpactJSON   `json:"impact"`
}

func (inv *invocation) diffPolicy(ctx context.Context, p *project, a policyArgs, candidate checkpolicy.Policy) error {
	current, err := p.client.CheckPolicyState(ctx, p.scope)
	if err != nil {
		return inv.qualityError(err, "can't read the project's check policy")
	}
	saved, err := p.client.SaveCheckPolicy(ctx, p.scope,
		remote.SavePolicy{Policy: candidate, GraceDays: a.graceDays, DryRun: true})
	if err != nil {
		return inv.qualityError(err, "can't preview the policy")
	}
	out := policyDiffJSON{
		Schema: policyDiffSchema, FromVersion: current.Policy.Version,
		From: documentOf(current.Policy), To: candidate,
		Changes: policyChanges(documentOf(current.Policy), candidate),
		Impact:  toPolicyImpactJSON(saved.Impact, candidate),
	}
	return inv.emit(out, func(pr *printer) { printPolicyDiff(pr, out) })
}

// policyChanges is the document diff: what a reviewer reads before the
// numbers tell them what it costs.
func policyChanges(from, to checkpolicy.Policy) []policyChangeJSON {
	out := []policyChangeJSON{}
	add := func(what, a, b string) {
		if a != b {
			out = append(out, policyChangeJSON{What: what, From: a, To: b})
		}
	}
	add("require_complete", requireCompleteText(from.RequireComplete), requireCompleteText(to.RequireComplete))
	add("fail_on", string(from.FailOn), string(to.FailOn))
	add("missing_translations", string(from.MissingTranslations), string(to.MissingTranslations))
	names := map[string]bool{}
	for _, m := range []map[string]checkpolicy.Environment{from.Environments, to.Environments} {
		for name := range m {
			names[name] = true
		}
	}
	for _, name := range sortedKeys(names) {
		a, b := "—", "—"
		if e, ok := from.Environments[name]; ok {
			a = environmentText(e)
		}
		if e, ok := to.Environments[name]; ok {
			b = environmentText(e)
		}
		add("environments."+name, a, b)
	}
	// Rules are compared by index, because the index is how the document
	// names a rule and how specificity ties are broken: inserting one in
	// the middle really does change what every later rule is.
	for i := 0; i < max(len(from.Rules), len(to.Rules)); i++ {
		add(fmt.Sprintf("rules[%d]", i), ruleText(from.Rules, i), ruleText(to.Rules, i))
	}
	return out
}

func ruleText(rules []checkpolicy.Rule, i int) string {
	if i >= len(rules) {
		return "—"
	}
	r := rules[i]
	return fmt.Sprintf("%s → %s (%s)", selectorText(r.Selector), r.Severity, ruleMode(r))
}

func toPolicyImpactJSON(im remote.CheckPolicyImpact, candidate checkpolicy.Policy) policyImpactJSON {
	out := policyImpactJSON{
		Findings: im.Findings, Runs: im.Runs, Raised: im.Raised, Lowered: im.Lowered,
		Silenced: im.Silenced, NewlyFailing: im.NewlyFailing, NoLongerFailing: im.NoLongerFailing,
		OpenPullRequests: im.OpenPullRequests, NewlyFailingRefs: derefList(im.NewlyFailingRefs),
		NoLongerFailingRefs: derefList(im.NoLongerFailingRefs),
		PullRequests:        []policyPullRequestJSON{},
		Rules:               make([]policyRuleImpactJSON, 0, len(im.Rules)),
	}
	if im.NewlyFailingPullRequests != nil {
		for _, pr := range *im.NewlyFailingPullRequests {
			row := policyPullRequestJSON{Ref: pr.Ref, Number: pr.Number}
			if pr.Url != nil {
				row.URL = *pr.Url
			}
			out.PullRequests = append(out.PullRequests, row)
		}
	}
	for _, r := range im.Rules {
		row := policyRuleImpactJSON{
			Rule: r.Rule, Matched: r.Matched, Changed: r.Changed, NewlyFailing: r.NewlyFailing,
			Selector: "everything",
		}
		// The rule as the candidate document writes it: the preview
		// names rules by index, and an index nobody can read back is a
		// number without a rule attached.
		if r.Rule >= 0 && r.Rule < len(candidate.Rules) {
			rule := candidate.Rules[r.Rule]
			row.Selector = selectorText(rule.Selector)
			row.Severity, row.Mode = string(rule.Severity), string(ruleMode(rule))
		}
		out.Rules = append(out.Rules, row)
	}
	return out
}

func printPolicyDiff(p *printer, out policyDiffJSON) {
	p.line("%s", p.bold(fmt.Sprintf("check policy v%d → the document you passed", out.FromVersion)))
	if len(out.Changes) == 0 {
		p.line("%s the document is the one in force: nothing would change", p.pass())
	} else {
		rows := [][]string{{"WHAT", "NOW", "WOULD BE"}}
		for _, c := range out.Changes {
			rows = append(rows, []string{c.What, c.From, c.To})
		}
		p.table(rows)
	}
	printPolicyImpact(p, out.Impact)
}

// printPolicyImpact is §4.3's number, said out loud. The one that
// matters is open_pull_requests: it is how many people would wake up to
// a red pull request they did not cause.
func printPolicyImpact(p *printer, im policyImpactJSON) {
	p.line("")
	p.line("%s %s", p.bold("Impact"), p.dim(fmt.Sprintf("measured against %s from %s",
		plural(im.Findings, "stored finding", "stored findings"), plural(im.Runs, "run", "runs"))))
	if im.Findings == 0 {
		p.line("  %s", p.dim("nothing has been checked yet, so there is nothing to measure against — "+
			"this is not the same as \"no impact\""))
		return
	}
	p.line("  %s", p.dim(fmt.Sprintf("%d raised · %d lowered · %d no longer computed", im.Raised, im.Lowered, im.Silenced)))
	if im.NewlyFailing == 0 {
		p.line("  %s no finding starts failing a run", p.pass())
	} else {
		p.line("  %s %s start failing a run", p.caution(), plural(im.NewlyFailing, "finding", "findings"))
	}
	if im.NoLongerFailing > 0 {
		p.line("  %s", p.dim(fmt.Sprintf("%s stop failing a run", plural(im.NoLongerFailing, "finding", "findings"))))
	}
	printNewlyFailing(p, im)
	if len(im.Rules) == 0 {
		return
	}
	rows := [][]string{{"RULE", "SELECTS", "SEVERITY", "MODE", "MATCHED", "CHANGED", "NEWLY FAILING"}}
	for _, r := range im.Rules {
		rows = append(rows, []string{fmt.Sprint(r.Rule), r.Selector, r.Severity, r.Mode,
			fmt.Sprint(r.Matched), fmt.Sprint(r.Changed), fmt.Sprint(r.NewlyFailing)})
	}
	p.table(rows)
}

// printNewlyFailing says who would wake up to a red pull request: each
// pull request by number, branch and address, then the refs that turn
// red with no pull request on them — somebody's branch all the same.
func printNewlyFailing(p *printer, im policyImpactJSON) {
	named := map[string]bool{}
	if im.OpenPullRequests > 0 {
		p.line("  %s %s would newly fail", p.fail(), plural(im.OpenPullRequests, "open pull request", "open pull requests"))
		prs := slices.Clone(im.PullRequests)
		slices.SortFunc(prs, func(a, b policyPullRequestJSON) int { return a.Number - b.Number })
		for _, pr := range prs {
			named[pr.Ref] = true
			line := fmt.Sprintf("#%d %s", pr.Number, pr.Ref)
			if pr.URL != "" {
				line += "  " + pr.URL
			}
			p.line("    %s", line)
		}
		p.line("    %s", p.dim("ship the rules in `mode: warn` first, or save with --grace-days, "+
			"so nobody is failed for something they did not do"))
	}
	var rest []string
	for _, ref := range im.NewlyFailingRefs {
		if !named[ref] {
			rest = append(rest, ref)
		}
	}
	switch {
	case len(rest) == 0:
	case len(named) > 0:
		p.line("  %s", p.dim("other refs that turn red: "+strings.Join(rest, ", ")))
	default:
		p.line("  %s", p.dim("refs that turn red: "+strings.Join(rest, ", ")))
	}
}

// ── import ──────────────────────────────────────────────────────────

type policyImportJSON struct {
	Schema string `json:"schema"`
	// DryRun says nothing was stored.
	DryRun bool   `json:"dry_run"`
	File   string `json:"file"`
	// Version is the version now in force; it is unchanged for a dry
	// run.
	Version int                `json:"version"`
	Policy  checkpolicy.Policy `json:"policy"`
	Impact  policyImpactJSON   `json:"impact"`
	// Grace is the rollout this save started, if it started one.
	Grace *graceJSON `json:"grace,omitempty"`
}

func (inv *invocation) importPolicy(ctx context.Context, p *project, a policyArgs, candidate checkpolicy.Policy) error {
	saved, err := p.client.ImportCheckPolicy(ctx, p.scope,
		remote.SavePolicy{Policy: candidate, GraceDays: a.graceDays, DryRun: a.dryRun})
	if err != nil {
		return inv.qualityError(err, "can't import the check policy")
	}
	out := policyImportJSON{
		Schema: policyImportSchema, DryRun: saved.DryRun, File: a.file,
		Version: saved.State.Policy.Version, Policy: documentOf(saved.State.Policy),
		Impact: toPolicyImpactJSON(saved.Impact, candidate),
	}
	if saved.State.PinnedVersion > 0 && saved.State.Policy.GraceUntil != nil {
		out.Grace = &graceJSON{
			PreviousVersion: saved.State.PinnedVersion,
			Until:           saved.State.Policy.GraceUntil.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	return inv.emit(out, func(pr *printer) { printPolicyImport(pr, out) })
}

func printPolicyImport(p *printer, out policyImportJSON) {
	if out.DryRun {
		p.line("%s %s", p.caution(), p.bold("dry run: nothing was stored"))
	} else {
		p.line("%s check policy saved as v%d", p.pass(), out.Version)
	}
	printPolicyBody(p, out.Policy)
	if g := out.Grace; g != nil {
		p.line("  %s", p.dim(fmt.Sprintf(
			"rollout: v%d still grades pull requests opened before this save, until %s", g.PreviousVersion, g.Until)))
	}
	printPolicyImpact(p, out.Impact)
}
