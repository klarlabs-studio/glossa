package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// ── output shapes (cmd/glossa/README.md, "JSON output") ─────────────

// tmUnitJSON is a translation-memory unit.
type tmUnitJSON struct {
	ID           string `json:"id"`
	SourceLocale string `json:"source_locale"`
	TargetLocale string `json:"target_locale"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	// ProjectID is null for a tenant-wide unit.
	ProjectID     *string    `json:"project_id"`
	MessageKey    string     `json:"message_key,omitempty"`
	Namespace     string     `json:"namespace,omitempty"`
	Origin        string     `json:"origin"`
	State         string     `json:"state"`
	HitCount      int        `json:"hit_count"`
	CreatedAt     time.Time  `json:"created_at"`
	RetiredAt     *time.Time `json:"retired_at,omitempty"`
	RetiredReason string     `json:"retired_reason,omitempty"`
}

func toTMUnit(u remote.TMUnit) tmUnitJSON {
	out := tmUnitJSON{ID: u.Id, SourceLocale: u.SourceLocale, TargetLocale: u.TargetLocale, Source: u.Source, Target: u.Target,
		ProjectID: u.ProjectId, MessageKey: derefStr(u.MessageKey), Namespace: derefStr(u.Namespace), Origin: string(u.Origin),
		State: string(u.State), HitCount: u.HitCount, CreatedAt: u.CreatedAt, RetiredAt: u.RetiredAt}
	if u.RetiredReason != nil {
		out.RetiredReason = string(*u.RetiredReason)
	}
	return out
}

type tmMatchJSON struct {
	Score int    `json:"score"`
	Kind  string `json:"kind"` // exact, context (101) or fuzzy
	// Target is the unit's target with the query's variable names, in
	// MF2.
	Target string `json:"target"`
	// TargetText is the same target in the query's syntax (TargetSyntax):
	// MF1 when it can express it, else MF2.
	TargetText       string     `json:"target_text"`
	TargetSyntax     string     `json:"target_syntax"`
	VariablesAdapted bool       `json:"variables_adapted"`
	Unit             tmUnitJSON `json:"unit"`
}

type tmQueryJSON struct {
	Text   string `json:"text"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Syntax string `json:"syntax,omitempty"`
	Side   string `json:"side,omitempty"`
}

type tmSearchJSON struct {
	Schema           string        `json:"schema"`
	Query            tmQueryJSON   `json:"query"`
	SourceNormalized string        `json:"source_normalized"`
	Matches          []tmMatchJSON `json:"matches"`
}

type tmConcordanceMatchJSON struct {
	Similarity float32    `json:"similarity"`
	Unit       tmUnitJSON `json:"unit"`
}

type tmConcordanceJSON struct {
	Schema  string                   `json:"schema"`
	Query   tmQueryJSON              `json:"query"`
	Matches []tmConcordanceMatchJSON `json:"matches"`
}

type tmUnitsJSON struct {
	Schema string       `json:"schema"`
	Units  []tmUnitJSON `json:"units"`
}

type tmRetireJSON struct {
	Schema string     `json:"schema"`
	Unit   tmUnitJSON `json:"unit"`
}

const tmUsage = `tm <action> [flags]

Actions:
  search <text> --to LOCALE [--from LOCALE] [--min-score N] [--limit N] [--syntax mf1|mf2] [--all-projects]
                  exact and fuzzy matches for a message, best first (--from: the source locale)
  concordance <text> [--from LOCALE] [--to LOCALE] [--side source|target] [--limit N] [--all-projects]
                  units whose text contains a phrase
  units [--locale-pair de:en] [--state active|retired|all] [--limit N] [--all-projects]
                  the project's units (derived from approved translations)
  units --retire <id>
                  take a unit out of matching (it stays listed as history)
  export [--locale L]... [--source-locale L] [--scope project|tenant] [-o PATH]
                  the units as TMX 1.4b (glossa export --format tmx)
  import <file.tmx> [--apply] [--scope project|tenant]
                  TMX units into the memory; a dry run without --apply (glossa import --format tmx)

Lookups see tenant-wide units and the project's; --all-projects widens them to the tenant.`

const tmExportUsage = `tm export [--locale L]... [--source-locale L] [--scope project|tenant] [-o PATH] [--no-wait]

The translation memory as TMX 1.4b: the project's units (--scope tenant: every unit of the
tenant), those with --locale targets and a --source-locale when given. Same as
` + "`glossa export --format tmx`" + `; exit codes: 0 ok, 3 refused or a corrupted download, 4 the job failed.`

const tmImportUsage = `tm import <file.tmx> [--apply] [--scope project|tenant] [--no-wait] [--all-results]

TMX units into the translation memory, as the project's units (--scope tenant: tenant-wide).
Without --apply it is a dry run. A unit whose text the scope already has is unchanged: TM
imports only add. Same as ` + "`glossa import --format tmx`" + `; needs integration.manage (a write token).`

// tmTransfer runs `tm export` and `tm import`; ok is false for the
// other actions.
func tmTransfer(ctx context.Context, inv *invocation, args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "export":
		return true, runExportAs(ctx, inv, args[1:], tmExportUsage, "tmx")
	case "import":
		return true, runFileImport(ctx, inv, args[1:], tmImportUsage, "tmx")
	}
	return false, nil
}

type tmArgs struct {
	action, text, from, to, syntax, side, state, localePair, retire string
	minScore, limit                                                 int
	allProjects, limitSet                                           bool
}

func parseTMArgs(inv *invocation, args []string) (tmArgs, error) {
	fs := inv.flags(tmUsage)
	var a tmArgs
	fs.StringVar(&a.from, "from", "", "the source locale (default: the project's)")
	fs.StringVar(&a.to, "to", "", "the target locale")
	fs.StringVar(&a.syntax, "syntax", "", "search: the text's syntax, mf1 (ICU) or mf2 (default: glossa.yaml's syntax)")
	fs.StringVar(&a.side, "side", "source", "concordance: search the source or the target text")
	fs.StringVar(&a.state, "state", "active", "units: active, retired or all")
	fs.StringVar(&a.localePair, "locale-pair", "", "units: <source>:<target>, e.g. de:en")
	fs.StringVar(&a.retire, "retire", "", "units: the ID of the unit to retire")
	fs.IntVar(&a.minScore, "min-score", 0, "search: the lowest score to return, 50-101 (default: the server's)")
	fs.IntVar(&a.limit, "limit", 0, "at most this many results (search default 10, concordance 20, units 50; units 0: all)")
	fs.BoolVar(&a.allProjects, "all-projects", false, "look at every project's units, not only this project's")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, usageError(inv.name, "missing action: search, concordance or units")
	}
	a.action, pos = pos[0], pos[1:]
	a.limitSet = isSet(fs, "limit")
	if a.limit < 0 {
		return a, usageError(inv.name, "--limit must be 0 or more")
	}
	switch a.action {
	case "search", "concordance":
		if a.text = joinArgs(pos); a.text == "" {
			return a, usageError(inv.name, "%s takes the text to look for", a.action)
		}
		if a.action == "search" && a.to == "" {
			return a, usageError(inv.name, "search needs --to <locale>, the language you want the text in")
		}
		if a.side != "source" && a.side != "target" {
			return a, usageError(inv.name, "--side must be source or target, not %q", a.side)
		}
		return a, nil
	case "units":
		if a.state != "active" && a.state != "retired" && a.state != "all" {
			return a, usageError(inv.name, "--state must be active, retired or all, not %q", a.state)
		}
		return a, noMore(inv, pos)
	}
	return a, usageError(inv.name, "unknown action %q (search, concordance, units)", a.action)
}

func runTM(ctx context.Context, inv *invocation, args []string) error {
	if ok, err := tmTransfer(ctx, inv, args); ok {
		return err
	}
	a, err := parseTMArgs(inv, args)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	switch a.action {
	case "search":
		return inv.tmSearch(ctx, p, a)
	case "concordance":
		return inv.tmConcordance(ctx, p, a)
	}
	if a.retire != "" {
		return inv.tmRetire(ctx, p, a.retire)
	}
	return inv.tmUnits(ctx, p, a)
}

// locales normalizes --from and --to; from defaults to def.
func (a tmArgs) locales(inv *invocation, def string) (string, string, error) {
	from, to := orDefault(a.from, def), a.to
	var err error
	if from != "" {
		if from, err = normalizeLocale(inv, "--from", from); err != nil {
			return "", "", err
		}
	}
	if to != "" {
		if to, err = normalizeLocale(inv, "--to", to); err != nil {
			return "", "", err
		}
	}
	return from, to, nil
}

func (inv *invocation) tmSearch(ctx context.Context, p *project, a tmArgs) error {
	from, to, err := a.locales(inv, p.info.SourceLocale)
	if err != nil {
		return err
	}
	syntax := orDefault(a.syntax, orDefault(p.cfg.Syntax, "mf1"))
	if syntax != "mf1" && syntax != "mf2" {
		return usageError(inv.name, "--syntax must be mf1 or mf2, not %q", syntax)
	}
	limit := a.limit
	if limit == 0 {
		limit = 10
	}
	st := remote.Syntax(syntax)
	q := remote.TMLookup{Source: a.text, SourceLocale: from, TargetLocale: to, Syntax: &st, Limit: &limit, ProjectId: &p.scope.Project}
	if a.minScore > 0 {
		q.MinScore = &a.minScore
	}
	if a.allProjects {
		q.AllProjects = &a.allProjects
	}
	res, err := p.client.LookupTM(ctx, p.scope.Tenant, q)
	if err != nil {
		return inv.m2Error(err, "can't search the translation memory")
	}
	out := tmSearchJSON{Schema: "glossa.cli.tm.search/v1", Query: tmQueryJSON{Text: a.text, From: from, To: to, Syntax: syntax},
		SourceNormalized: res.SourceNormalized, Matches: []tmMatchJSON{}}
	for _, m := range res.Matches {
		mj := tmMatchJSON{Score: m.Score, Kind: string(m.Kind), Target: m.Target, TargetText: m.TargetText,
			TargetSyntax: string(m.TargetSyntax), VariablesAdapted: m.VariablesAdapted, Unit: toTMUnit(m.Unit)}
		if mj.TargetText == "" { // a server without target_text
			mj.TargetText, mj.TargetSyntax = m.Target, "mf2"
		}
		out.Matches = append(out.Matches, mj)
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Matches) == 0 {
			pr.line("No translation-memory matches for %q (%s → %s).", a.text, from, to)
			return
		}
		pr.line("%s for %q (%s → %s)", plural(len(out.Matches), "match", "matches"), a.text, from, to)
		rows := [][]string{{"SCORE", "KIND", "TARGET", "SOURCE", "UNIT"}}
		for _, m := range out.Matches {
			rows = append(rows, []string{strconv.Itoa(m.Score), m.Kind, m.TargetText, m.Unit.Source, unitLabel(m.Unit)})
		}
		pr.table(rows)
	})
}

// unitLabel names a unit's origin for humans.
func unitLabel(u tmUnitJSON) string {
	if u.MessageKey != "" {
		return u.MessageKey + " · " + u.ID
	}
	return u.ID
}

func (inv *invocation) tmConcordance(ctx context.Context, p *project, a tmArgs) error {
	from, to, err := a.locales(inv, "")
	if err != nil {
		return err
	}
	limit := a.limit
	if limit == 0 {
		limit = 20
	}
	matches, err := p.client.Concordance(ctx, p.scope.Tenant, remote.ConcordanceQuery{Text: a.text, Side: a.side,
		SourceLocale: from, TargetLocale: to, Project: p.scope.Project, AllProjects: a.allProjects, Limit: limit})
	if err != nil {
		return inv.m2Error(err, "can't search the translation memory")
	}
	out := tmConcordanceJSON{Schema: "glossa.cli.tm.concordance/v1", Query: tmQueryJSON{Text: a.text, From: from, To: to, Side: a.side},
		Matches: []tmConcordanceMatchJSON{}}
	for _, m := range matches {
		out.Matches = append(out.Matches, tmConcordanceMatchJSON{Similarity: m.Similarity, Unit: toTMUnit(m.Unit)})
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Matches) == 0 {
			pr.line("No units contain %q.", a.text)
			return
		}
		pr.line("%s containing %q (%s)", plural(len(out.Matches), "unit", "units"), a.text, a.side)
		rows := [][]string{{"PAIR", "SOURCE", "TARGET", "UNIT"}}
		for _, m := range out.Matches {
			rows = append(rows, []string{m.Unit.SourceLocale + "→" + m.Unit.TargetLocale, m.Unit.Source, m.Unit.Target, unitLabel(m.Unit)})
		}
		pr.table(rows)
	})
}

func (inv *invocation) tmUnits(ctx context.Context, p *project, a tmArgs) error {
	f := remote.UnitFilter{State: a.state}
	if !a.allProjects {
		f.Project = p.scope.Project
	}
	if a.localePair != "" {
		src, tgt, ok := strings.Cut(a.localePair, ":")
		if !ok {
			return usageError(inv.name, "--locale-pair takes <source>:<target>, e.g. de:en; got %q", a.localePair)
		}
		var err error
		if f.SourceLocale, err = normalizeLocale(inv, "--locale-pair", src); err != nil {
			return err
		}
		if f.TargetLocale, err = normalizeLocale(inv, "--locale-pair", tgt); err != nil {
			return err
		}
	}
	limit := a.limit
	if !a.limitSet {
		limit = 50
	}
	units, err := p.client.TMUnits(ctx, p.scope.Tenant, f, limit)
	if err != nil {
		return inv.m2Error(err, "can't list translation-memory units")
	}
	out := tmUnitsJSON{Schema: "glossa.cli.tm.units/v1", Units: []tmUnitJSON{}}
	for _, u := range units {
		out.Units = append(out.Units, toTMUnit(u))
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Units) == 0 {
			pr.line("No %s translation-memory units.", a.state)
			return
		}
		rows := [][]string{{"ID", "PAIR", "SOURCE", "TARGET", "KEY", "STATE", "HITS"}}
		for _, u := range out.Units {
			rows = append(rows, []string{u.ID, u.SourceLocale + "→" + u.TargetLocale, u.Source, u.Target, dash(u.MessageKey),
				u.State, strconv.Itoa(u.HitCount)})
		}
		pr.table(rows)
	})
}

func (inv *invocation) tmRetire(ctx context.Context, p *project, id string) error {
	if err := p.client.RetireTMUnit(ctx, p.scope.Tenant, id); err != nil {
		return inv.m2Error(err, "can't retire unit "+id)
	}
	u, err := p.client.TMUnit(ctx, p.scope.Tenant, id)
	if err != nil {
		return inv.m2Error(err, "can't read unit "+id)
	}
	out := tmRetireJSON{Schema: "glossa.cli.tm.retire/v1", Unit: toTMUnit(u)}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Retired %s: %q → %q is no longer matched %s", pr.pass(), id, u.Source, u.Target,
			pr.dim(fmt.Sprintf("(%s)", orDefault(out.Unit.RetiredReason, "retired"))))
	})
}
