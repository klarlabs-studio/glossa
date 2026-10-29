package cli

import (
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
)

func TestInitReadsTheProjectFromTheServer(t *testing.T) {
	srv := newFakeServer(t)
	srv.sourceLocale = "de"
	w := newWorkspace(t)
	w.env["GLOSSA_TOKEN"] = testToken
	var out initJSON
	w.json(&out, "init", "--server", srv.URL(), "--project", "shop", "--typescript", "src/messages.ts").want(t, ExitOK)
	if !out.Checked || out.Config.SourceLocale != "de" || out.Config.Tenant != "ten_1" {
		t.Fatalf("init = %+v", out)
	}
	cfg, err := config.Load(out.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server != srv.URL() || cfg.Project != "shop" || cfg.Catalogs.Path != "locales/{locale}.json" || cfg.Generate.TypeScript != "src/messages.ts" {
		t.Errorf("glossa.yaml = %+v", cfg)
	}
	var doc errorDoc
	w.json(&doc, "init", "--server", srv.URL(), "--project", "shop").want(t, ExitUsage)
	if doc.Error.Code != "config_exists" {
		t.Errorf("second init = %+v", doc.Error)
	}
	w.json(&doc, "init", "--server", srv.URL(), "--project", "shop", "--source-locale", "en", "--force").want(t, ExitUsage)
	if doc.Error.Code != "source_locale_mismatch" {
		t.Errorf("mismatch = %+v", doc.Error)
	}
	w.json(&doc, "init", "--server", srv.URL(), "--project", "nope", "--force").want(t, ExitUsage)
	if doc.Error.Code != "project_not_found" {
		t.Errorf("unknown project = %+v", doc.Error)
	}
}

func TestInitOfflineNeedsTheSourceLocale(t *testing.T) {
	w := newWorkspace(t)
	var doc errorDoc
	w.json(&doc, "init", "--server", "http://localhost:1", "--project", "shop").want(t, ExitUsage)
	if !strings.Contains(doc.Error.Fix, "--source-locale") {
		t.Errorf("fix = %q", doc.Error.Fix)
	}
	var out initJSON
	w.json(&out, "init", "--server", "http://localhost:1", "--project", "shop", "--source-locale", "de_ch").want(t, ExitOK)
	if out.Checked || out.Config.SourceLocale != "de-CH" {
		t.Errorf("offline init = %+v", out)
	}
}

func TestInitPromptsOnATerminal(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t)
	w.env["GLOSSA_TOKEN"] = testToken
	w.stdin = srv.URL() + "\nshop\n\n"
	r := runInteractive(w, "init")
	r.want(t, ExitOK)
	if !strings.Contains(r.stderr, "Project (slug)") || !strings.Contains(w.read("glossa.yaml"), "project: shop") {
		t.Errorf("stderr = %s\nfile = %s", r.stderr, w.read("glossa.yaml"))
	}
}

func TestLoginStoresAVerifiedTokenAndWhoamiShowsIt(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t)
	w.stdin = testToken + "\n"
	var login loginJSON
	w.json(&login, "login", "--server", srv.URL(), "--token-stdin").want(t, ExitOK)
	if login.Tenant.Slug != "acme" || login.StoredIn != "memory" || w.store.tokens[srv.URL()] != testToken {
		t.Fatalf("login = %+v, store = %v", login, w.store.tokens)
	}
	var who whoamiJSON
	w.json(&who, "whoami", "--server", srv.URL()).want(t, ExitOK)
	if who.TokenSource != "memory" || strings.Contains(who.Token, testToken[15:]) || who.Tenant.ID != "ten_1" {
		t.Errorf("whoami = %+v", who)
	}
	w.withProject(srv, nil)
	w.json(&who, "whoami").want(t, ExitOK)
	if who.TokenSource != "GLOSSA_TOKEN" || who.Project == nil || who.Project.Slug != "shop" {
		t.Errorf("whoami in a project = %+v", who)
	}
	var out map[string]any
	w.json(&out, "logout", "--server", srv.URL()).want(t, ExitOK)
	if out["removed"] != true || len(w.store.tokens) != 0 {
		t.Errorf("logout = %v", out)
	}
}

func TestLoginRejectsBadTokens(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t)
	w.stdin = "not-a-token\n"
	var doc errorDoc
	w.json(&doc, "login", "--server", srv.URL(), "--token-stdin").want(t, ExitUsage)
	if doc.Error.Code != "invalid_token" {
		t.Errorf("malformed = %+v", doc.Error)
	}
	w.stdin = "glossa_api_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB\n"
	w.json(&doc, "login", "--server", srv.URL(), "--token-stdin").want(t, ExitNetwork)
	if doc.Error.Code != "unauthenticated" || len(w.store.tokens) != 0 {
		t.Errorf("refused = %+v, stored %v", doc.Error, w.store.tokens)
	}
}
