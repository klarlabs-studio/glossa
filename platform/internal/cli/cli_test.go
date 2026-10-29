package cli

import (
	"strings"
	"testing"
)

func TestHelpVersionAndUnknownCommands(t *testing.T) {
	w := newWorkspace(t)
	r := w.run("help")
	r.want(t, ExitOK)
	for _, c := range commands() {
		if !strings.Contains(r.stdout, c.name) {
			t.Errorf("help lacks %s", c.name)
		}
	}
	if r := w.run("--version"); r.stdout != "glossa test\n" {
		t.Errorf("version = %q", r.stdout)
	}
	r = w.run("push", "--help")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "--dry-run") || strings.Count(r.stdout, "Usage: glossa push") != 1 {
		t.Errorf("push --help = %s", r.stdout)
	}
	r = w.run("frobnicate")
	r.want(t, ExitUsage)
	if !strings.Contains(r.stderr, `unknown command "frobnicate"`) || !strings.Contains(r.stderr, "fix:") {
		t.Errorf("stderr = %s", r.stderr)
	}
	w.run("push", "--bogus").want(t, ExitUsage)
}

func TestErrorsSayWhatWhereWhyAndFix(t *testing.T) {
	w := newWorkspace(t)
	r := w.run("push")
	r.want(t, ExitUsage)
	for _, want := range []string{"error: no glossa.yaml found", "where:", "why:", "fix:", "glossa init"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.stderr)
		}
	}
	var doc errorDoc
	w.json(&doc, "push").want(t, ExitUsage)
	if doc.Schema != "glossa.cli.error/v1" || doc.Error.Code != "config_not_found" || doc.Error.ExitCode != 2 || doc.Error.Fix == "" {
		t.Errorf("error doc = %+v", doc)
	}
}

func TestNetworkAndAuthFailuresExit3(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, map[string]string{"en": `{"a": "A"}`})
	w.env["GLOSSA_TOKEN"] = "glossa_api_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	var doc errorDoc
	w.json(&doc, "push").want(t, ExitNetwork)
	if doc.Error.Code != "unauthenticated" || !strings.Contains(doc.Error.Fix, "glossa login") {
		t.Errorf("401 = %+v", doc.Error)
	}
	delete(w.env, "GLOSSA_TOKEN")
	w.json(&doc, "push").want(t, ExitNetwork)
	if doc.Error.Code != "no_token" {
		t.Errorf("no token = %+v", doc.Error)
	}
	w.env["GLOSSA_TOKEN"] = testToken
	w.env["GLOSSA_SERVER"] = "http://127.0.0.1:1"
	w.json(&doc, "status").want(t, ExitNetwork)
	if doc.Error.Code != "network" {
		t.Errorf("unreachable = %+v", doc.Error)
	}
}

func TestInvalidConfigIsAUsageError(t *testing.T) {
	w := newWorkspace(t)
	w.write("glossa.yaml", "version: 1\nserver: x\nproject: p\nsource_locale: en\ncatalogs:\n  path: locales/en.json\n")
	var doc errorDoc
	w.json(&doc, "check", "--offline").want(t, ExitUsage)
	if doc.Error.Code != "invalid_config" || !strings.Contains(doc.Error.Where, "catalogs.path") {
		t.Errorf("error = %+v", doc.Error)
	}
}

func TestColorOnlyOnATerminal(t *testing.T) {
	inv := &invocation{env: Env{ColorOutput: true, Getenv: func(string) string { return "" }}}
	inv.setupOutput()
	if !inv.out.color {
		t.Error("no color on a terminal")
	}
	inv = &invocation{env: Env{ColorOutput: true, Getenv: func(k string) string {
		if k == "NO_COLOR" {
			return "1"
		}
		return ""
	}}}
	inv.setupOutput()
	if inv.out.color {
		t.Error("color despite NO_COLOR")
	}
	inv = &invocation{env: Env{ColorOutput: true}, json: true}
	inv.setupOutput()
	if inv.out.color || !inv.out.quiet {
		t.Error("--json must be plain and suppress human output")
	}
}
