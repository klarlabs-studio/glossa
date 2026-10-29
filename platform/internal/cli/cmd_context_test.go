package cli

import (
	"strings"
	"testing"
)

// pluginUsages is .glossa/usages.json as @klarlabs-studio/glossa/unplugin writes it.
const pluginUsages = `{
  "schema": "glossa.usages/v1",
  "application": "web",
  "commit": "` + testCommit + `",
  "branch": "feat/copy",
  "tool": { "name": "@klarlabs-studio/glossa", "version": "0.1.0" },
  "usages": [
    { "key": "cart.items", "file": "src/Cart.vue", "line": 3, "column": 9, "component": "Cart", "route": "/cart", "kind": "t" },
    { "key": "cart.gone", "file": "src/Cart.vue", "line": 7, "column": 9, "component": "Cart", "route": "/cart", "kind": "t" }
  ]
}
`

type contextPushDoc struct {
	Schema   string `json:"schema"`
	File     string `json:"file"`
	Source   string `json:"source"`
	Replayed bool   `json:"replayed"`
	Build    struct {
		ID              string `json:"id"`
		Usages          int    `json:"usages"`
		UnknownKeys     int    `json:"unknown_keys"`
		OnDefaultBranch bool   `json:"on_default_branch"`
		Digest          string `json:"digest"`
	} `json:"build"`
}

func TestContextPushUploadsThePluginsDocument(t *testing.T) {
	srv := newFakeServer(t)
	srv.messages["cart.items"] = &fakeMessage{key: "cart.items", state: "active"}
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN})
	w.write(".glossa/usages.json", pluginUsages)

	r := w.run("context", "push", ".glossa/usages.json")
	r.want(t, ExitOK)
	for _, s := range []string{"Uploaded the usages of web at 0123456789ab (feat/copy) as build bld_1",
		"1 usage names a key the catalog doesn't know"} {
		if !strings.Contains(r.stdout, s) {
			t.Errorf("output lacks %q:\n%s", s, r.stdout)
		}
	}
	ups := srv.ctx.uploads
	if len(ups) != 1 || ups[0].source != "plugin" || len(ups[0].doc.Usages) != 2 || ups[0].doc.Tool.Name != "@klarlabs-studio/glossa" {
		t.Fatalf("uploaded = %+v", ups)
	}

	// Again: the server answers with the first build.
	var out contextPushDoc
	w.json(&out, "context", "push", ".glossa/usages.json").want(t, ExitOK)
	if out.Schema != "glossa.cli.context.push/v1" || out.File != ".glossa/usages.json" || out.Source != "plugin" || !out.Replayed ||
		out.Build.ID != "bld_1" || out.Build.Usages != 2 || out.Build.UnknownKeys != 1 || len(out.Build.Digest) != 64 {
		t.Errorf("--json = %+v", out)
	}
}

func TestContextPushTakesTheSourceFromTheToolOrTheFlag(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN})
	w.write("extract.json", strings.Replace(pluginUsages, `"name": "@klarlabs-studio/glossa", "version": "0.1.0"`, `"name": "glossa", "version": "0.4.0"`, 1))
	w.write("runtime.json", strings.Replace(pluginUsages, "feat/copy", "main", 1))
	w.run("context", "push", "extract.json").want(t, ExitOK)
	w.run("context", "push", "runtime.json", "--source", "runtime").want(t, ExitOK)
	ups := srv.ctx.uploads
	if len(ups) != 2 || ups[0].source != "extract" || ups[1].source != "runtime" {
		t.Errorf("sources = %+v", ups)
	}
}

func TestContextPushRefusals(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN})
	w.write("messages.json", `{"cart.items": "Items"}`)
	w.write("short.json", strings.Replace(pluginUsages, testCommit, "0123456", 1))
	w.write("ios.json", strings.Replace(pluginUsages, `"web"`, `"ios"`, 1))
	var e errorDoc
	for _, tc := range []struct {
		args []string
		exit ExitCode
		code string
	}{
		{[]string{"context"}, ExitUsage, "invalid_usage"},
		{[]string{"context", "pull", "x.json"}, ExitUsage, "invalid_usage"},
		{[]string{"context", "push"}, ExitUsage, "invalid_usage"},
		{[]string{"context", "push", "missing.json"}, ExitUsage, "file_unreadable"},
		{[]string{"context", "push", "messages.json"}, ExitUsage, "invalid_usages"},
		{[]string{"context", "push", "short.json"}, ExitUsage, "invalid_usages"},
		{[]string{"context", "push", "ios.json"}, ExitUsage, "unknown_application"},
		{[]string{"context", "push", "ios.json", "--source", "bundler"}, ExitUsage, "invalid_source"},
	} {
		w.json(&e, tc.args...).want(t, tc.exit)
		if e.Error.Code != tc.code {
			t.Errorf("%v: error = %+v, want %s", tc.args, e.Error, tc.code)
		}
	}
	if len(srv.ctx.builds) != 0 {
		t.Errorf("builds = %v", srv.ctx.builds)
	}
}
