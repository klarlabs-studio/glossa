package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/capture"
	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/cli/extract"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

const captureUsage = `capture [--check] [--upload] [--out DIR] [--base-url URL] [--no-coverage] [--application SLUG] [--commit SHA] [--branch NAME] [--cdp URL]

Screenshots the pages of the capture plan (glossa.yaml capture:) in headless Chrome at
each viewport and locale, with the regions where messages render (RFC 0004 §3.2).
Run it against a preview with fixture data: a page whose runtime reports a production
manifest is refused, and data-glossa-redact elements are blacked out. Without --upload,
the glossa.captures/v1 manifest and its PNGs go to capture.output (.glossa/captures).
The coverage report lists messages with a current usage but no visible region; it reads
the Context API (--no-coverage skips it). --cdp attaches to a browser you started
yourself instead of launching one; its host must be loopback unless --cdp-allow-remote.

--check checks the project with the captures in it: what the visual probe pass measured
in each page (RFC 0005 §5), reported with every other layer and graded by the project's
check policy, with the same exit codes as glossa check (0 ok · 1 the policy failed
the run · 2 usage or config · 3 no server and no cached policy · 4 a layer was lost).
A visual finding is a warning the first time it is seen and may only be an error once
the same fingerprint comes back in the next capture of the same route, viewport and
locale, because a headless browser's text metrics move with the fonts it found.`

// runCapture takes the captures; tests replace it to run without Chrome.
var runCapture = capture.Run

type captureFlags struct {
	upload, noCoverage, check   bool
	out, baseURL                string
	application, commit, branch string
	timeout                     time.Duration
	cdp                         string
	cdpAllowRemote              bool
}

// captureJSON is `glossa capture --json`.
type captureJSON struct {
	Schema      string            `json:"schema"`
	Application string            `json:"application"`
	Commit      string            `json:"commit"`
	Branch      string            `json:"branch"`
	Captures    []captureSummary  `json:"captures"`
	Output      *captureOutput    `json:"output,omitempty"`
	Upload      *captureUpload    `json:"upload,omitempty"`
	Coverage    *capture.Coverage `json:"coverage"`
	// Check is `--check`: the whole check run, in the shape `glossa
	// check --json` prints, so a CI job that already reads one reads
	// this one.
	Check *checkJSON `json:"check,omitempty"`

	checked  *captureCheck
	shots    []capture.Shot
	doc      capture.Document
	manifest []byte            // doc as written
	images   map[string][]byte // digest → PNG
}

type captureSummary struct {
	Route    string           `json:"route"`
	URL      string           `json:"url"`
	Locale   string           `json:"locale"`
	Viewport capture.Viewport `json:"viewport"`
	Image    capture.Image    `json:"image"`
	Renders  int              `json:"renders"`
	Regions  int              `json:"regions"`
	Visible  int              `json:"visible"`
	// Probes is how many findings the visual probe pass measured on this
	// capture — what the manifest carries for it (RFC 0005 §5).
	Probes    int  `json:"probes"`
	Redacted  int  `json:"redacted"`
	Truncated bool `json:"truncated"`
}

type captureOutput struct {
	Dir      string `json:"dir"`
	Manifest string `json:"manifest"`
	Images   int    `json:"images"`
}

type captureUpload struct {
	Build              string `json:"build"`
	Captures           int    `json:"captures"`
	ImagesStored       int    `json:"images_stored"`
	ImagesDeduplicated int    `json:"images_deduplicated"`
	// Findings is how many of the manifest's visual findings the server
	// stored. It is the server's number and not a count of what was
	// sent: 0 on a replay, and 0 where the project's policy switched the
	// visual layer off, which is the project not paying for a layer it
	// does not compute (RFC 0005 §4.1).
	Findings    int      `json:"findings"`
	UnknownKeys []string `json:"unknown_keys"`
	Replayed    bool     `json:"replayed"`
}

func runCaptureCmd(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags(captureUsage)
	var f captureFlags
	fs.BoolVar(&f.check, "check", false, "check the project with this run's visual findings in it, and exit as glossa check does")
	fs.BoolVar(&f.upload, "upload", false, "send the captures to the server (the Captures API) instead of writing them")
	fs.BoolVar(&f.noCoverage, "no-coverage", false, "skip the coverage report (it reads the current usages from the server)")
	fs.StringVar(&f.out, "out", "", "where to write the manifest and PNGs (default: capture.output, .glossa/captures)")
	fs.StringVar(&f.baseURL, "base-url", "", "where the app runs (default: capture.base_url)")
	fs.StringVar(&f.application, "application", "", "the application's slug (default: capture.application, GLOSSA_APPLICATION, extract.application)")
	fs.StringVar(&f.commit, "commit", "", "the commit the app is built from (default: CI, else git HEAD)")
	fs.StringVar(&f.branch, "branch", "", "the branch the app is built from (default: CI, else git)")
	fs.DurationVar(&f.timeout, "timeout", capture.DefaultTimeout, "the longest a page may take to load, settle or replay")
	fs.StringVar(&f.cdp, "cdp", "", "attach to a browser you started yourself at this DevTools endpoint (ws:// or http://) instead of starting Chrome (default: GLOSSA_CAPTURE_CDP)")
	fs.BoolVar(&f.cdpAllowRemote, "cdp-allow-remote", false, "let --cdp name a host that isn't loopback (default: GLOSSA_CAPTURE_CDP_ALLOW_REMOTE)")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageError(inv.name, "capture takes no arguments (the pages are glossa.yaml's capture.routes)")
	}
	cfg, err := inv.loadConfig()
	if err != nil {
		return err
	}
	plan, err := capture.NewPlan(cfg, f.baseURL, inv.env.getenv)
	if err != nil {
		return planError(cfg, err)
	}
	app := firstOf(f.application, inv.env.getenv("GLOSSA_APPLICATION"), cfg.Capture.Application, cfg.Extract.Application)
	header, err := inv.buildHeader(ctx, cfg, "captures", app, "capture.application", f.commit, f.branch)
	if err != nil {
		return err
	}
	// Connect before Chrome starts: a bad token fails in a second, not
	// after every page was captured.
	var p *project
	var appID string
	if f.upload || !f.noCoverage {
		if p, err = inv.connectWith(ctx, cfg); err != nil {
			return err
		}
		if appID, err = inv.applicationID(ctx, p, header.Application); err != nil {
			return err
		}
	}
	out := &captureJSON{Schema: "glossa.cli.capture/v1", Application: header.Application, Commit: header.Commit, Branch: header.Branch}
	opts := capture.Options{
		Timeout:        f.timeout,
		Progress:       inv.captureProgress(len(plan.Jobs)),
		CDP:            firstOf(f.cdp, inv.env.getenv("GLOSSA_CAPTURE_CDP")),
		AllowRemoteCDP: f.cdpAllowRemote || envTrue(inv.env.getenv("GLOSSA_CAPTURE_CDP_ALLOW_REMOTE")),
	}
	// The policy is resolved before Chrome starts, because the probe pass
	// measures against its thresholds (RFC 0005 §5.2) and because a check
	// that cannot resolve its policy must fail before it captures forty
	// pages rather than after.
	if f.check {
		if out.checked, err = inv.startCheck(ctx, cfg); err != nil {
			return err
		}
		opts.Probe = probeOptions(out.checked.policy)
	}
	if out.shots, err = runCapture(ctx, plan, opts); err != nil {
		return captureError(err)
	}
	if err := out.assemble(header); err != nil {
		return err
	}
	if f.upload {
		if err := inv.uploadCaptures(ctx, p, out); err != nil {
			return err
		}
	} else if err := out.write(inv.captureDir(cfg, f.out)); err != nil {
		return err
	}
	if !f.noCoverage {
		if out.Coverage, err = inv.coverage(ctx, p, appID, out.doc); err != nil {
			return err
		}
	}
	if f.check {
		out.Check = inv.finishCheck(ctx, cfg, header, out)
	}
	if err := inv.emit(out, func(pr *printer) { printCapture(pr, out) }); err != nil {
		return err
	}
	if out.Check != nil {
		return checkExit(*out.Check)
	}
	return nil
}

// assemble builds the document, in the plan's order, and the summaries.
func (out *captureJSON) assemble(h extract.Header) error {
	caps := make([]capture.Capture, 0, len(out.shots))
	out.images = map[string][]byte{}
	for _, s := range out.shots {
		c := s.Capture
		// The probes ride with the capture they were measured on. They
		// are the only part of the visual layer nothing but the page
		// could produce, and a manifest that left them behind would make
		// `visual` a layer that exists in the terminal and nowhere else.
		c.Findings = capture.Findings(s.Probes)
		caps = append(caps, c)
		out.images[c.Image.SHA256] = s.PNG
		visible := 0
		for _, r := range c.Regions {
			if r.Visible {
				visible++
			}
		}
		out.Captures = append(out.Captures, captureSummary{Route: c.Route, URL: c.URL, Locale: c.Locale, Viewport: c.Viewport, Image: c.Image,
			Renders: len(c.Renders), Regions: len(c.Regions), Visible: visible, Probes: len(s.Probes),
			Redacted: s.Redacted, Truncated: s.Truncated})
	}
	out.doc = capture.NewDocument(h, caps)
	var err error
	out.manifest, err = json.MarshalIndent(out.doc, "", "  ")
	return err
}

// captureDir is --out (relative to the working directory), else
// capture.output.
func (inv *invocation) captureDir(cfg *config.Config, flag string) string {
	switch {
	case flag == "":
		return cfg.CaptureOutput()
	case filepath.IsAbs(flag):
		return flag
	default:
		return filepath.Join(inv.env.Dir, flag)
	}
}

var imageFile = regexp.MustCompile(`^[0-9a-f]{64}\.png$`)

// write puts captures.json and <sha256>.png into dir, and removes PNGs of
// earlier runs that the manifest no longer names.
func (out *captureJSON) write(dir string) error {
	fail := func(err error) error {
		return &Error{Exit: ExitUsage, Code: "write_failed", What: "can't write the captures", Where: dir, Why: err.Error(),
			Fix: "check capture.output or --out"}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail(err)
	}
	for digest, data := range out.images {
		if err := os.WriteFile(filepath.Join(dir, digest+".png"), data, 0o644); err != nil { //nolint:gosec // generated images
			return fail(err)
		}
	}
	manifest := filepath.Join(dir, "captures.json")
	if err := os.WriteFile(manifest, append(out.manifest, '\n'), 0o644); err != nil { //nolint:gosec // generated manifest
		return fail(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fail(err)
	}
	for _, e := range entries {
		name := e.Name()
		if imageFile.MatchString(name) && out.images[name[:64]] == nil {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return fail(err)
			}
		}
	}
	out.Output = &captureOutput{Dir: dir, Manifest: manifest, Images: len(out.images)}
	return nil
}

// uploadCaptures sends the manifest and its images to the Captures API.
// The server refusing them is a usage error (exit 2).
func (inv *invocation) uploadCaptures(ctx context.Context, p *project, out *captureJSON) error {
	images := make(map[string]io.Reader, len(out.images))
	for digest, data := range out.images {
		images[digest] = bytes.NewReader(data)
	}
	manifest, err := json.Marshal(out.doc)
	if err != nil {
		return err
	}
	up, err := p.client.UploadCaptures(ctx, p.scope, manifest, images)
	if err != nil {
		err = inv.apiError(err, "can't upload the captures")
		var e *Error
		var ae *remote.APIError
		if errors.As(err, &e) && errors.As(err, &ae) && (ae.Status == 400 || ae.Status == 413 || ae.Status == 422) {
			e.Exit = ExitUsage
			e.Fix = "fix the captures (schema: runtimes/testdata/schemas/captures.v1.schema.json), or create the application in the project"
		}
		return err
	}
	out.Upload = &captureUpload{Build: string(up.Build.Id), Captures: up.Captures, ImagesStored: up.ImagesStored,
		ImagesDeduplicated: up.ImagesDeduplicated, Findings: up.Findings, UnknownKeys: up.UnknownKeys,
		Replayed: up.Replayed}
	return nil
}

// applicationID resolves the application's slug in the project.
func (inv *invocation) applicationID(ctx context.Context, p *project, slug string) (string, error) {
	apps, err := p.client.Applications(ctx, p.scope)
	if err != nil {
		return "", inv.apiError(err, "can't read the project's applications")
	}
	for _, a := range apps {
		if string(a.Slug) == slug {
			return a.Id, nil
		}
	}
	return "", &Error{Exit: ExitUsage, Code: "unknown_application", What: fmt.Sprintf("the project has no application %q", slug),
		Where: p.cfg.Path + " (capture.application)",
		Fix:   "create the application in Studio, pass --application with one the project has, or --no-coverage for a local run"}
}

// coverage compares the application's current usages (in the branch's
// view) with the captures: the messages no capture shows.
func (inv *invocation) coverage(ctx context.Context, p *project, appID string, doc capture.Document) (*capture.Coverage, error) {
	all, err := p.client.CurrentUsages(ctx, p.scope, doc.Branch)
	if err != nil {
		return nil, inv.apiError(err, "can't read the current usages for the coverage report")
	}
	var usages []capture.Usage
	for _, u := range all {
		if u.ApplicationId != appID || u.MessageId == nil {
			continue // another application's, or a key the catalog doesn't know
		}
		usages = append(usages, capture.Usage{Key: u.Key, File: u.File, Line: u.Line, Route: deref(u.Route)})
	}
	c := capture.Cover(usages, doc)
	return &c, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// captureProgress prints each page as it is captured, on stderr, unless
// the output is JSON or quiet.
func (inv *invocation) captureProgress(n int) func(int, capture.Job) {
	if inv.json || inv.quiet {
		return nil
	}
	return func(i int, j capture.Job) {
		fmt.Fprintf(inv.env.Stderr, "  [%d/%d] %s at %d×%d in %s\n", i+1, n, j.URL, j.Viewport.Width, j.Viewport.Height, j.Locale)
	}
}

func planError(cfg *config.Config, err error) error {
	var pe *capture.PlanError
	if !errors.As(err, &pe) {
		return err
	}
	where := cfg.Path + " (" + pe.Field + ")"
	if pe.Field == "--base-url" {
		where = "--base-url"
	}
	return &Error{Exit: ExitUsage, Code: "invalid_capture_plan", What: "the capture plan can't run", Where: where, Why: pe.Problem,
		Fix: "fix capture: in glossa.yaml (see `glossa capture --help`)"}
}

// envTrue reads a boolean environment variable.
func envTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// refusalFixes say what to do about each refusal.
var refusalFixes = map[string]string{
	"production_page":     "point capture.base_url (or --base-url) at a preview deployment with fixture data",
	"environment_unknown": "make sure the page activates its release (a preview or development manifest) when it loads",
	"no_runtime":          "capture pages that render with @klarlabs-studio/glossa (t() or its components)",
	"locale_mismatch":     "check capture.locale: the page must pick the locale from the query parameter, cookie or URL the plan sets",
}

// cdpFixes say what to do about a --cdp endpoint.
var cdpFixes = map[string]string{
	"invalid_cdp_endpoint": "pass a DevTools endpoint on loopback (ws://127.0.0.1:9222/devtools/browser/… or http://127.0.0.1:9222), or --cdp-allow-remote for a browser on another host",
	"cdp_unreachable":      "start the browser with --remote-debugging-port and check the endpoint (--cdp or GLOSSA_CAPTURE_CDP)",
}

func captureError(err error) error {
	var r *capture.Refusal
	var pe *capture.PageError
	var be *capture.BrowserError
	var ce *capture.CDPError
	switch {
	case errors.As(err, &r):
		return &Error{Exit: ExitUsage, Code: r.Code, What: "refused to capture the page", Where: r.URL, Why: r.Reason, Fix: refusalFixes[r.Code], Err: err}
	case errors.As(err, &pe):
		return &Error{Exit: ExitUsage, Code: "page_failed", What: "can't " + pe.Step, Where: pe.URL, Why: pe.Err.Error(),
			Fix: "check that the app runs at capture.base_url and the route's url and playbook are right", Err: err}
	case errors.As(err, &ce):
		return &Error{Exit: ExitUsage, Code: ce.Code, What: "can't attach to the browser", Where: ce.Endpoint, Why: ce.Reason,
			Fix: cdpFixes[ce.Code], Err: err}
	case errors.As(err, &be):
		return &Error{Exit: ExitUsage, Code: "no_browser", What: "can't start Chrome", Why: be.Err.Error(),
			Fix: "install Google Chrome or Chromium (google-chrome or chromium on PATH), or attach to one with --cdp", Err: err}
	}
	return err
}

func printCapture(p *printer, out *captureJSON) {
	p.line("%s %s of %s at %s (%s)", p.pass(), plural(len(out.Captures), "capture", "captures"), out.Application, shortCommit(out.Commit), out.Branch)
	rows := [][]string{{"route", "locale", "viewport", "regions", "visible", "redacted"}}
	for _, c := range out.Captures {
		rows = append(rows, []string{c.Route, c.Locale, fmt.Sprintf("%d×%d", c.Viewport.Width, c.Viewport.Height),
			fmt.Sprint(c.Regions), fmt.Sprint(c.Visible), fmt.Sprint(c.Redacted)})
	}
	p.table(rows)
	for _, c := range out.Captures {
		if c.Truncated {
			p.line("%s %s at %d×%d is taller than an image holds: the screenshot stops at %d px", p.caution(), c.URL, c.Viewport.Width, c.Viewport.Height, c.Image.Height)
		}
	}
	switch {
	case out.Output != nil:
		p.line("%s Wrote %s and %s to %s", p.pass(), filepath.Base(out.Output.Manifest), plural(out.Output.Images, "image", "images"), out.Output.Dir)
	case out.Upload != nil:
		u := out.Upload
		what := "Uploaded"
		if u.Replayed {
			what = "Already uploaded"
		}
		p.line("%s %s %s to build %s (%d images stored, %d already there)", p.pass(), what, plural(u.Captures, "capture", "captures"), u.Build,
			u.ImagesStored, u.ImagesDeduplicated)
		// The server's number, not a count of what was sent: it is what
		// says the visual layer reached the pull request rather than
		// stopping in this terminal.
		if u.Findings > 0 {
			p.line("  %s", p.dim(plural(u.Findings, "visual finding", "visual findings")+" stored"))
		}
		if len(u.UnknownKeys) > 0 {
			p.line("%s %s the catalog doesn't know (`glossa push` the catalog first)", p.caution(), plural(len(u.UnknownKeys), "region names a key", "regions name keys"))
		}
	}
	printCoverage(p, out.Coverage)
	printVisual(p, out)
	if out.Check != nil {
		p.line("")
		printCheck(p, out.checked.run, *out.Check, out.checked.report)
	}
}

func printCoverage(p *printer, c *capture.Coverage) {
	if c == nil {
		return
	}
	if len(c.NotCaptured) == 0 {
		p.line("%s every used message is captured (%d of %d)", p.pass(), c.Captured, c.Messages)
		return
	}
	p.line("%s %d of %d used messages not captured (add routes or playbooks where they appear):", p.caution(), len(c.NotCaptured), c.Messages)
	for i, n := range c.NotCaptured {
		if i == maxFindingsPerGroup {
			p.line("  %s", p.dim(fmt.Sprintf("… and %d more", len(c.NotCaptured)-i)))
			break
		}
		where := fmt.Sprintf("%s:%d", n.File, n.Line)
		if len(n.Routes) > 0 {
			where += " " + p.dim(fmt.Sprint(n.Routes))
		}
		p.line("  %s  %s", n.Key, where)
	}
}
