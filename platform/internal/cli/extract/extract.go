// Package extract finds message usages in source code and writes them as
// a glossa.usages/v1 document (RFC 0004 §2.1, §2.2). It is the collector
// for Go and for anything built without @klarlabs-studio/glossa/unplugin:
//
//   - Go files are parsed with go/parser: `.T(…)` method calls
//     (Client.T(ctx, "…"), Localizer.T("…"), client.For(…).T("…")) and
//     the typed accessors `glossa generate` writes (msg.For(l).CheckoutPay(…)),
//     each with the enclosing declaration as its component.
//   - Go templates are parsed with text/template/parse: {{t "…"}},
//     {{td "…" "…"}}, {{th "…"}}.
//   - Web files (HTML, Vue, Astro, TS, JS, JSX) keep a lexical scan, the
//     fallback when no bundler plugin runs.
//
// Only literal keys count: a message key has to be a string literal at
// the call site. The shared fixture suite in runtimes/testdata/usages is
// the contract, and @klarlabs-studio/glossa/unplugin passes the same one.
package extract

import (
	"bytes"
	"cmp"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Schema is the document type Document carries.
const Schema = "glossa.usages/v1"

// The kinds of usage (the contract's call shapes).
const (
	KindT         = "t"
	KindComponent = "component"
	KindElement   = "element"
	KindAccessor  = "accessor"
	KindTemplate  = "template"
)

// Usage is one place a message is used.
type Usage struct {
	Key  string `json:"key"`
	File string `json:"file"`
	// Line and Column are 1-based; Column counts Unicode code points and
	// points at the key's first character inside its quotes, or at an
	// accessor's first path segment.
	Line   int `json:"line"`
	Column int `json:"column"`
	// Component is the Go declaration (pkg.Func, pkg.(*Type).Method) or
	// the Vue/Astro file around the usage; empty when none applies.
	Component string `json:"component,omitempty"`
	// Route is the route pattern, where the file says it (Astro pages).
	Route string `json:"route,omitempty"`
	// Kind is t, component, element, accessor or template.
	Kind string `json:"kind"`
}

// Tool is what produced a document.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Header is a build's identity: the document fields around the usages.
type Header struct {
	Application string
	Commit      string
	Branch      string
	Tool        Tool
}

// Document is a glossa.usages/v1 document: one build's usages.
type Document struct {
	Schema      string  `json:"schema"`
	Application string  `json:"application"`
	Commit      string  `json:"commit"`
	Branch      string  `json:"branch"`
	Tool        Tool    `json:"tool"`
	Usages      []Usage `json:"usages"`
}

// NewDocument returns the document for h and usages (sorted as Scan
// returns them).
func NewDocument(h Header, usages []Usage) Document {
	if usages == nil {
		usages = []Usage{}
	}
	return Document{Schema: Schema, Application: h.Application, Commit: h.Commit, Branch: h.Branch,
		Tool: h.Tool, Usages: usages}
}

// Accessors map the typed accessor names `generate` emits back to keys.
type Accessors struct {
	// TS maps an accessor path (checkout.paymentFailed) to its key.
	TS map[string]string
	// Go maps a method name (CheckoutPaymentFailed) to its key.
	Go map[string]string
}

// DefaultTemplates are the Go template files when the config names none.
var DefaultTemplates = []string{"**/*.{tmpl,gotmpl,gohtml}"}

// Options configure a scan.
type Options struct {
	// Include and Exclude select the files; Templates are the files read
	// as Go templates (a file matching them needn't match Include).
	Include   []string
	Exclude   []string
	Templates []string
	Accessors Accessors
}

// Skipped is a file the scan couldn't read as its language.
type Skipped struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// Result is what Scan found.
type Result struct {
	// Usages are sorted by key, file, line and column (bytewise, which is
	// code-point order).
	Usages []Usage
	// Files is the number of files scanned.
	Files int
	// Skipped are files that failed to parse; they add no usages.
	Skipped []Skipped
}

// keyPattern is the contract's message key.
var keyPattern = regexp.MustCompile(`^[a-z0-9_-]+(?:\.[a-z0-9_-]+)*$`)

const maxKeyLen = 200

func validKey(s string) bool { return len(s) <= maxKeyLen && keyPattern.MatchString(s) }

// generated is the first line of a generated file.
var generated = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

func isGenerated(src []byte) bool {
	first, _, _ := bytes.Cut(src, []byte("\n"))
	return generated.Match(bytes.TrimSuffix(first, []byte("\r")))
}

// skipDirs are never scanned.
var skipDirs = map[string]bool{"node_modules": true, "dist": true, "vendor": true, "build": true, "coverage": true}

// Scan walks root and returns the usages in the selected files.
func Scan(root string, opts Options) (Result, error) {
	include, err := compileAll(opts.Include)
	if err != nil {
		return Result{}, err
	}
	exclude, err := compileAll(opts.Exclude)
	if err != nil {
		return Result{}, err
	}
	templates, err := compileAll(opts.Templates)
	if err != nil {
		return Result{}, err
	}
	var res Result
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		isTemplate := matchAny(templates, rel)
		if !(isTemplate || matchAny(include, rel)) || matchAny(exclude, rel) {
			return nil
		}
		src, err := os.ReadFile(p) //nolint:gosec // walking the project the user pointed at
		if err != nil {
			return err
		}
		res.Files++
		usages, err := scanFile(rel, src, isTemplate, opts.Accessors)
		if err != nil {
			res.Skipped = append(res.Skipped, Skipped{File: rel, Reason: err.Error()})
			return nil
		}
		res.Usages = append(res.Usages, usages...)
		return nil
	})
	Sort(res.Usages)
	return res, err
}

// Sort orders usages by key, file, line and column: the document's order.
func Sort(usages []Usage) {
	slices.SortFunc(usages, func(a, b Usage) int {
		return cmp.Or(strings.Compare(a.Key, b.Key), strings.Compare(a.File, b.File),
			cmp.Compare(a.Line, b.Line), cmp.Compare(a.Column, b.Column))
	})
}

func scanFile(file string, src []byte, isTemplate bool, acc Accessors) ([]Usage, error) {
	switch {
	case isTemplate:
		return scanTemplate(file, src)
	case isGenerated(src):
		return nil, nil
	case path.Ext(file) == ".go":
		return scanGo(file, src, acc.Go)
	default:
		return scanWeb(file, src, acc.TS), nil
	}
}

func compileAll(patterns []string) ([]*Glob, error) {
	var out []*Glob
	for _, p := range patterns {
		g, err := CompileGlob(p)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

func matchAny(gs []*Glob, p string) bool {
	for _, g := range gs {
		if g.Match(p) {
			return true
		}
	}
	return false
}
