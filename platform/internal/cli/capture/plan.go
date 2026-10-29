// Package capture is `glossa capture` (RFC 0004 §3.2): it opens each
// page of the capture plan in headless Chrome with scout, finds where the
// page rendered its messages with the capture agent of @klarlabs-studio/glossa/capture,
// and takes a full-page screenshot, into one glossa.captures/v1 document.
//
// Capture runs where the app already runs, in the product's CI, against a
// preview with fixture data: a page whose runtime reports a production
// manifest is refused, and data-glossa-redact elements are blacked out
// before the screenshot (§10).
package capture

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"go.klarlabs.de/scout/agent"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
)

// MaxCaptures is the most captures one build holds (RFC 0004 §10).
const MaxCaptures = 500

// Plan is the capture plan with everything resolved: the pages to open
// and the fixture login.
type Plan struct {
	Jobs []Job
	// Origin is the base URL's origin: fixture headers go only there.
	Origin string
	// Headers are the fixture headers, secrets expanded. Never print them.
	Headers map[string]string
}

// Job is one capture: a (route, viewport, locale).
type Job struct {
	// Route is the pattern (/checkout/[step]); URL the page opened.
	Route string
	URL   string
	// Viewport's DeviceScaleFactor is 0 for the default, 1.
	Viewport config.Viewport
	Locale   string
	// Cookies are set before the page loads: the fixture login and the
	// locale cookie. Values are secrets.
	Cookies []Cookie
	// Playbook is replayed after the page loaded; nil for none.
	Playbook *agent.Playbook
}

// Cookie is a cookie for URL's origin.
type Cookie struct {
	Name, Value, URL string
}

// PlanError is a capture plan that can't run; Field names what to fix.
// It never quotes a secret.
type PlanError struct {
	Field   string
	Problem string
}

func (e *PlanError) Error() string { return e.Field + ": " + e.Problem }

// playbookActions are the action types scout replays.
var playbookActions = map[string]bool{"navigate": true, "click": true, "type": true, "select": true,
	"scroll": true, "wait": true, "fill_form": true, "extract": true}

// NewPlan resolves cfg's capture plan: baseURL (--base-url) overrides
// capture.base_url, getenv expands ${NAME} in cookies and headers, and
// playbooks are read. Jobs are ordered by route, URL, locale and the
// viewports' order in the plan.
func NewPlan(cfg *config.Config, baseURL string, getenv func(string) string) (*Plan, error) {
	cp := cfg.Capture
	base, field := cp.BaseURL, "capture.base_url"
	if baseURL != "" {
		base, field = baseURL, "--base-url"
	}
	if base == "" {
		return nil, &PlanError{Field: field, Problem: "is required: where the app runs (a preview with fixture data, e.g. http://localhost:4173)"}
	}
	b, err := url.Parse(base)
	if err != nil || (b.Scheme != "http" && b.Scheme != "https") || b.Host == "" || b.User != nil || b.RawQuery != "" || b.Fragment != "" {
		return nil, &PlanError{Field: field, Problem: "must be an http or https URL without credentials, query or fragment"}
	}
	if len(cp.Routes) == 0 {
		return nil, &PlanError{Field: "capture.routes", Problem: "is required: the pages to capture (route: /checkout/[step], url: /checkout/payment)"}
	}
	lookup := func(k string) (string, bool) {
		v := getenv(k)
		return v, v != ""
	}
	p := &Plan{Origin: b.Scheme + "://" + b.Host, Headers: map[string]string{}}
	var login []Cookie
	for i, c := range cp.Cookies {
		v, err := config.ExpandEnv(c.Value, lookup)
		if err != nil {
			return nil, &PlanError{Field: fmt.Sprintf("capture.cookies[%d].value", i), Problem: err.Error()}
		}
		login = append(login, Cookie{Name: c.Name, Value: v, URL: p.Origin})
	}
	names := make([]string, 0, len(cp.Headers))
	for name := range cp.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v, err := config.ExpandEnv(cp.Headers[name], lookup)
		if err != nil {
			return nil, &PlanError{Field: "capture.headers." + name, Problem: err.Error()}
		}
		p.Headers[name] = v
	}
	locales := cp.LocalesOr(cfg.SourceLocale)
	viewports := cp.ViewportsOrDefault()
	if n := len(cp.Routes) * len(locales) * len(viewports); n > MaxCaptures {
		return nil, &PlanError{Field: "capture", Problem: fmt.Sprintf("%d routes × %d locales × %d viewports is %d captures; a build holds at most %d",
			len(cp.Routes), len(locales), len(viewports), n, MaxCaptures)}
	}
	type sortable struct {
		job      Job
		viewport int
	}
	var all []sortable
	for i, r := range cp.Routes {
		var pb *agent.Playbook
		if r.Playbook != "" {
			if pb, err = loadPlaybook(cfg.Resolve(r.Playbook)); err != nil {
				return nil, &PlanError{Field: fmt.Sprintf("capture.routes[%d].playbook", i), Problem: err.Error()}
			}
		}
		for _, locale := range locales {
			u, err := pageURL(b, r.URLOrRoute(), locale, cp.Locale.Query)
			if err != nil {
				return nil, &PlanError{Field: fmt.Sprintf("capture.routes[%d].url", i), Problem: err.Error()}
			}
			cookies := append([]Cookie(nil), login...)
			if cp.Locale.Cookie != "" {
				cookies = append(cookies, Cookie{Name: cp.Locale.Cookie, Value: locale, URL: p.Origin})
			}
			for vi, v := range viewports {
				all = append(all, sortable{Job{Route: r.Route, URL: u, Viewport: v, Locale: locale, Cookies: cookies, Playbook: pb}, vi})
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		switch {
		case a.job.Route != b.job.Route:
			return a.job.Route < b.job.Route
		case a.job.URL != b.job.URL:
			return a.job.URL < b.job.URL
		case a.job.Locale != b.job.Locale:
			return a.job.Locale < b.job.Locale
		}
		return a.viewport < b.viewport
	})
	for _, s := range all {
		p.Jobs = append(p.Jobs, s.job)
	}
	return p, nil
}

// pageURL is ref (a path or an absolute URL, with {locale}) under base,
// with the locale query parameter when the plan selects the locale so.
func pageURL(base *url.URL, ref, locale, query string) (string, error) {
	ref = strings.ReplaceAll(ref, config.LocalePlaceholder, url.PathEscape(locale))
	if strings.HasPrefix(ref, "/") {
		ref = base.Scheme + "://" + base.Host + strings.TrimRight(base.Path, "/") + ref
	}
	u, err := url.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("isn't a URL once resolved against the base URL")
	}
	if query != "" {
		q := u.Query()
		q.Set(query, locale)
		u.RawQuery = q.Encode()
	}
	if len(u.String()) > 2048 {
		return "", fmt.Errorf("is longer than 2048 characters")
	}
	return u.String(), nil
}

// loadPlaybook reads a scout playbook. Its starting URL is ignored: a
// capture's playbook starts on the route's page.
func loadPlaybook(path string) (*agent.Playbook, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // glossa.yaml names the file
	if err != nil {
		return nil, fmt.Errorf("can't read %s: %v", path, err)
	}
	var pb agent.Playbook
	if err := json.Unmarshal(raw, &pb); err != nil {
		return nil, fmt.Errorf("%s isn't a scout playbook (JSON): %v", path, err)
	}
	if len(pb.Actions) == 0 {
		return nil, fmt.Errorf("%s has no actions", path)
	}
	for i, a := range pb.Actions {
		if !playbookActions[a.Type] {
			return nil, fmt.Errorf("%s: action %d has type %q; scout replays navigate, click, type, select, scroll, wait, fill_form and extract", path, i+1, a.Type)
		}
	}
	pb.URL = ""
	return &pb, nil
}
