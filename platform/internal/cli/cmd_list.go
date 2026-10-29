package cli

import (
	"context"
	"strconv"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// ── locales ─────────────────────────────────────────────────────────

type localeJSON struct {
	Code      string `json:"code"`
	Direction string `json:"direction"`
	IsSource  bool   `json:"is_source"`
}

type localesJSON struct {
	Schema   string              `json:"schema"`
	Locales  []localeJSON        `json:"locales"`
	Fallback map[string][]string `json:"fallback"`
}

func runLocales(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("locales")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	ls, err := p.client.Locales(ctx, p.scope)
	if err != nil {
		return inv.apiError(err, "can't list locales")
	}
	fb, err := p.client.FallbackGraph(ctx, p.scope)
	if err != nil {
		return inv.apiError(err, "can't read the fallback graph")
	}
	remote.SortLocales(ls)
	out := localesJSON{Schema: "glossa.cli.locales/v1", Locales: []localeJSON{}, Fallback: fb}
	if out.Fallback == nil {
		out.Fallback = map[string][]string{}
	}
	for _, l := range ls {
		out.Locales = append(out.Locales, localeJSON{Code: l.Code, Direction: string(l.Direction), IsSource: l.IsSource})
	}
	return inv.emit(out, func(pr *printer) {
		rows := [][]string{{"LOCALE", "DIRECTION", "", "FALLBACK"}}
		for _, l := range out.Locales {
			src := ""
			if l.IsSource {
				src = "source"
			}
			rows = append(rows, []string{l.Code, l.Direction, src, strings.Join(fb[l.Code], " → ")})
		}
		pr.table(rows)
		if def := fb["*"]; len(def) > 0 {
			pr.line("%s", pr.dim("default fallback (*): "+strings.Join(def, " → ")))
		}
	})
}

// ── messages ────────────────────────────────────────────────────────

type argumentJSON struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type messageJSON struct {
	Key            string         `json:"key"`
	Namespace      string         `json:"namespace"`
	State          string         `json:"state"`
	SourceRevision int            `json:"source_revision"`
	Text           string         `json:"text"`
	Syntax         string         `json:"syntax"`
	Arguments      []argumentJSON `json:"arguments"`
	Description    string         `json:"description,omitempty"`
}

type messagesJSON struct {
	Schema   string        `json:"schema"`
	Messages []messageJSON `json:"messages"`
}

func runMessages(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("messages [--prefix checkout.] [--missing-in de | --outdated-in de] [--state active|obsolete|all]")
	var f remote.MessageFilter
	fs.StringVar(&f.KeyPrefix, "prefix", "", "only keys starting with this")
	fs.StringVar(&f.Namespace, "namespace", "", "only this namespace")
	fs.StringVar(&f.MissingIn, "missing-in", "", "only messages without a translation in this locale")
	fs.StringVar(&f.OutdatedIn, "outdated-in", "", "only messages whose translation in this locale is outdated")
	state := fs.String("state", "active", "active, obsolete or all")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	switch *state {
	case "active", "obsolete":
		f.State = *state
	case "all":
	default:
		return usageError(inv.name, "--state must be active, obsolete or all, not %q", *state)
	}
	for _, l := range []*string{&f.MissingIn, &f.OutdatedIn} {
		if *l == "" {
			continue
		}
		tag, err := bcp47.Parse(*l)
		if err != nil {
			return usageError(inv.name, "%q is not a locale", *l)
		}
		*l = tag.String()
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	msgs, err := p.client.Messages(ctx, p.scope, f)
	if err != nil {
		return inv.apiError(err, "can't list messages")
	}
	out := messagesJSON{Schema: "glossa.cli.messages/v1", Messages: []messageJSON{}}
	for _, m := range msgs {
		mj := messageJSON{Key: m.Key, Namespace: m.Namespace, State: string(m.State), SourceRevision: m.SourceRevision,
			Text: m.Source.Text, Syntax: string(m.Source.Syntax), Arguments: []argumentJSON{}, Description: m.Description}
		for _, a := range m.Source.Arguments {
			mj.Arguments = append(mj.Arguments, argumentJSON{Name: a.Name, Type: string(a.Type)})
		}
		out.Messages = append(out.Messages, mj)
	}
	return inv.emit(out, func(pr *printer) {
		rows := [][]string{{"KEY", "REV", "ARGUMENTS", "TEXT"}}
		for _, m := range out.Messages {
			var as []string
			for _, a := range m.Arguments {
				as = append(as, a.Name+":"+a.Type)
			}
			key := m.Key
			if m.State == "obsolete" {
				key += " (obsolete)"
			}
			rows = append(rows, []string{key, strconv.Itoa(m.SourceRevision), strings.Join(as, " "), oneLine(m.Text, 60)})
		}
		pr.table(rows)
		pr.line("%s", pr.dim(plural(len(out.Messages), "message", "messages")))
	})
}

// ── namespaces ──────────────────────────────────────────────────────

type namespaceJSON struct {
	Name     string `json:"name"`
	Active   int    `json:"active_messages"`
	Obsolete int    `json:"obsolete_messages"`
}

// namespacesJSON is glossa.cli.namespaces/v1.
type namespacesJSON struct {
	Schema     string          `json:"schema"`
	Namespaces []namespaceJSON `json:"namespaces"`
}

func runNamespaces(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("namespaces")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if err := noMore(inv, pos); err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	ns, err := p.client.Namespaces(ctx, p.scope)
	if err != nil {
		return inv.apiError(err, "can't list namespaces")
	}
	out := namespacesJSON{Schema: "glossa.cli.namespaces/v1", Namespaces: []namespaceJSON{}}
	for _, n := range ns {
		out.Namespaces = append(out.Namespaces, namespaceJSON{Name: n.Name, Active: n.ActiveMessages, Obsolete: n.ObsoleteMessages})
	}
	return inv.emit(out, func(pr *printer) {
		rows := [][]string{{"NAMESPACE", "MESSAGES", "OBSOLETE"}}
		for _, n := range out.Namespaces {
			rows = append(rows, []string{n.Name, strconv.Itoa(n.Active), strconv.Itoa(n.Obsolete)})
		}
		pr.table(rows)
		pr.line("%s", pr.dim(plural(len(out.Namespaces), "namespace", "namespaces")))
	})
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
