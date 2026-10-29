package capture_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/capture"
	"go.klarlabs.de/glossa/platform/internal/cli/config"
)

func load(t *testing.T, captureYAML string, files map[string]string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, config.FileName)
	body := "version: 1\nserver: http://glossa.invalid\nproject: shop\nsource_locale: de\ncatalogs:\n  path: locales/{locale}.json\ncapture:\n" + captureYAML
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPlanExpandsRoutesViewportsAndLocalesInOrder(t *testing.T) {
	cfg := load(t, `
  base_url: http://localhost:4173/app/
  locales: [ja, de]
  locale: { query: lang }
  viewports:
    - { width: 390, height: 844, device_scale_factor: 2, mobile: true }
    - { width: 1280, height: 800 }
  routes:
    - { route: "/checkout/[step]", url: "/checkout/payment?x=1" }
    - route: /
`, nil)
	p, err := capture.NewPlan(cfg, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, j := range p.Jobs {
		got = append(got, j.Route+" "+j.URL+" "+j.Locale+" "+strings.Join([]string{strconv.Itoa(j.Viewport.Width), strconv.Itoa(j.Viewport.Height)}, "x"))
	}
	want := []string{
		"/ http://localhost:4173/app/?lang=de de 390x844",
		"/ http://localhost:4173/app/?lang=de de 1280x800",
		"/ http://localhost:4173/app/?lang=ja ja 390x844",
		"/ http://localhost:4173/app/?lang=ja ja 1280x800",
		"/checkout/[step] http://localhost:4173/app/checkout/payment?lang=de&x=1 de 390x844",
		"/checkout/[step] http://localhost:4173/app/checkout/payment?lang=de&x=1 de 1280x800",
		"/checkout/[step] http://localhost:4173/app/checkout/payment?lang=ja&x=1 ja 390x844",
		"/checkout/[step] http://localhost:4173/app/checkout/payment?lang=ja&x=1 ja 1280x800",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("jobs:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if v := p.Jobs[0].Viewport; v.DeviceScaleFactor != 2 || !v.Mobile {
		t.Errorf("viewport = %+v", v)
	}
	if p.Origin != "http://localhost:4173" {
		t.Errorf("origin = %s", p.Origin)
	}
}

func TestPlanLocaleInURLOrCookie(t *testing.T) {
	cfg := load(t, `
  base_url: http://localhost:4173
  locales: [de, en]
  routes:
    - { route: /, url: "/{locale}/" }
`, nil)
	p, err := capture.NewPlan(cfg, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if p.Jobs[0].URL != "http://localhost:4173/de/" || p.Jobs[2].URL != "http://localhost:4173/en/" {
		t.Errorf("urls = %s, %s", p.Jobs[0].URL, p.Jobs[2].URL)
	}

	cfg = load(t, `
  base_url: https://preview.example.com
  locales: [de, en]
  locale: { cookie: glossa_locale }
  routes:
    - route: /
`, nil)
	p, err = capture.NewPlan(cfg, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	j := p.Jobs[2]
	if j.URL != "https://preview.example.com/" || len(j.Cookies) != 1 ||
		j.Cookies[0] != (capture.Cookie{Name: "glossa_locale", Value: "en", URL: "https://preview.example.com"}) {
		t.Errorf("job = %+v", j)
	}
}

func TestPlanExpandsSecretsFromTheEnvironment(t *testing.T) {
	cfg := load(t, `
  base_url: http://localhost:4173
  routes: [{ route: / }]
  cookies:
    - { name: session, value: "${FIXTURE_SESSION}" }
  headers:
    X-Fixture-User: "user-${FIXTURE_USER}"
`, nil)
	p, err := capture.NewPlan(cfg, "", env(map[string]string{"FIXTURE_SESSION": "s3cret", "FIXTURE_USER": "lina"}))
	if err != nil {
		t.Fatal(err)
	}
	if c := p.Jobs[0].Cookies; len(c) != 1 || c[0].Value != "s3cret" || c[0].URL != "http://localhost:4173" {
		t.Errorf("cookies = %+v", c)
	}
	if p.Headers["X-Fixture-User"] != "user-lina" {
		t.Errorf("headers = %v", p.Headers)
	}

	_, err = capture.NewPlan(cfg, "", env(map[string]string{"FIXTURE_SESSION": "s3cret"}))
	var pe *capture.PlanError
	if !errors.As(err, &pe) || pe.Field != "capture.headers.X-Fixture-User" || !strings.Contains(pe.Problem, "FIXTURE_USER is not set") ||
		strings.Contains(err.Error(), "s3cret") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanLoadsPlaybooks(t *testing.T) {
	good := `{"name":"open","url":"http://elsewhere.invalid/","actions":[{"type":"click","selector":"#open"},{"type":"wait","selector":"dialog"}]}`
	cfg := load(t, `
  base_url: http://localhost:4173
  routes:
    - { route: /a, playbook: pb/open.json }
    - { route: /b }
`, map[string]string{"pb/open.json": good})
	p, err := capture.NewPlan(cfg, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	pb := p.Jobs[0].Playbook
	if pb == nil || len(pb.Actions) != 2 || pb.URL != "" || p.Jobs[2].Playbook != nil {
		t.Errorf("playbooks = %+v / %+v", pb, p.Jobs[2].Playbook)
	}

	for name, body := range map[string]string{
		"missing":        "",
		"not json":       "{",
		"no actions":     `{"name":"x","actions":[]}`,
		"unknown action": `{"name":"x","actions":[{"type":"teleport"}]}`,
	} {
		files := map[string]string{"pb/open.json": body}
		if name == "missing" {
			files = nil
		}
		cfg := load(t, "  base_url: http://localhost:4173\n  routes:\n    - { route: /a, playbook: pb/open.json }\n", files)
		_, err := capture.NewPlan(cfg, "", env(nil))
		var pe *capture.PlanError
		if !errors.As(err, &pe) || pe.Field != "capture.routes[0].playbook" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestPlanNeedsABaseURLAndRoutes(t *testing.T) {
	var pe *capture.PlanError
	cfg := load(t, "  routes: [{ route: / }]\n", nil)
	if _, err := capture.NewPlan(cfg, "", env(nil)); !errors.As(err, &pe) || pe.Field != "capture.base_url" {
		t.Errorf("no base url: %v", err)
	}
	if p, err := capture.NewPlan(cfg, "http://ci:3000", env(nil)); err != nil || p.Jobs[0].URL != "http://ci:3000/" {
		t.Errorf("--base-url: %v", err)
	}
	if _, err := capture.NewPlan(cfg, "ftp://x", env(nil)); !errors.As(err, &pe) || pe.Field != "--base-url" {
		t.Errorf("bad --base-url: %v", err)
	}
	cfg = load(t, "  base_url: http://localhost:4173\n", nil)
	if _, err := capture.NewPlan(cfg, "", env(nil)); !errors.As(err, &pe) || pe.Field != "capture.routes" {
		t.Errorf("no routes: %v", err)
	}
}

func TestPlanRefusesMoreCapturesThanABuildHolds(t *testing.T) {
	var routes strings.Builder
	for i := range 130 {
		routes.WriteString("    - route: /r" + strconv.Itoa(i) + "\n")
	}
	cfg := load(t, "  base_url: http://localhost:4173\n  locales: [de, en]\n  locale: { query: l }\n  routes:\n"+routes.String(), nil)
	var pe *capture.PlanError
	if _, err := capture.NewPlan(cfg, "", env(nil)); !errors.As(err, &pe) || !strings.Contains(pe.Problem, "520 captures") {
		t.Errorf("err = %v", err)
	}
}
