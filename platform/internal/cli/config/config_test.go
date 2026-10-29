package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
)

func write(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const valid = `version: 1
server: https://glossa.example.com
project: shop
source_locale: en_us
catalogs:
  path: locales/{locale}.json
`

func TestLoadCanonicalizesAndResolvesPaths(t *testing.T) {
	dir := t.TempDir()
	c, err := config.Load(write(t, dir, valid), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.SourceLocale != "en-US" || c.SyntaxOrDefault() != "mf1" {
		t.Errorf("config = %+v", c)
	}
	if got, want := c.CatalogPath("de"), filepath.Join(dir, "locales", "de.json"); got != want {
		t.Errorf("CatalogPath = %s, want %s", got, want)
	}
	if got := c.PullPath("de"); got != c.CatalogPath("de") {
		t.Errorf("PullPath = %s", got)
	}
}

func TestLoadAppliesEnvironmentOverrides(t *testing.T) {
	env := map[string]string{"GLOSSA_SERVER": "http://ci:8080", "GLOSSA_PROJECT": "other", "GLOSSA_TENANT": "t1"}
	c, err := config.Load(write(t, t.TempDir(), valid), func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.Server != "http://ci:8080" || c.Project != "other" || c.Tenant != "t1" {
		t.Errorf("config = %+v", c)
	}
}

func TestLoadRejectsInvalidFiles(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"unknown field", valid + "sever: x\n", "field sever not found"},
		{"version", strings.Replace(valid, "version: 1", "version: 2", 1), "version: must be 1"},
		{"no locale placeholder", strings.Replace(valid, "{locale}", "en", 1), "catalogs.path: must contain {locale}"},
		{"bad locale", strings.Replace(valid, "en_us", "not a locale", 1), "source_locale"},
		{"bad syntax", valid + "syntax: icu\n", "syntax: must be mf1 or mf2"},
		{"vue without ts", valid + "generate:\n  vue: src/glossa-vue.ts\n", "generate.vue"},
		{"react without ts", valid + "generate:\n  react: src/glossa-react.ts\n", "generate.react"},
		{"fail_on", valid + "check:\n  fail_on: info\n", "check.fail_on"},
		{"application", valid + "extract:\n  application: Web App\n", "extract.application"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Load(write(t, t.TempDir(), tc.body), nil)
			var inv *config.InvalidError
			if !errors.As(err, &inv) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want InvalidError containing %q", err, tc.want)
			}
		})
	}
}

func TestFindWalksUp(t *testing.T) {
	root := t.TempDir()
	p := write(t, root, valid)
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := config.Find(sub)
	if err != nil || got != p {
		t.Fatalf("Find = %q, %v; want %q", got, err, p)
	}
	if _, err := config.Find(t.TempDir()); !errors.Is(err, config.ErrNotFound) {
		t.Errorf("Find in empty dir = %v", err)
	}
}

func TestWriteRoundTripsAndRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, config.FileName)
	c := &config.Config{Version: 1, Server: "http://localhost:8080", Project: "shop", SourceLocale: "de",
		Catalogs: config.Catalogs{Path: "locales/{locale}.json"},
		Generate: config.Generate{TypeScript: "src/messages.ts"}}
	if err := c.Write(p, false); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(p, false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second write = %v, want ErrExist", err)
	}
	back, err := config.Load(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if back.Project != "shop" || back.Generate.TypeScript != "src/messages.ts" {
		t.Errorf("round trip = %+v", back)
	}
}
