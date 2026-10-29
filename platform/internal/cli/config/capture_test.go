package config_test

import (
	"errors"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
)

const withCapture = valid + `capture:
  base_url: http://localhost:4173
  application: web
  locales: [de, ja_jp]
  locale:
    query: lang
  viewports:
    - { width: 1280, height: 800 }
    - { width: 390, height: 844, device_scale_factor: 2, mobile: true }
  routes:
    - route: /
    - route: /checkout/[step]
      url: /checkout/payment
      playbook: playbooks/checkout.json
  cookies:
    - { name: session, value: "${FIXTURE_SESSION}" }
  headers:
    X-Fixture-User: "user-${FIXTURE_USER}"
`

func TestCaptureLoadsWithDefaults(t *testing.T) {
	dir := t.TempDir()
	c, err := config.Load(write(t, dir, withCapture), nil)
	if err != nil {
		t.Fatal(err)
	}
	cp := c.Capture
	if got := cp.LocalesOr(c.SourceLocale); strings.Join(got, ",") != "de,ja-JP" {
		t.Errorf("locales = %v", got)
	}
	if cp.Routes[0].URLOrRoute() != "/" || cp.Routes[1].URLOrRoute() != "/checkout/payment" {
		t.Errorf("routes = %+v", cp.Routes)
	}
	if got := c.CaptureOutput(); !strings.HasSuffix(got, ".glossa/captures") || !strings.HasPrefix(got, dir) {
		t.Errorf("output = %s", got)
	}

	// Nothing but routes: the source locale, the two default viewports.
	c, err = config.Load(write(t, t.TempDir(), valid+"capture:\n  routes:\n    - route: /\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Capture.LocalesOr(c.SourceLocale); len(got) != 1 || got[0] != "en-US" {
		t.Errorf("default locales = %v", got)
	}
	vs := c.Capture.ViewportsOrDefault()
	if len(vs) != 2 || vs[0] != (config.Viewport{Width: 1280, Height: 800}) || vs[1] != (config.Viewport{Width: 390, Height: 844}) {
		t.Errorf("default viewports = %+v", vs)
	}
}

func TestCaptureExpandEnv(t *testing.T) {
	env := map[string]string{"A": "1", "B_2": "two"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	got, err := config.ExpandEnv("x-${A}-${B_2}-$A", lookup)
	if err != nil || got != "x-1-two-$A" {
		t.Errorf("ExpandEnv = %q, %v", got, err)
	}
	var missing *config.MissingEnvError
	if _, err := config.ExpandEnv("${NOPE}", lookup); !errors.As(err, &missing) || missing.Name != "NOPE" {
		t.Errorf("missing = %v", err)
	}
	for _, bad := range []string{"${", "${A", "${1A}", "${a-b}", "${}"} {
		if _, err := config.ExpandEnv(bad, lookup); err == nil || errors.As(err, &missing) {
			t.Errorf("ExpandEnv(%q) = %v, want a syntax error", bad, err)
		}
	}
}

func TestCaptureRejectsInvalidPlans(t *testing.T) {
	base := valid + "capture:\n"
	for _, tc := range []struct{ name, body, want string }{
		{"base url scheme", base + "  base_url: ftp://x\n", "capture.base_url: must be an http or https URL"},
		{"base url credentials", base + "  base_url: http://u:p@x\n", "capture.base_url: must not hold credentials"},
		{"route slash", base + "  routes:\n    - route: checkout\n", "capture.routes[0].route: must start with /"},
		{"dynamic route without url", base + "  routes:\n    - route: /blog/[slug]\n", "capture.routes[0].url: /blog/[slug] has a dynamic segment"},
		{"url shape", base + "  routes:\n    - { route: /a, url: 'mailto:x' }\n", "capture.routes[0].url: must be a path"},
		{"duplicate route", base + "  routes:\n    - route: /\n    - route: /\n", "capture.routes[1]: / (/) is listed twice"},
		{"viewport size", base + "  viewports:\n    - { width: 0, height: 800 }\n", "capture.viewports[0]: width and height must be 1–16384"},
		{"viewport scale", base + "  viewports:\n    - { width: 10, height: 10, device_scale_factor: 5 }\n", "capture.viewports[0].device_scale_factor: must be in (0, 4]"},
		{"locale tag", base + "  locales: [nope nope]\n", "capture.locales[0]"},
		{"locale twice", base + "  locales: [de, de]\n", "capture.locales[1]: de is listed twice"},
		{"two selectors", base + "  locale: { query: lang, cookie: lang }\n", "capture.locale: set query or cookie, not both"},
		{"selector name", base + "  locale: { query: 'a b' }\n", "capture.locale.query"},
		{"no selector", base + "  locales: [de]\n  routes:\n    - route: /\n", "capture.locale: how does the page pick the locale?"},
		{"cookie name", base + "  cookies:\n    - { name: 'a b', value: x }\n", "capture.cookies[0].name"},
		{"cookie value", base + "  cookies:\n    - { name: a, value: '' }\n", "capture.cookies[0].value: is required"},
		{"env syntax", base + "  cookies:\n    - { name: a, value: '${oops' }\n", "capture.cookies[0].value: ${"},
		{"header name", base + "  headers: { 'Bad Header': x }\n", "capture.headers: \"Bad Header\" is not a header name"},
		{"reserved header", base + "  headers: { Cookie: x }\n", "capture.headers: Cookie can't be set"},
		{"application", base + "  application: Web\n", "capture.application"},
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

func TestCaptureNeedsNoSelectorWhenURLsCarryTheLocale(t *testing.T) {
	body := valid + "capture:\n  locales: [de, en]\n  routes:\n    - { route: /, url: '/{locale}/' }\n"
	if _, err := config.Load(write(t, t.TempDir(), body), nil); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureErrorsNeverEchoSecrets(t *testing.T) {
	body := valid + "capture:\n  cookies:\n    - { name: 'bad name', value: 'hunter2' }\n  headers: { 'X-Token': 'secret-${oops' }\n"
	_, err := config.Load(write(t, t.TempDir(), body), nil)
	if err == nil || strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
}
