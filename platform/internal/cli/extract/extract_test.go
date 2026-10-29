package extract_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/extract"
)

func TestGlob(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"**/*.{ts,vue}", "src/a/b.vue", true},
		{"**/*.{ts,vue}", "b.ts", true},
		{"**/*.{ts,vue}", "src/b.tsx", false},
		{"src/*.go", "src/a.go", true},
		{"src/*.go", "src/x/a.go", false},
		{"**/*_test.go", "a/b_test.go", true},
		{"**/*.test.*", "src/x.test.ts", true},
		{"src/**", "src/a/b/c.ts", true},
		{"file?.go", "file1.go", true},
		{"[ab].ts", "a.ts", true},
		{"[!ab].ts", "a.ts", false},
	} {
		g, err := extract.CompileGlob(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		if got := g.Match(tc.path); got != tc.want {
			t.Errorf("%s ~ %s = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lines(us []extract.Usage) string {
	var out []string
	for _, u := range us {
		s := fmt.Sprintf("%s %s:%d:%d %s", u.Key, u.File, u.Line, u.Column, u.Kind)
		if u.Component != "" {
			s += " " + u.Component
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

// TestScanSelectsFiles covers what the fixture suite doesn't: include,
// exclude and template globs, skipped directories, and files that don't
// parse.
func TestScanSelectsFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/App.vue", `<template><p>{{ $t("cart.items") }}</p></template>`)
	write(t, root, "templates/mail.html", `<h1>{{t "email.title"}}</h1>`)
	write(t, root, "templates/broken.gotmpl", `{{t "email.broken"`)
	write(t, root, "internal/broken.go", `package broken
func f() { l.T("go.broken") `)
	write(t, root, "node_modules/x/index.ts", `t("vendored.key")`)
	write(t, root, ".cache/x.ts", `t("hidden.key")`)
	write(t, root, "src/cart.test.ts", `t("test.key")`)
	write(t, root, "src/notes.md", `t("not.included")`)

	res, err := extract.Scan(root, extract.Options{
		Include:   []string{"**/*.{ts,vue,go}"},
		Exclude:   []string{"**/*.test.*"},
		Templates: []string{"templates/**"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "cart.items src/App.vue:1:21 t App\nemail.title templates/mail.html:1:10 template"
	if got := lines(res.Usages); got != want {
		t.Errorf("usages:\n%s\nwant:\n%s", got, want)
	}
	if res.Files != 4 {
		t.Errorf("files = %d, want 4", res.Files)
	}
	if len(res.Skipped) != 2 || res.Skipped[0].File != "internal/broken.go" || res.Skipped[1].File != "templates/broken.gotmpl" {
		t.Errorf("skipped = %+v", res.Skipped)
	}
}

// TestGoShapesBeyondTheFixtures pins Go details the shared suite leaves
// open: generic receivers, the runtime imported without a name, and
// shadowed imports.
func TestGoShapesBeyondTheFixtures(t *testing.T) {
	root := t.TempDir()
	write(t, root, "set/set.go", `package set

import (
	"strings"

	"go.klarlabs.de/glossa/runtimes/go"
)

type Set[T any] struct{ l *glossa.Localizer }

func (s *Set[T]) Label() string { return s.l.T("set.label", nil) }

func Default() string { return glossa.Default("x").Text + strings.Title("y") }

func Shadow(strings *Msgs) string { return strings.Title() }
`)
	res, err := extract.Scan(root, extract.Options{Include: []string{"**"},
		Accessors: extract.Accessors{Go: map[string]string{"Default": "default", "Title": "title"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "set.label set/set.go:11:49 t set.(*Set).Label\ntitle set/set.go:15:52 accessor set.Shadow"
	if got := lines(res.Usages); got != want {
		t.Errorf("usages:\n%s\nwant:\n%s", got, want)
	}
}

// TestTemplatePipes: a literal piped into a bare t or th is its key; td's
// piped value is its default text, never a key.
func TestTemplatePipes(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.tmpl", `{{"a.one" | th}}{{"fake.default" | td}}{{"fake.x" | printf "%s" | t}}`)
	res, err := extract.Scan(root, extract.Options{Templates: extract.DefaultTemplates})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := lines(res.Usages), "a.one a.tmpl:1:4 template"; got != want {
		t.Errorf("usages:\n%s\nwant:\n%s", got, want)
	}
}

func TestDocumentIsSortedAndNeverNull(t *testing.T) {
	doc := extract.NewDocument(extract.Header{Application: "web"}, nil)
	if doc.Schema != "glossa.usages/v1" || doc.Usages == nil {
		t.Errorf("doc = %+v", doc)
	}
	us := []extract.Usage{
		{Key: "b", File: "a", Line: 1, Column: 1},
		{Key: "a", File: "src/negatives.html", Line: 1, Column: 1},
		{Key: "a", File: "src/Negatives.vue", Line: 2, Column: 1},
		{Key: "a", File: "src/Negatives.vue", Line: 1, Column: 9},
		{Key: "a", File: "src/Negatives.vue", Line: 1, Column: 3},
	}
	extract.Sort(us)
	var got []string
	for _, u := range us {
		got = append(got, fmt.Sprintf("%s %s:%d:%d", u.Key, u.File, u.Line, u.Column))
	}
	want := "a src/Negatives.vue:1:3,a src/Negatives.vue:1:9,a src/Negatives.vue:2:1,a src/negatives.html:1:1,b a:1:1"
	if strings.Join(got, ",") != want {
		t.Errorf("order = %v", got)
	}
}
