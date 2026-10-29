package cli

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/cli/snapshot"
)

// ── status ──────────────────────────────────────────────────────────

type localeStatus struct {
	Code        string  `json:"code"`
	Direction   string  `json:"direction"`
	IsSource    bool    `json:"is_source"`
	Translated  int     `json:"translated"`
	Approved    int     `json:"approved"`
	NeedsReview int     `json:"needs_review"`
	Draft       int     `json:"draft"`
	Rejected    int     `json:"rejected"`
	Outdated    int     `json:"outdated"`
	Missing     int     `json:"missing"`
	Coverage    float64 `json:"coverage"`
}

type statusJSON struct {
	Schema   string         `json:"schema"`
	Origin   string         `json:"origin"`
	Messages int            `json:"messages"`
	Locales  []localeStatus `json:"locales"`
}

func runStatus(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("status [--offline] | status --quality [--locale L] [--environment E] [--since T]")
	offline := fs.Bool("offline", false, "count the local catalogs instead of the server's project")
	quality := fs.Bool("quality", false,
		"print the seven quality numbers instead of coverage (RFC 0005 §8): the same ones the dashboard shows")
	locale := fs.String("locale", "", "--quality: narrow the per-locale numbers to this locale")
	environment := fs.String("environment", "", "--quality: where \"published\" means, for the lead time (default: production)")
	since := fs.String("since", "", "--quality: the start of the window, RFC 3339 or YYYY-MM-DD (default: 30 days ago)")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	if *quality {
		// The seven numbers are the server's: they are computed from six
		// contexts' tables, and the local catalogs know about one of
		// them. An --offline answer would be a different, smaller
		// question wearing the same name.
		if *offline {
			return usageError(inv.name, "--quality reads the server's summary: drop --offline")
		}
		q := remote.SummaryQuery{Environment: *environment}
		var err error
		if *locale != "" {
			if q.Locale, err = normalizeLocale(inv, "--locale", *locale); err != nil {
				return err
			}
		}
		if *since != "" {
			if q.Since, err = parseSince(inv, *since); err != nil {
				return err
			}
		}
		return inv.statusQuality(ctx, q)
	}
	cfg, err := inv.loadConfig()
	if err != nil {
		return err
	}
	var (
		out   statusJSON
		label string
	)
	if *offline {
		s, l, err := inv.snapshot(ctx, cfg, true, snapshot.Options{})
		if err != nil {
			return err
		}
		out, label = statusJSON{Origin: s.Origin, Messages: len(s.Messages), Locales: coverage(s)}, l
	} else if out, label, err = inv.serverStatus(ctx, cfg); err != nil {
		return err
	}
	out.Schema = "glossa.cli.status/v1"
	return inv.emit(out, func(p *printer) {
		p.line("%s %s", p.bold(label), p.dim(plural(out.Messages, "message", "messages")))
		rows := [][]string{{"LOCALE", "TRANSLATED", "APPROVED", "REVIEW", "DRAFT", "OUTDATED", "MISSING", "COVERAGE"}}
		for _, l := range out.Locales {
			code := l.Code
			if l.IsSource {
				code += " (source)"
			}
			rows = append(rows, []string{code, strconv.Itoa(l.Translated), strconv.Itoa(l.Approved), strconv.Itoa(l.NeedsReview),
				strconv.Itoa(l.Draft), strconv.Itoa(l.Outdated), strconv.Itoa(l.Missing), fmt.Sprintf("%.1f%%", l.Coverage*100)})
		}
		p.table(rows)
	})
}

// parseSince reads --since: a date, or an RFC 3339 timestamp. A date
// is its start, because a window that begins on the 1st begins at the
// beginning of the 1st.
func parseSince(inv *invocation, v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	if d, err := time.Parse(time.DateOnly, v); err == nil {
		return d.UTC(), nil
	}
	return time.Time{}, usageError(inv.name, "--since takes a date (2026-09-01) or an RFC 3339 timestamp, not %q", v)
}

// serverStatus reads the project's per-locale stats: one request,
// computed by the server, instead of every translation.
func (inv *invocation) serverStatus(ctx context.Context, cfg *config.Config) (statusJSON, string, error) {
	p, err := inv.connectWith(ctx, cfg)
	if err != nil {
		return statusJSON{}, "", err
	}
	st, err := p.client.TranslationStats(ctx, p.scope)
	if err != nil {
		return statusJSON{}, "", inv.apiError(err, "can't read the project's translation stats")
	}
	out := statusJSON{Origin: snapshot.FromServerOrigin, Messages: st.Messages, Locales: make([]localeStatus, 0, len(st.Locales))}
	for _, l := range st.Locales {
		ls := localeStatus{Code: l.Code, Direction: string(l.Direction), IsSource: l.IsSource, Translated: l.Translated,
			Approved: l.States.Approved, NeedsReview: l.States.NeedsReview, Draft: l.States.Draft, Rejected: l.States.Rejected,
			Outdated: l.Outdated, Missing: l.Missing, Coverage: 1}
		if st.Messages > 0 {
			ls.Coverage = float64(l.Translated) / float64(st.Messages)
		}
		out.Locales = append(out.Locales, ls)
	}
	sort.SliceStable(out.Locales, func(i, j int) bool {
		a, b := out.Locales[i], out.Locales[j]
		if a.IsSource != b.IsSource {
			return a.IsSource
		}
		return a.Code < b.Code
	})
	return out, fmt.Sprintf("%s on %s", p.info.Slug, cfg.Server), nil
}

// coverage counts review states per locale. Coverage is the share of
// active messages with a usable (not rejected) translation.
func coverage(s *snapshot.Snapshot) []localeStatus {
	var out []localeStatus
	total := len(s.Messages)
	for _, l := range s.Locales {
		st := localeStatus{Code: l.Code, Direction: l.Direction, IsSource: l.IsSource}
		if l.IsSource {
			st.Translated, st.Approved, st.Coverage = total, total, 1
			out = append(out, st)
			continue
		}
		for _, m := range s.Messages {
			t, ok := s.Translations[l.Code][m.Key]
			if !ok {
				st.Missing++
				continue
			}
			switch t.State {
			case "approved":
				st.Approved++
			case "needs_review":
				st.NeedsReview++
			case "draft":
				st.Draft++
			case "rejected":
				st.Rejected++
				st.Missing++
				continue
			}
			st.Translated++
			if t.Outdated {
				st.Outdated++
			}
		}
		if total > 0 {
			st.Coverage = float64(st.Translated) / float64(total)
		}
		out = append(out, st)
	}
	return out
}

// ── diff ────────────────────────────────────────────────────────────

type changedJSON struct {
	Key    string `json:"key"`
	Local  string `json:"local"`
	Server string `json:"server"`
}

type diffSetJSON struct {
	Locale string `json:"locale"`
	// Added are local only (push would create them).
	Added []string `json:"added"`
	// Changed differ in their canonical model.
	Changed []changedJSON `json:"changed"`
	// Removed are on the server only.
	Removed   []string `json:"removed"`
	Unchanged int      `json:"unchanged"`
}

type diffJSON struct {
	Schema       string        `json:"schema"`
	Source       diffSetJSON   `json:"source"`
	Translations []diffSetJSON `json:"translations"`
	Identical    bool          `json:"identical"`
}

func runDiff(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("diff [--exit-code]")
	exitCode := fs.Bool("exit-code", false, "exit 1 when local and server differ (like git diff --exit-code)")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	cfg, err := inv.loadConfig()
	if err != nil {
		return err
	}
	local, err := loadLocal(cfg)
	if err != nil {
		return err
	}
	server, _, err := inv.snapshot(ctx, cfg, false, snapshot.Options{})
	if err != nil {
		return err
	}
	out := diffSnapshots(local, server)
	if err := inv.emit(out, func(p *printer) { printDiff(p, out) }); err != nil {
		return err
	}
	if *exitCode && !out.Identical {
		return silentExit(ExitCheckFailed, "differences")
	}
	return nil
}

// comparable is one side of a diff: key → text and canonical model.
type comparable map[string]struct{ text, model string }

func diffSnapshots(local, server *snapshot.Snapshot) diffJSON {
	src := func(s *snapshot.Snapshot) comparable {
		c := comparable{}
		for _, m := range s.Messages {
			c[m.Key] = struct{ text, model string }{m.Text, snapshot.ModelJSON(m.Model)}
		}
		return c
	}
	trs := func(s *snapshot.Snapshot, locale string) comparable {
		c := comparable{}
		for k, t := range s.Translations[locale] {
			c[k] = struct{ text, model string }{t.Text, snapshot.ModelJSON(t.Model)}
		}
		return c
	}
	out := diffJSON{Schema: "glossa.cli.diff/v1", Source: diffSet(local.SourceLocale, src(local), src(server)), Translations: []diffSetJSON{}}
	identical := out.Source.identical()
	for _, l := range local.TargetLocales() {
		if _, onServer := server.Translations[l.Code]; !onServer {
			continue
		}
		d := diffSet(l.Code, trs(local, l.Code), trs(server, l.Code))
		identical = identical && d.identical()
		out.Translations = append(out.Translations, d)
	}
	out.Identical = identical
	return out
}

func (d diffSetJSON) identical() bool {
	return len(d.Added) == 0 && len(d.Changed) == 0 && len(d.Removed) == 0
}

func diffSet(locale string, local, server comparable) diffSetJSON {
	d := diffSetJSON{Locale: locale, Added: []string{}, Changed: []changedJSON{}, Removed: []string{}}
	for k, l := range local {
		s, ok := server[k]
		switch {
		case !ok:
			d.Added = append(d.Added, k)
		case l.model != s.model:
			d.Changed = append(d.Changed, changedJSON{Key: k, Local: l.text, Server: s.text})
		default:
			d.Unchanged++
		}
	}
	for k := range server {
		if _, ok := local[k]; !ok {
			d.Removed = append(d.Removed, k)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Slice(d.Changed, func(i, j int) bool { return d.Changed[i].Key < d.Changed[j].Key })
	return d
}

func printDiff(p *printer, out diffJSON) {
	sets := append([]diffSetJSON{out.Source}, out.Translations...)
	for i, d := range sets {
		title := d.Locale + " (source)"
		if i > 0 {
			title = d.Locale
		}
		p.line("%s  %s", p.bold(title), p.dim(fmt.Sprintf("%d added · %d changed · %d only on the server · %d unchanged",
			len(d.Added), len(d.Changed), len(d.Removed), d.Unchanged)))
		for _, k := range d.Added {
			p.line("  %s %s", p.ok("+"), k)
		}
		for _, c := range d.Changed {
			p.line("  %s %s", p.warn("~"), c.Key)
			p.line("      %s %s", p.bad("server:"), c.Server)
			p.line("      %s %s", p.ok("local: "), c.Local)
		}
		for _, k := range d.Removed {
			p.line("  %s %s", p.bad("-"), k)
		}
	}
	if out.Identical {
		p.line("%s local catalogs match the server", p.pass())
	}
}
