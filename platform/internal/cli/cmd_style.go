package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// ── output shapes (cmd/glossa/README.md, "JSON output") ─────────────

// styleScopeJSON is where a guide applies; null members are broader.
type styleScopeJSON struct {
	ProjectID *string `json:"project_id"`
	Locale    *string `json:"locale"`
	Namespace *string `json:"namespace"`
}

type styleShowJSON struct {
	Schema  string                    `json:"schema"`
	Scope   styleScopeJSON            `json:"scope"`
	Fields  remote.StyleFields        `json:"fields"`
	Rules   []remote.StyleRule        `json:"rules"`
	Sources []remote.StyleGuideSource `json:"sources"`
}

type styleGuideJSON struct {
	ID      string             `json:"id"`
	Name    string             `json:"name"`
	Scope   styleScopeJSON     `json:"scope"`
	Version int                `json:"version"`
	Fields  remote.StyleFields `json:"fields"`
	Rules   []remote.StyleRule `json:"rules"`
}

type styleEditJSON struct {
	Schema string `json:"schema"`
	// Action is created, updated or unchanged.
	Action string         `json:"action"`
	Guide  styleGuideJSON `json:"guide"`
}

const styleUsage = `style <action> [flags]

Actions:
  show [--locale L] [--namespace NS] [--tenant]
                  the effective style guide: every guide that applies, merged, the narrowest winning
  edit --file style.yaml [--locale L] [--namespace NS] [--tenant-wide]
                  create or replace the guide of exactly that scope from a YAML file:
                    name: …        (optional)
                    fields: {formality: {register: formal, pronoun: Sie}, tone: [friendly],
                             punctuation: {quotes: "„“"}, numbers: {…}, dates: {…}}
                    rules: [{id: no-exclamation, title: …, rationale: …, good: […], bad: […]}]`

type styleArgs struct {
	action, locale, namespace, file string
	tenant                          bool
}

func parseStyleArgs(inv *invocation, args []string) (styleArgs, error) {
	fs := inv.flags(styleUsage)
	var a styleArgs
	fs.StringVar(&a.locale, "locale", "", "the locale (a guide for de also applies to de-AT)")
	fs.StringVar(&a.namespace, "namespace", "", "the message namespace")
	fs.StringVar(&a.file, "file", "", "edit: the YAML file with the guide")
	fs.BoolVar(&a.tenant, "tenant", false, "show: tenant-level guides only, without the project's")
	fs.BoolVar(&a.tenant, "tenant-wide", false, "edit: the tenant's guide, for every project")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, usageError(inv.name, "missing action: show or edit")
	}
	a.action = pos[0]
	if err := noMore(inv, pos[1:]); err != nil {
		return a, err
	}
	if a.locale != "" {
		if a.locale, err = normalizeLocale(inv, "--locale", a.locale); err != nil {
			return a, err
		}
	}
	if a.tenant && a.namespace != "" {
		return a, usageError(inv.name, "a namespace belongs to a project: drop --tenant or --namespace")
	}
	switch a.action {
	case "show":
		return a, nil
	case "edit":
		if a.file == "" {
			return a, usageError(inv.name, "edit needs --file <style.yaml>")
		}
		return a, nil
	}
	return a, usageError(inv.name, "unknown action %q (show, edit)", a.action)
}

func runStyle(ctx context.Context, inv *invocation, args []string) error {
	a, err := parseStyleArgs(inv, args)
	if err != nil {
		return err
	}
	var file styleFile
	if a.action == "edit" {
		if file, err = inv.readStyleFile(a.file); err != nil {
			return err
		}
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	scope := remote.StyleScope{Locale: a.locale, Namespace: a.namespace}
	if !a.tenant {
		scope.Project = p.scope.Project
	}
	if a.action == "show" {
		return inv.styleShow(ctx, p, scope)
	}
	return inv.styleEdit(ctx, p, scope, file)
}

func scopeJSON(s remote.StyleScope) styleScopeJSON {
	return styleScopeJSON{ProjectID: optionalStr(s.Project), Locale: optionalStr(s.Locale), Namespace: optionalStr(s.Namespace)}
}

func (inv *invocation) styleShow(ctx context.Context, p *project, scope remote.StyleScope) error {
	eff, err := p.client.EffectiveStyle(ctx, p.scope.Tenant, scope)
	if err != nil {
		return inv.m2Error(err, "can't read the style guide")
	}
	out := styleShowJSON{Schema: "glossa.cli.style.show/v1", Scope: scopeJSON(scope), Fields: eff.Fields,
		Rules: nonNilList(eff.Rules), Sources: nonNilList(eff.Sources)}
	return inv.emit(out, func(pr *printer) {
		label := string(p.info.Slug)
		if scope.Project == "" {
			label = "the tenant"
		}
		for _, s := range []string{scope.Locale, scope.Namespace} {
			if s != "" {
				label += " · " + s
			}
		}
		pr.line("%s %s", pr.bold("Style for"), label)
		if len(out.Sources) == 0 {
			pr.line("  %s", pr.dim("no style guide applies; `glossa style edit --file style.yaml` adds one"))
			return
		}
		printStyle(pr, out.Fields, out.Rules)
		var srcs []string
		for _, s := range out.Sources {
			srcs = append(srcs, fmt.Sprintf("%s v%d", sourceLabel(s), s.Version))
		}
		pr.line("%s", pr.dim("from: "+strings.Join(srcs, " → ")))
	})
}

func sourceLabel(s remote.StyleGuideSource) string {
	switch {
	case s.Namespace != nil:
		return "namespace " + *s.Namespace
	case s.Locale != nil:
		return *s.Locale
	case s.ProjectId != nil:
		return "project"
	}
	return "tenant"
}

// printStyle prints the fields as dotted paths and the rules.
func printStyle(pr *printer, fields remote.StyleFields, rules []remote.StyleRule) {
	var flat [][]string
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(strings.TrimPrefix(prefix+"."+k, "."), x[k])
			}
		case []any:
			parts := make([]string, len(x))
			for i, e := range x {
				parts[i] = fmt.Sprint(e)
			}
			flat = append(flat, []string{"  " + prefix, strings.Join(parts, ", ")})
		default:
			flat = append(flat, []string{"  " + prefix, fmt.Sprint(x)})
		}
	}
	var m map[string]any
	b, _ := json.Marshal(fields)
	_ = json.Unmarshal(b, &m)
	walk("", m)
	if len(flat) > 0 {
		pr.table(append([][]string{{"  FIELD", "VALUE"}}, flat...))
	}
	for _, r := range rules {
		if r.Disabled != nil && *r.Disabled {
			continue
		}
		pr.line("  %s %s", pr.bold(r.Id), derefStr(r.Title))
		if r.Rationale != nil {
			pr.line("    %s", pr.dim(*r.Rationale))
		}
		for _, g := range derefList(r.Good) {
			pr.line("    %s %s", pr.ok("good:"), g)
		}
		for _, b := range derefList(r.Bad) {
			pr.line("    %s  %s", pr.bad("bad:"), b)
		}
	}
}

// styleFile is `style edit`'s YAML.
type styleFile struct {
	Name   *string             `json:"name,omitempty"`
	Fields *remote.StyleFields `json:"fields,omitempty"`
	Rules  *[]remote.StyleRule `json:"rules,omitempty"`
}

// readStyleFile reads the YAML strictly: through JSON into the API's
// shapes, so an unknown field is an error, not silently dropped.
func (inv *invocation) readStyleFile(path string) (styleFile, error) {
	var f styleFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(inv.env.Dir, path)
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, &Error{Exit: ExitUsage, Code: "file_not_found", What: "style file not found", Where: path, Fix: "check --file"}
	}
	if err != nil {
		return f, &Error{Exit: ExitUsage, Code: "invalid_style_file", What: "can't read the style file", Where: path, Why: err.Error()}
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return f, &Error{Exit: ExitUsage, Code: "invalid_style_file", What: "the style file isn't YAML", Where: path, Why: err.Error()}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return f, &Error{Exit: ExitUsage, Code: "invalid_style_file", What: "the style file isn't a YAML mapping", Where: path, Why: err.Error()}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, &Error{Exit: ExitUsage, Code: "invalid_style_file", What: "the style file doesn't match the style-guide fields",
			Where: path, Why: err.Error(), Fix: "use name, fields (formality, tone, punctuation, numbers, dates) and rules; `glossa style --help` shows the shape"}
	}
	if f.Fields == nil && f.Rules == nil && f.Name == nil {
		return f, &Error{Exit: ExitUsage, Code: "invalid_style_file", What: "the style file is empty", Where: path,
			Fix: "add fields and/or rules; `glossa style --help` shows the shape"}
	}
	return f, nil
}

func (inv *invocation) styleEdit(ctx context.Context, p *project, scope remote.StyleScope, f styleFile) error {
	existing, etag, found, err := p.client.StyleGuideFor(ctx, p.scope.Tenant, scope)
	if err != nil {
		return inv.m2Error(err, "can't read the style guides")
	}
	var (
		g      remote.StyleGuide
		action = "created"
	)
	if found {
		g, err = p.client.ReplaceStyleGuide(ctx, p.scope.Tenant, existing.Id, etag,
			remote.ReplaceStyleGuide{Name: f.Name, Fields: f.Fields, Rules: f.Rules})
		action = "updated"
		if err == nil && g.Version == existing.Version {
			action = "unchanged"
		}
	} else {
		body := remote.CreateStyleGuide{Name: f.Name, Fields: f.Fields, Rules: f.Rules,
			ProjectId: optionalStr(scope.Project), Locale: optionalStr(scope.Locale), Namespace: optionalStr(scope.Namespace)}
		g, err = p.client.CreateStyleGuide(ctx, p.scope.Tenant, body, newIdempotencyKey())
	}
	if err != nil {
		return inv.m2Error(err, "can't save the style guide")
	}
	out := styleEditJSON{Schema: "glossa.cli.style.edit/v1", Action: action, Guide: styleGuideJSON{ID: g.Id, Name: g.Name,
		Scope:   styleScopeJSON{ProjectID: g.ProjectId, Locale: g.Locale, Namespace: g.Namespace},
		Version: g.Version, Fields: g.Fields, Rules: nonNilList(g.Rules)}}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Style guide %s %s (version %d) %s", pr.pass(), orDefault(g.Name, g.Id), action, g.Version, pr.dim("("+g.Id+")"))
		pr.line("  %s", pr.dim("`glossa style show` shows the effective style"))
	})
}
