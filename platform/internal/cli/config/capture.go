package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Capture is the capture plan of `glossa capture` (RFC 0004 §3.2): which
// pages to screenshot, at which viewports and in which locales. Each
// (route, viewport, locale) becomes one capture.
type Capture struct {
	// BaseURL is where the app runs (a preview with fixture data, never
	// production); route URLs are relative to it. --base-url overrides it.
	BaseURL string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	// Application is the slug of the captured application (default:
	// extract.application).
	Application string `yaml:"application,omitempty" json:"application,omitempty"`
	// Output is where captures go without --upload (default
	// .glossa/captures).
	Output string `yaml:"output,omitempty" json:"output,omitempty"`
	// Locales are the locales captured (default: the source locale).
	Locales []string `yaml:"locales,omitempty" json:"locales,omitempty"`
	// Locale says how a page is told the locale, when a route's URL has
	// no {locale}.
	Locale LocaleSelector `yaml:"locale,omitempty" json:"locale,omitempty"`
	// Viewports default to 1280×800 and 390×844.
	Viewports []Viewport     `yaml:"viewports,omitempty" json:"viewports,omitempty"`
	Routes    []CaptureRoute `yaml:"routes,omitempty" json:"routes,omitempty"`
	// Cookies and Headers log in as a fixture user. Values may reference
	// environment variables as ${NAME}; they're never printed. Headers go
	// only to the base URL's origin.
	Cookies []CaptureCookie   `yaml:"cookies,omitempty" json:"-"`
	Headers map[string]string `yaml:"headers,omitempty" json:"-"`
}

// LocaleSelector is how the page learns the locale: a query parameter
// (?lang=de) or a cookie on the base URL. Empty: the route URLs carry
// {locale}, or only the source locale is captured.
type LocaleSelector struct {
	Query  string `yaml:"query,omitempty" json:"query,omitempty"`
	Cookie string `yaml:"cookie,omitempty" json:"cookie,omitempty"`
}

// Viewport is a CSS viewport size, emulated with a device scale factor
// (default 1) and, for mobile, touch and meta-viewport handling.
type Viewport struct {
	Width             int     `yaml:"width" json:"width"`
	Height            int     `yaml:"height" json:"height"`
	DeviceScaleFactor float64 `yaml:"device_scale_factor,omitempty" json:"device_scale_factor,omitempty"`
	Mobile            bool    `yaml:"mobile,omitempty" json:"mobile,omitempty"`
}

// CaptureRoute is one page: its route pattern (as usages name it) and
// the concrete URL to open.
type CaptureRoute struct {
	// Route is the pattern: /checkout/[step].
	Route string `yaml:"route" json:"route"`
	// URL is a path relative to the base URL, or an absolute URL, and may
	// hold {locale}. Default: the route, when it has no dynamic segment.
	URL string `yaml:"url,omitempty" json:"url,omitempty"`
	// Playbook is a scout playbook (JSON) replayed after the page loads,
	// e.g. to open a dialog. Relative to glossa.yaml.
	Playbook string `yaml:"playbook,omitempty" json:"playbook,omitempty"`
}

// URLOrRoute is the URL to open, before {locale} is filled in.
func (r CaptureRoute) URLOrRoute() string {
	if r.URL != "" {
		return r.URL
	}
	return r.Route
}

// CaptureCookie is a cookie set on the base URL before each page loads.
type CaptureCookie struct {
	Name  string `yaml:"name" json:"name"`
	Value string `yaml:"value" json:"-"`
}

// DefaultCaptureOutput is where captures go without --upload.
const DefaultCaptureOutput = ".glossa/captures"

// DefaultViewports are captured when capture.viewports is empty.
var DefaultViewports = []Viewport{{Width: 1280, Height: 800}, {Width: 390, Height: 844}}

// LocalesOr is the locales to capture: capture.locales, else source.
func (c Capture) LocalesOr(source string) []string {
	if len(c.Locales) > 0 {
		return c.Locales
	}
	return []string{source}
}

// ViewportsOrDefault is the viewports to capture.
func (c Capture) ViewportsOrDefault() []Viewport {
	if len(c.Viewports) > 0 {
		return c.Viewports
	}
	return DefaultViewports
}

// CaptureOutput is the directory captures are written to.
func (c *Config) CaptureOutput() string {
	if c.Capture.Output != "" {
		return c.Resolve(c.Capture.Output)
	}
	return c.Resolve(DefaultCaptureOutput)
}

// MissingEnvError is a ${NAME} reference to an unset variable.
type MissingEnvError struct{ Name string }

func (e *MissingEnvError) Error() string {
	return fmt.Sprintf("environment variable %s is not set", e.Name)
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ExpandEnv replaces each ${NAME} in s with lookup(NAME). An unset
// variable is a *MissingEnvError; a malformed reference an error that
// never quotes s (values are secrets).
func ExpandEnv(s string, lookup func(string) (string, bool)) (string, error) {
	var b strings.Builder
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			b.WriteString(s)
			return b.String(), nil
		}
		b.WriteString(s[:i])
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			return "", errors.New("${ without a closing }")
		}
		name := s[i+2 : i+end]
		if !envName.MatchString(name) {
			return "", errors.New("${…} must name an environment variable (letters, digits and _)")
		}
		v, ok := lookup(name)
		if !ok {
			return "", &MissingEnvError{Name: name}
		}
		b.WriteString(v)
		s = s[i+end+1:]
	}
}

// token is an HTTP token (RFC 9110 §5.6.2): header and cookie names.
var token = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

// reservedHeaders are set by the browser or by glossa capture itself.
var reservedHeaders = map[string]bool{"host": true, "cookie": true, "content-length": true, "connection": true,
	"transfer-encoding": true, "upgrade": true, "te": true, "trailer": true, "keep-alive": true}

var queryName = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)

func (c *Config) validateCapture() error {
	cp := &c.Capture
	bad := func(field, format string, args ...any) error {
		return &InvalidError{Path: c.Path, Field: "capture." + field, Problem: fmt.Sprintf(format, args...)}
	}
	if cp.BaseURL != "" {
		if problem := checkAbsoluteURL(cp.BaseURL); problem != "" {
			return bad("base_url", "%s", problem)
		}
	}
	if cp.Application != "" && !ValidApplication(cp.Application) {
		return bad("application", "%q is not an application slug (lowercase letters, digits and -, e.g. web)", cp.Application)
	}
	if err := validateRoutes(cp.Routes, bad); err != nil {
		return err
	}
	if err := validateViewports(cp.Viewports, bad); err != nil {
		return err
	}
	if err := cp.validateLocales(c.SourceLocale, bad); err != nil {
		return err
	}
	return validateLogin(cp, bad)
}

type badFunc func(field, format string, args ...any) error

// checkAbsoluteURL explains why u isn't an http(s) URL without
// credentials, or returns "".
func checkAbsoluteURL(u string) string {
	p, err := url.Parse(u)
	switch {
	case err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "":
		return "must be an http or https URL (e.g. http://localhost:4173)"
	case p.User != nil:
		return "must not hold credentials (log in with capture.cookies or capture.headers)"
	}
	return ""
}

func validateRoutes(routes []CaptureRoute, bad badFunc) error {
	seen := map[[2]string]bool{}
	for i, r := range routes {
		f := fmt.Sprintf("routes[%d]", i)
		switch {
		case !strings.HasPrefix(r.Route, "/"):
			return bad(f+".route", "must start with / (a route pattern like /checkout/[step])")
		case len(r.Route) > 1024 || strings.ContainsAny(r.Route, " \t\r\n"):
			return bad(f+".route", "must be a route pattern of at most 1024 characters without spaces")
		case r.URL == "" && strings.Contains(r.Route, "["):
			return bad(f+".url", "%s has a dynamic segment: give the concrete URL to open (e.g. url: /checkout/payment)", r.Route)
		}
		if u := strings.ReplaceAll(r.URL, LocalePlaceholder, "xx"); u != "" && !strings.HasPrefix(u, "/") {
			if problem := checkAbsoluteURL(u); problem != "" {
				return bad(f+".url", "must be a path relative to capture.base_url (/checkout/payment) or an http or https URL")
			}
		}
		key := [2]string{r.Route, r.URLOrRoute()}
		if seen[key] {
			return bad(f, "%s (%s) is listed twice", r.Route, r.URLOrRoute())
		}
		seen[key] = true
	}
	return nil
}

func validateViewports(vs []Viewport, bad badFunc) error {
	seen := map[Viewport]bool{}
	for i, v := range vs {
		f := fmt.Sprintf("viewports[%d]", i)
		if v.Width < 1 || v.Width > 16384 || v.Height < 1 || v.Height > 16384 {
			return bad(f, "width and height must be 1–16384 CSS pixels (got %d×%d)", v.Width, v.Height)
		}
		if v.DeviceScaleFactor < 0 || v.DeviceScaleFactor > 4 {
			return bad(f+".device_scale_factor", "must be in (0, 4] (got %g)", v.DeviceScaleFactor)
		}
		if seen[v] {
			return bad(f, "%d×%d is listed twice", v.Width, v.Height)
		}
		seen[v] = true
	}
	return nil
}

func (cp *Capture) validateLocales(source string, bad badFunc) error {
	seen := map[string]bool{}
	for i, l := range cp.Locales {
		tag, err := bcp47.Parse(l)
		if err != nil {
			return bad(fmt.Sprintf("locales[%d]", i), "%q is not a BCP 47 locale (e.g. de, ja-JP)", l)
		}
		cp.Locales[i] = tag.String()
		if seen[cp.Locales[i]] {
			return bad(fmt.Sprintf("locales[%d]", i), "%s is listed twice", cp.Locales[i])
		}
		seen[cp.Locales[i]] = true
	}
	sel := cp.Locale
	switch {
	case sel.Query != "" && sel.Cookie != "":
		return bad("locale", "set query or cookie, not both")
	case sel.Query != "" && !queryName.MatchString(sel.Query):
		return bad("locale.query", "%q is not a query parameter name (letters, digits, _ . -)", sel.Query)
	case sel.Cookie != "" && !token.MatchString(sel.Cookie):
		return bad("locale.cookie", "%q is not a cookie name", sel.Cookie)
	}
	if sel.Query != "" || sel.Cookie != "" || len(cp.Routes) == 0 {
		return nil
	}
	onlySource := true
	for _, l := range cp.Locales {
		onlySource = onlySource && l == source
	}
	inURL := true
	for _, r := range cp.Routes {
		inURL = inURL && strings.Contains(r.URLOrRoute(), LocalePlaceholder)
	}
	if !onlySource && !inURL {
		return bad("locale", "how does the page pick the locale? Set locale.query (?lang=de) or locale.cookie, or put %s in every route's url", LocalePlaceholder)
	}
	return nil
}

func validateLogin(cp *Capture, bad badFunc) error {
	syntax := func(string) (string, bool) { return "", true }
	for i, ck := range cp.Cookies {
		f := fmt.Sprintf("cookies[%d]", i)
		if !token.MatchString(ck.Name) {
			return bad(f+".name", "%q is not a cookie name", ck.Name)
		}
		if ck.Value == "" {
			return bad(f+".value", "is required (reference a secret as ${NAME})")
		}
		if _, err := ExpandEnv(ck.Value, syntax); err != nil {
			return bad(f+".value", "%v", err)
		}
	}
	names := make([]string, 0, len(cp.Headers))
	for name := range cp.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !token.MatchString(name) {
			return bad("headers", "%q is not a header name", name)
		}
		if reservedHeaders[strings.ToLower(name)] {
			return bad("headers", "%s can't be set (use capture.cookies for cookies)", name)
		}
		if _, err := ExpandEnv(cp.Headers[name], syntax); err != nil {
			return bad("headers."+name, "%v", err)
		}
	}
	return nil
}
