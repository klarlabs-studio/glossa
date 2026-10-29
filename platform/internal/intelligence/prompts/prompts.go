// Package prompts holds the translation agent's prompts as versioned
// artifacts (RFC 0003 §3.2): <task>/<version>.tmpl, embedded into the
// binary. A prompt change is a new version file; the version a suggestion
// was made with is recorded in its provenance, and the evals pin the
// versions they were recorded against.
//
// Each template defines "system" (stable instructions, no per-message
// data, so providers can cache it) and "user" (the message and the
// knowledge the tools returned). The repair template defines only "user":
// it is the follow-up turn of the translate conversation. Templates use
// [[ ]] as delimiters because MF2 quoted patterns are {{ }}.
package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"text/template"
)

//go:embed */*.tmpl
var files embed.FS

// Prompt tasks.
const (
	Translate = "translate"
	Repair    = "repair"
	Assess    = "assess"
	// Linguistic is the linguistic QA review (RFC 0005 §3.8). It is a
	// prompt of its own and not a mode of assess: assess asks how likely
	// a reviewer approves a draft the agent just wrote, and this asks
	// what is wrong with a translation that already exists.
	Linguistic = "linguistic"
)

// Template is one versioned prompt.
type Template struct {
	Task    string
	Version string
	tmpl    *template.Template
}

// ID is "<task>/<version>", as recorded in provenance.
func (t *Template) ID() string { return t.Task + "/" + t.Version }

// Load parses the template for task and version.
func Load(task, version string) (*Template, error) {
	src, err := files.ReadFile(task + "/" + version + ".tmpl")
	if err != nil {
		return nil, fmt.Errorf("prompts: %s/%s: %w", task, version, err)
	}
	tmpl, err := template.New(task).Delims("[[", "]]").Funcs(funcs).Option("missingkey=error").Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("prompts: parse %s/%s: %w", task, version, err)
	}
	return &Template{Task: task, Version: version, tmpl: tmpl}, nil
}

// Files exposes the embedded templates, e.g. to list the versions.
func Files() fs.FS { return files }

// MustLoad is Load for the built-in versions; it panics on a broken
// embedded template, which the tests catch.
func MustLoad(task, version string) *Template {
	t, err := Load(task, version)
	if err != nil {
		panic(err)
	}
	return t
}

// Rendered is a rendered prompt.
type Rendered struct {
	System string
	User   string
}

// Render executes the template's parts with data. System is empty for a
// template without a "system" part.
func (t *Template) Render(data any) (Rendered, error) {
	var out Rendered
	for name, dst := range map[string]*string{"system": &out.System, "user": &out.User} {
		if t.tmpl.Lookup(name) == nil {
			continue
		}
		var buf bytes.Buffer
		if err := t.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
			return Rendered{}, fmt.Errorf("prompts: render %s %s: %w", t.ID(), name, err)
		}
		*dst = strings.TrimSpace(collapseBlankLines(buf.String()))
	}
	if out.User == "" {
		return Rendered{}, fmt.Errorf("prompts: %s rendered an empty user prompt", t.ID())
	}
	return out, nil
}

// collapseBlankLines keeps templates readable without leaking runs of
// blank lines (and cache-busting whitespace noise) into prompts.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

var funcs = template.FuncMap{
	"join": strings.Join,
	"inc":  func(i int) int { return i + 1 },
}
