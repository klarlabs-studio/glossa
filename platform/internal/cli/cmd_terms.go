package cli

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/qa"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/cli/terminology"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// ── output shapes (cmd/glossa/README.md, "JSON output") ─────────────

type termJSON struct {
	ID            string `json:"id"`
	Locale        string `json:"locale"`
	Text          string `json:"text"`
	Status        string `json:"status"`
	PartOfSpeech  string `json:"part_of_speech,omitempty"`
	CaseSensitive bool   `json:"case_sensitive"`
	Note          string `json:"note,omitempty"`
}

// conceptJSON is a termbase concept with its terms.
type conceptJSON struct {
	ID string `json:"id"`
	// ProjectID is null for a tenant-wide concept.
	ProjectID  *string    `json:"project_id"`
	Definition string     `json:"definition"`
	Domain     string     `json:"domain"`
	Note       string     `json:"note"`
	Version    int        `json:"version"`
	Terms      []termJSON `json:"terms"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func toConcept(c remote.TermConcept) conceptJSON {
	out := conceptJSON{ID: c.Id, ProjectID: c.ProjectId, Definition: c.Definition, Domain: c.Domain, Note: c.Note,
		Version: c.Version, Terms: []termJSON{}, UpdatedAt: c.UpdatedAt}
	for _, t := range c.Terms {
		tj := termJSON{ID: t.Id, Locale: t.Locale, Text: t.Text, Status: string(t.Status), CaseSensitive: t.CaseSensitive, Note: derefStr(t.Note)}
		if t.PartOfSpeech != nil {
			tj.PartOfSpeech = string(*t.PartOfSpeech)
		}
		out.Terms = append(out.Terms, tj)
	}
	return out
}

type termsListJSON struct {
	Schema   string        `json:"schema"`
	Concepts []conceptJSON `json:"concepts"`
}

type termsShowJSON struct {
	Schema  string      `json:"schema"`
	Concept conceptJSON `json:"concept"`
}

// termsChangeJSON is add, edit, deprecate and forbid.
type termsChangeJSON struct {
	Schema string `json:"schema"`
	// Action is created, updated, unchanged, deprecated or forbidden.
	Action  string      `json:"action"`
	Concept conceptJSON `json:"concept"`
}

type termsCheckJSON struct {
	Schema string `json:"schema"`
	FailOn string `json:"fail_on"`
	terminology.Report
	Passed bool `json:"passed"`
}

const termsUsage = `terms <action> [flags]

Actions:
  list [--query TEXT] [--locale L] [--domain D] [--limit N] [--all-projects]
                  concepts with their terms (this project's and the tenant-wide ones)
  show <concept-id | term>
                  one concept; a term is looked up by its text (--locale narrows it)
  add [<term>] [--locale L] [--preferred L=TEXT]... [--admitted L=TEXT]... [--deprecated L=TEXT]...
      [--forbidden L=TEXT]... [--definition TEXT] [--domain D] [--note TEXT] [--part-of-speech POS]
      [--case-sensitive] [--tenant-wide]
                  a concept: <term> is preferred in --locale (default: the source locale)
  edit <concept-id> [--definition --domain --note] [--preferred|--admitted|--deprecated|--forbidden L=TEXT]...
      [--remove L=TEXT]...
                  change a concept: listed terms are added or get the status
  deprecate <term> [--locale L] [--concept ID]
  forbid <term> [--locale L] [--concept ID]
                  mark a term deprecated (translations get a warning) or forbidden (an error);
                  with --concept the term is added when the concept lacks it
  check [--locale L]... [--states approved,needs_review,draft] [--fail-on error|warning]
                  terminology QA over the project's translations; exits 1 on errors
  export [--scope project|tenant] [-o PATH]
                  the termbase as TBX-Basic (glossa export --format tbx)
  import <file.tbx> [--apply | --overwrite] [--scope project|tenant]
                  TBX concepts into the termbase; a dry run without --apply (glossa import --format tbx)`

const termsExportUsage = `terms export [--scope project|tenant] [-o PATH] [--no-wait]

The termbase as TBX-Basic: the project's concepts (--scope tenant: every concept of the
tenant). Same as ` + "`glossa export --format tbx`" + `; exit codes: 0 ok, 3 refused or a corrupted download, 4 the job failed.`

const termsImportUsage = `terms import <file.tbx> [--apply | --overwrite] [--scope project|tenant] [--no-wait] [--all-results]

TBX concepts into the termbase, as the project's concepts (--scope tenant: tenant-wide).
Without --apply it is a dry run. A concept whose stored content differs is a conflict
(concept_differs; exit 1) unless --overwrite. Same as ` + "`glossa import --format tbx`" + `;
needs integration.manage (a write token).`

type termsArgs struct {
	action, ref, locale, query, domain, definition, note, partOfSpeech, concept, states, failOn string
	limit                                                                                       int
	allProjects, caseSensitive, tenantWide                                                      bool
	preferred, admitted, deprecated, forbidden, remove, locales                                 listFlag
	set                                                                                         map[string]bool
}

func parseTermsArgs(inv *invocation, args []string) (termsArgs, error) {
	fs := inv.flags(termsUsage)
	var a termsArgs
	fs.Var(&a.locales, "locale", "the term's locale (add: default the source locale); check: the locales to check (repeatable)")
	fs.StringVar(&a.query, "query", "", "list: search term texts and definitions")
	fs.StringVar(&a.domain, "domain", "", "list: only this domain; add, edit: the concept's domain")
	fs.StringVar(&a.definition, "definition", "", "add, edit: what the concept means")
	fs.StringVar(&a.note, "note", "", "add, edit: a note for translators")
	fs.StringVar(&a.partOfSpeech, "part-of-speech", "", "add: noun, proper_noun, verb, adjective, adverb, phrase or other")
	fs.StringVar(&a.concept, "concept", "", "deprecate, forbid: the concept's ID")
	fs.StringVar(&a.states, "states", "", "check: review states to check (default: all but rejected)")
	fs.StringVar(&a.failOn, "fail-on", "", "check: lowest severity that fails, error (default) or warning")
	fs.IntVar(&a.limit, "limit", 50, "list: at most this many concepts (0: all)")
	fs.BoolVar(&a.allProjects, "all-projects", false, "list: every concept of the tenant")
	fs.BoolVar(&a.caseSensitive, "case-sensitive", false, "add: match the terms case-sensitively")
	fs.BoolVar(&a.tenantWide, "tenant-wide", false, "add: for every project of the tenant, not only this one")
	fs.Var(&a.preferred, "preferred", "add, edit: a preferred term, <locale>=<text> (repeatable)")
	fs.Var(&a.admitted, "admitted", "add, edit: an admitted term, <locale>=<text> (repeatable)")
	fs.Var(&a.deprecated, "deprecated", "add, edit: a deprecated term, <locale>=<text> (repeatable)")
	fs.Var(&a.forbidden, "forbidden", "add, edit: a forbidden term, <locale>=<text> (repeatable)")
	fs.Var(&a.remove, "remove", "edit: remove a term, <locale>=<text> (repeatable)")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	a.set = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { a.set[f.Name] = true })
	if len(pos) == 0 {
		return a, usageError(inv.name, "missing action: list, show, add, edit, deprecate, forbid or check")
	}
	a.action, pos = pos[0], pos[1:]
	if a.action != "check" && len(a.locales) > 0 {
		if len(a.locales) > 1 || strings.Contains(a.locales[0], ",") {
			return a, usageError(inv.name, "%s takes one --locale", a.action)
		}
		if a.locale, err = normalizeLocale(inv, "--locale", a.locales[0]); err != nil {
			return a, err
		}
	}
	switch a.action {
	case "list", "check":
		return a, noMore(inv, pos)
	case "show", "deprecate", "forbid":
		if a.ref = joinArgs(pos); a.ref == "" {
			return a, usageError(inv.name, "%s takes a term (or a concept ID)", a.action)
		}
		return a, nil
	case "add":
		a.ref = joinArgs(pos)
		if a.ref == "" && len(a.preferred)+len(a.admitted)+len(a.deprecated)+len(a.forbidden) == 0 {
			return a, usageError(inv.name, "add takes a term, or --preferred/--admitted/--deprecated/--forbidden <locale>=<text>")
		}
		return a, nil
	case "edit":
		if len(pos) != 1 || !idPattern.MatchString(pos[0]) {
			return a, usageError(inv.name, "edit takes the concept's ID (`glossa terms list` shows it)")
		}
		a.ref = pos[0]
		return a, nil
	}
	return a, usageError(inv.name, "unknown action %q (list, show, add, edit, deprecate, forbid, check)", a.action)
}

func runTerms(ctx context.Context, inv *invocation, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "export":
			return runExportAs(ctx, inv, args[1:], termsExportUsage, "tbx")
		case "import":
			return runFileImport(ctx, inv, args[1:], termsImportUsage, "tbx")
		}
	}
	a, err := parseTermsArgs(inv, args)
	if err != nil {
		return err
	}
	if a.action == "check" {
		return inv.termsCheck(ctx, a)
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	switch a.action {
	case "list":
		return inv.termsList(ctx, p, a)
	case "show":
		c, err := inv.findConcept(ctx, p, a.ref, a.locale)
		if err != nil {
			return err
		}
		out := termsShowJSON{Schema: "glossa.cli.terms.show/v1", Concept: toConcept(c)}
		return inv.emit(out, func(pr *printer) { printConcept(pr, out.Concept) })
	case "add":
		return inv.termsAdd(ctx, p, a)
	case "edit":
		return inv.termsEdit(ctx, p, a)
	default:
		return inv.termsMark(ctx, p, a)
	}
}

func (inv *invocation) termsList(ctx context.Context, p *project, a termsArgs) error {
	f := remote.ConceptFilter{Query: a.query, Locale: a.locale, Domain: a.domain}
	if !a.allProjects {
		f.Project = p.scope.Project
	}
	if a.limit < 0 {
		return usageError(inv.name, "--limit must be 0 (all) or more")
	}
	cs, err := p.client.TermConcepts(ctx, p.scope.Tenant, f, a.limit)
	if err != nil {
		return inv.m2Error(err, "can't list the termbase")
	}
	out := termsListJSON{Schema: "glossa.cli.terms.list/v1", Concepts: []conceptJSON{}}
	for _, c := range cs {
		out.Concepts = append(out.Concepts, toConcept(c))
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Concepts) == 0 {
			pr.line("No concepts in the termbase%s.", map[bool]string{true: " match", false: ""}[a.query != ""])
			return
		}
		rows := [][]string{{"CONCEPT", "TERMS", "DOMAIN", "SCOPE"}}
		for _, c := range out.Concepts {
			rows = append(rows, []string{c.ID, termsSummary(c.Terms), dash(c.Domain), scopeLabel(c.ProjectID)})
		}
		pr.table(rows)
	})
}

func scopeLabel(project *string) string {
	if project == nil {
		return "tenant"
	}
	return "project"
}

// termsSummary is "Warenkorb (de) · cart (en) · basket (en, forbidden)".
func termsSummary(ts []termJSON) string {
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		label := t.Locale
		if t.Status != "preferred" {
			label += ", " + t.Status
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", t.Text, label))
	}
	return strings.Join(parts, " · ")
}

func printConcept(pr *printer, c conceptJSON) {
	pr.line("%s %s", pr.bold("Concept"), c.ID)
	for _, f := range []struct{ label, value string }{
		{"scope", scopeLabel(c.ProjectID)}, {"definition", c.Definition}, {"domain", c.Domain}, {"note", c.Note},
		{"version", fmt.Sprint(c.Version)},
	} {
		if f.value != "" {
			pr.line("  %-11s %s", f.label, f.value)
		}
	}
	rows := [][]string{{"  LOCALE", "TERM", "STATUS", "POS", "ID"}}
	for _, t := range c.Terms {
		status := t.Status
		switch t.Status {
		case "forbidden":
			status = pr.bad(status)
		case "deprecated":
			status = pr.warn(status)
		}
		rows = append(rows, []string{"  " + t.Locale, t.Text, status, dash(t.PartOfSpeech), t.ID})
	}
	pr.table(rows)
}

// findConcept resolves a concept ID or a term's text (in locale, when
// given) to exactly one concept.
func (inv *invocation) findConcept(ctx context.Context, p *project, ref, locale string) (remote.TermConcept, error) {
	if idPattern.MatchString(ref) {
		c, _, err := p.client.TermConcept(ctx, p.scope.Tenant, ref)
		if err != nil {
			return c, inv.m2Error(err, "can't read concept "+ref)
		}
		return c, nil
	}
	cs, err := p.client.TermConcepts(ctx, p.scope.Tenant, remote.ConceptFilter{Query: ref, Locale: locale, Project: p.scope.Project}, 0)
	if err != nil {
		return remote.TermConcept{}, inv.m2Error(err, "can't search the termbase")
	}
	var found []remote.TermConcept
	for _, c := range cs {
		if _, ok := findTerm(c.Terms, locale, ref); ok {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		where := "the termbase"
		if locale != "" {
			where = "the termbase (" + locale + ")"
		}
		return remote.TermConcept{}, &Error{Exit: ExitNetwork, Code: "term_not_found", What: fmt.Sprintf("no term %q", ref), Where: where,
			Fix: "`glossa terms list --query <text>` searches the termbase; `glossa terms add` adds a concept"}
	}
	ids := make([]string, len(found))
	for i, c := range found {
		ids[i] = c.Id
	}
	return remote.TermConcept{}, &Error{Exit: ExitUsage, Code: "term_ambiguous", What: fmt.Sprintf("%q is a term of %d concepts", ref, len(found)),
		Why: strings.Join(ids, ", "), Fix: "name the concept: pass its ID (or --concept <id>), or narrow with --locale"}
}

// findTerm finds the term with text (case-insensitively) in locale ("":
// any locale).
func findTerm(ts []remote.Term, locale, text string) (int, bool) {
	for i, t := range ts {
		if (locale == "" || t.Locale == locale) && strings.EqualFold(t.Text, text) {
			return i, true
		}
	}
	return -1, false
}

// termFlags are the --preferred, --admitted, --deprecated and
// --forbidden terms, in that order.
func (a termsArgs) termFlags(inv *invocation) ([]remote.TermInput, error) {
	var out []remote.TermInput
	for _, g := range []struct {
		flag   string
		values listFlag
		status remote.TermStatus
	}{
		{"--preferred", a.preferred, "preferred"}, {"--admitted", a.admitted, "admitted"},
		{"--deprecated", a.deprecated, "deprecated"}, {"--forbidden", a.forbidden, "forbidden"},
	} {
		for _, v := range g.values {
			l, text, err := localeText(inv, g.flag, v)
			if err != nil {
				return nil, err
			}
			st := g.status
			out = append(out, remote.TermInput{Locale: l, Text: text, Status: &st})
		}
	}
	return out, nil
}

func (inv *invocation) termsAdd(ctx context.Context, p *project, a termsArgs) error {
	terms, err := a.termFlags(inv)
	if err != nil {
		return err
	}
	if a.ref != "" {
		st := remote.TermStatus("preferred")
		terms = append([]remote.TermInput{{Locale: orDefault(a.locale, p.info.SourceLocale), Text: a.ref, Status: &st}}, terms...)
	}
	for i := range terms {
		if a.caseSensitive {
			terms[i].CaseSensitive = &a.caseSensitive
		}
		if a.partOfSpeech != "" {
			pos := remote.PartOfSpeech(a.partOfSpeech)
			terms[i].PartOfSpeech = &pos
		}
	}
	body := remote.CreateTermConcept{Terms: terms, Definition: optionalStr(a.definition), Domain: optionalStr(a.domain), Note: optionalStr(a.note)}
	if !a.tenantWide {
		body.ProjectId = &p.scope.Project
	}
	c, err := p.client.CreateTermConcept(ctx, p.scope.Tenant, body, newIdempotencyKey())
	if err != nil {
		return inv.m2Error(err, "can't add the concept")
	}
	return inv.emitConceptChange("created", c)
}

func optionalStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (inv *invocation) emitConceptChange(action string, c remote.TermConcept) error {
	out := termsChangeJSON{Schema: "glossa.cli.terms.change/v1", Action: action, Concept: toConcept(c)}
	return inv.emit(out, func(pr *printer) {
		switch action {
		case "unchanged":
			pr.line("%s Concept %s unchanged (version %d)", pr.pass(), c.Id, c.Version)
		default:
			pr.line("%s Concept %s %s (version %d): %s", pr.pass(), c.Id, action, c.Version, termsSummary(out.Concept.Terms))
		}
	})
}

// replaceBody is the concept as a replacement, to change.
func replaceBody(c remote.TermConcept) remote.ReplaceTermConcept {
	body := remote.ReplaceTermConcept{Definition: &c.Definition, Domain: &c.Domain, Note: &c.Note, ProductRef: &c.ProductRef,
		Terms: make([]remote.TermInput, len(c.Terms))}
	for i, t := range c.Terms {
		st, cs := t.Status, t.CaseSensitive
		body.Terms[i] = remote.TermInput{Locale: t.Locale, Text: t.Text, Status: &st, CaseSensitive: &cs, Note: t.Note, PartOfSpeech: t.PartOfSpeech}
	}
	return body
}

// setTerm gives the term in locale the status, adding it if missing.
func setTerm(body *remote.ReplaceTermConcept, locale, text string, status remote.TermStatus) {
	for i, t := range body.Terms {
		if t.Locale == locale && strings.EqualFold(t.Text, text) {
			body.Terms[i].Status = &status
			return
		}
	}
	body.Terms = append(body.Terms, remote.TermInput{Locale: locale, Text: text, Status: &status})
}

func (inv *invocation) replaceConcept(ctx context.Context, p *project, id string, change func(*remote.ReplaceTermConcept) error, action string) error {
	c, etag, err := p.client.TermConcept(ctx, p.scope.Tenant, id)
	if err != nil {
		return inv.m2Error(err, "can't read concept "+id)
	}
	body := replaceBody(c)
	if err := change(&body); err != nil {
		return err
	}
	updated, err := p.client.ReplaceTermConcept(ctx, p.scope.Tenant, id, etag, body)
	if err != nil {
		return inv.m2Error(err, "can't change concept "+id)
	}
	if updated.Version == c.Version {
		action = "unchanged"
	}
	return inv.emitConceptChange(action, updated)
}

func (inv *invocation) termsEdit(ctx context.Context, p *project, a termsArgs) error {
	terms, err := a.termFlags(inv)
	if err != nil {
		return err
	}
	return inv.replaceConcept(ctx, p, a.ref, func(body *remote.ReplaceTermConcept) error {
		if a.set["definition"] {
			body.Definition = &a.definition
		}
		if a.set["domain"] {
			body.Domain = &a.domain
		}
		if a.set["note"] {
			body.Note = &a.note
		}
		for _, t := range terms {
			setTerm(body, t.Locale, t.Text, *t.Status)
		}
		for _, v := range a.remove {
			l, text, err := localeText(inv, "--remove", v)
			if err != nil {
				return err
			}
			kept := body.Terms[:0]
			removed := false
			for _, t := range body.Terms {
				if t.Locale == l && strings.EqualFold(t.Text, text) {
					removed = true
					continue
				}
				kept = append(kept, t)
			}
			if !removed {
				return usageError(inv.name, "concept %s has no term %q in %s", a.ref, text, l)
			}
			body.Terms = kept
		}
		return nil
	}, "updated")
}

// termsMark deprecates or forbids a term.
func (inv *invocation) termsMark(ctx context.Context, p *project, a termsArgs) error {
	status, action := remote.TermStatus("deprecated"), "deprecated"
	if a.action == "forbid" {
		status, action = "forbidden", "forbidden"
	}
	id := a.concept
	if id == "" {
		c, err := inv.findConcept(ctx, p, a.ref, a.locale)
		if err != nil {
			return err
		}
		id = c.Id
	} else if !idPattern.MatchString(id) {
		return usageError(inv.name, "--concept takes the concept's ID (`glossa terms list` shows it)")
	}
	return inv.replaceConcept(ctx, p, id, func(body *remote.ReplaceTermConcept) error {
		locale := a.locale
		if locale == "" {
			var locales []string
			for _, t := range body.Terms {
				if strings.EqualFold(t.Text, a.ref) && !contains(locales, t.Locale) {
					locales = append(locales, t.Locale)
				}
			}
			switch len(locales) {
			case 0:
				return usageError(inv.name, "concept %s has no term %q: pass --locale to add it as %s", id, a.ref, status)
			case 1:
				locale = locales[0]
			default:
				return usageError(inv.name, "%q is a term in %s: pass --locale", a.ref, strings.Join(locales, ", "))
			}
		}
		setTerm(body, locale, a.ref, status)
		return nil
	}, action)
}

// ── terms check ─────────────────────────────────────────────────────

func (inv *invocation) termsCheck(ctx context.Context, a termsArgs) error {
	cfg, err := inv.loadConfig()
	if err != nil {
		return err
	}
	policy, _, err := checkPolicy(inv, cfg, checkpolicy.Policy{}, checkFlags{require: "none", failOn: a.failOn})
	if err != nil {
		return err
	}
	locales, err := normalizeLocales(inv, "--locale", a.locales)
	if err != nil {
		return err
	}
	var states []string
	if a.states != "" {
		for _, s := range strings.Split(a.states, ",") {
			s = strings.TrimSpace(s)
			if !contains([]string{"approved", "needs_review", "draft", "rejected"}, s) {
				return usageError(inv.name, "--states takes approved, needs_review, draft or rejected, not %q", s)
			}
			states = append(states, s)
		}
	}
	p, err := inv.connectWith(ctx, cfg)
	if err != nil {
		return err
	}
	ls, err := p.client.Locales(ctx, p.scope)
	if err != nil {
		return inv.apiError(err, "can't list locales")
	}
	var targets []string
	for _, l := range ls {
		if !l.IsSource {
			targets = append(targets, l.Code)
		}
	}
	for _, l := range locales {
		if !contains(targets, l) {
			return &Error{Exit: ExitUsage, Code: "locale_not_found", What: fmt.Sprintf("the project has no locale %s", l),
				Fix: "check --locale (`glossa locales` lists them)"}
		}
	}
	if len(locales) == 0 {
		locales = targets
	}
	report, err := inv.terminology(ctx, p, terminology.Options{Locales: locales, States: states})
	if err != nil {
		return err
	}
	passed := report.Errors == 0 && (policy.FailOn != qa.Warning || report.Warnings == 0)
	out := termsCheckJSON{Schema: "glossa.cli.terms.check/v1", FailOn: string(policy.FailOn), Report: report, Passed: passed}
	label := fmt.Sprintf("%s on %s", p.info.Slug, cfg.Server)
	if err := inv.emit(out, func(pr *printer) { printTermsCheck(pr, label, out) }); err != nil {
		return err
	}
	if !passed {
		return silentExit(ExitCheckFailed, "terms_check_failed")
	}
	return nil
}

// terminology has the server run terminology QA over the project's
// translations in opts' locales, a page at a time.
func (inv *invocation) terminology(ctx context.Context, p *project, opts terminology.Options) (terminology.Report, error) {
	report, err := terminology.Run(ctx, opts, func(ctx context.Context, q remote.TermFindingsQuery, fn func(remote.TermFindingsPage) error) error {
		return p.client.ProjectTermFindings(ctx, p.scope, q, fn)
	})
	if err != nil {
		return report, inv.m2Error(err, "can't check terminology")
	}
	return report, nil
}

func printTermsCheck(pr *printer, label string, out termsCheckJSON) {
	pr.line("%s %s checked against the termbase (%s)", pr.pass(), plural(out.Checked, "translation", "translations"), label)
	byLocale := map[string][]terminology.Finding{}
	for _, f := range out.Findings {
		byLocale[f.Locale] = append(byLocale[f.Locale], f)
	}
	for _, l := range out.Locales {
		fs := byLocale[l.Code]
		switch {
		case len(fs) == 0:
			pr.line("%s %s no findings", pr.pass(), l.Code)
		case l.Errors == 0:
			pr.line("%s %s %s", pr.caution(), l.Code, plural(l.Warnings, "warning", "warnings"))
		default:
			pr.line("%s %s %s, %s", pr.fail(), l.Code, plural(l.Errors, "error", "errors"), plural(l.Warnings, "warning", "warnings"))
		}
		sort.SliceStable(fs, func(i, j int) bool { return fs[i].Severity == "error" && fs[j].Severity != "error" })
		for i, f := range fs {
			if i == maxFindingsPerGroup {
				pr.line("  %s", pr.dim(fmt.Sprintf("… and %d more (--json lists all)", len(fs)-i)))
				break
			}
			sev := pr.bad("error")
			if f.Severity != "error" {
				sev = pr.warn("warn ")
			}
			msg := f.Code + ": " + f.Message
			if len(f.Suggestions) > 0 {
				msg += " (use " + strings.Join(quoteAll(f.Suggestions), " or ") + ")"
			}
			pr.line("  %s %s  %s", sev, f.Key, msg)
		}
	}
	pr.line("")
	if out.Passed {
		pr.line("%s", pr.ok("Terminology check passed."))
	} else {
		pr.line("%s", pr.bad(fmt.Sprintf("Terminology check failed: %s, %s.", plural(out.Errors, "error", "errors"), plural(out.Warnings, "warning", "warnings"))))
	}
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
