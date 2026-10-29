package capture

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"math"
	"net/url"
	"strings"
	"time"

	browse "go.klarlabs.de/scout"
	"go.klarlabs.de/scout/agent"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// agentScript is @klarlabs-studio/glossa/capture's capture agent (src/agent.ts), bundled
// by `pnpm --filter @klarlabs-studio/glossa-capture build:cli`. A test in that package
// fails when this copy is stale.
//
//go:embed agent.js
var agentScript string

// Server limits for an image (RFC 0004 §3.3): the screenshot of a taller
// page stops where an image would exceed them.
const (
	// MaxImageBytes is the largest PNG the Captures API takes.
	MaxImageBytes = 10 << 20
	// MaxImagePixels is the most pixels it stores.
	MaxImagePixels = 40_000_000
	// maxImageSide keeps each side within what Chrome renders reliably in one capture.
	maxImageSide = 16384
)

// DefaultTimeout bounds each browser step.
const DefaultTimeout = 30 * time.Second

// Options configure Run.
type Options struct {
	// Timeout bounds each browser step (default DefaultTimeout).
	Timeout time.Duration
	// Progress, when set, is called before each job.
	Progress func(i int, j Job)
	// CDP, when set, is the DevTools endpoint (ws://, wss://, http:// or
	// https://) of a browser someone else started: Glossa attaches to it
	// instead of launching Chrome, and leaves it running.
	CDP string
	// AllowRemoteCDP lets CDP name a host that isn't loopback.
	AllowRemoteCDP bool
	// Probe, when set, is what the visual probe pass measures against
	// (RFC 0005 §5.2): the check policy's thresholds, handed to the page
	// with the capture's options. Nil leaves the probe on the defaults
	// compiled into it, which is what a capture with no policy in reach
	// measures against.
	Probe *ProbeOptions
}

// ProbeOptions is what `glossa capture` hands `session.collect()`: the
// numbers the visual probe pass measures against, which are the check
// policy's (checkpolicy.VisualThresholds) and not the probe's.
//
// The field names are the page's, because this struct is the wire — it
// marshals straight into the `collect()` call. Every length is in CSS
// pixels, never device pixels (RFC 0005 §5.2).
type ProbeOptions struct {
	// Slack is how much more content than box a region may have before
	// text-clipped is reported.
	Slack int `json:"slack"`
	// Overlap is the per cent of the smaller of two regions they must
	// share before region-overlap is reported.
	Overlap int `json:"overlap"`
	// Tolerance is the line boxes a translation may gain over the source
	// capture before line-growth is reported.
	Tolerance int `json:"tolerance"`
	// ByLocale is the tolerance of the locales that need their own. It
	// never goes to the page: a page is captured in one locale, so the
	// page is handed that locale's number as Tolerance.
	ByLocale map[string]int `json:"-"`
	// Max caps the findings one capture reports (RFC 0005 §10).
	Max int `json:"max"`
}

// in returns the options as the page in locale is given them: the
// locale's own line-growth tolerance where the policy names one.
func (o ProbeOptions) in(locale string) ProbeOptions {
	if n, ok := o.ByLocale[locale]; ok {
		o.Tolerance = n
	}
	return o
}

// Shot is one capture and its PNG.
type Shot struct {
	Capture Capture
	PNG     []byte
	// Truncated is true when the page was taller than an image holds.
	Truncated bool
	// Redacted is how many data-glossa-redact elements were blacked out.
	Redacted int
	// Probes are what the visual probe pass found (RFC 0005 §5.2), as
	// the page wrote them: the `glossa.finding/v1` shape without a
	// fingerprint, which only a caller that knows the catalog can
	// compute.
	//
	// They are not Capture's own, because a probe finding is Quality's
	// and a capture is Context's. The manifest carries them beside the
	// capture they were measured on (Capture.Findings, built by
	// Findings), in the shape
	// runtimes/testdata/schemas/captures.v1.schema.json publishes: the
	// same shape with the fingerprint and the capture left for the
	// ingest to complete.
	Probes []domain.Finding
	// Metrics is the line boxes each key covered on this page, which the
	// next locale's capture of the same route and viewport compares
	// itself against.
	Metrics map[string]int
}

// Refusal is a page glossa capture won't capture: a production page, one
// without a runtime, or one that doesn't render the requested locale.
type Refusal struct {
	URL, Code, Reason string
}

func (e *Refusal) Error() string { return e.URL + ": " + e.Reason }

// PageError is a page that failed to load, replay or screenshot.
type PageError struct {
	URL, Step string
	Err       error
}

func (e *PageError) Error() string { return fmt.Sprintf("%s: %s: %v", e.URL, e.Step, e.Err) }
func (e *PageError) Unwrap() error { return e.Err }

// BrowserError is Chrome failing to start.
type BrowserError struct{ Err error }

func (e *BrowserError) Error() string { return "can't start Chrome: " + e.Err.Error() }
func (e *BrowserError) Unwrap() error { return e.Err }

// Run takes every capture of p in headless Chrome, in the plan's order.
// It stops at the first page it can't or won't capture.
//
// With o.CDP it attaches to a browser the caller started instead of
// launching one (a CI sandbox where Chrome's own sandbox can't run, or a
// remote or containerised Chrome). Then it owns only the pages it opens:
// each is closed after its capture, and the browser is left running.
func Run(ctx context.Context, p *Plan, o Options) ([]Shot, error) {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	// Capture runs against previews on localhost and private networks, in
	// CI: the addresses scout blocks by default are what it's for.
	opts := []browse.Option{browse.WithHeadless(true), browse.WithTimeout(o.Timeout), browse.WithAllowPrivateIPs(true)}
	attached := strings.TrimSpace(o.CDP) != ""
	if attached {
		ws, err := resolveCDP(o.CDP, o.AllowRemoteCDP)
		if err != nil {
			return nil, err
		}
		opts = append(opts, browse.WithRemoteCDP(ws))
	}
	engine := browse.New(opts...)
	if err := engine.Launch(); err != nil {
		if attached {
			return nil, &CDPError{Endpoint: o.CDP, Code: "cdp_unreachable", Reason: err.Error()}
		}
		return nil, &BrowserError{Err: err}
	}
	// Close drops the CDP connection and kills only a browser scout
	// started itself, so an attached one outlives the run.
	defer func() { _ = engine.Close() }()
	shots := make([]Shot, 0, len(p.Jobs))
	for i, j := range p.Jobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if o.Progress != nil {
			o.Progress(i, j)
		}
		s, err := shoot(engine, p, j, o)
		if err != nil {
			return nil, err
		}
		shots = append(shots, s)
	}
	return shots, nil
}

// pageStatus is the agent's status(): what the page's runtimes report.
type pageStatus struct {
	Runtimes     int       `json:"runtimes"`
	Environments []*string `json:"environments"`
	Locales      []*string `json:"locales"`
}

// pageCapture is the agent's collect().
type pageCapture struct {
	Renders  []Render         `json:"renders"`
	Regions  []Region         `json:"regions"`
	Probes   []domain.Finding `json:"probes"`
	Metrics  map[string]int   `json:"metrics"`
	Width    float64          `json:"width"`
	Height   float64          `json:"height"`
	Redacted []Box            `json:"redacted"`
}

// collectOptions is the argument `a.collect()` is called with for a
// page in locale: the policy's thresholds, or nothing at all when the
// caller has none and the probe's own defaults stand.
func collectOptions(o Options, locale string) string {
	if o.Probe == nil {
		return ""
	}
	b, err := json.Marshal(o.Probe.in(locale))
	if err != nil {
		return ""
	}
	return string(b)
}

// shoot opens one page on a fresh tab, checks it may be captured, and
// returns its regions and full-page PNG.
func shoot(e *browse.Engine, p *Plan, j Job, o Options) (Shot, error) {
	fail := func(step string, err error) (Shot, error) {
		return Shot{}, &PageError{URL: j.URL, Step: step, Err: err}
	}
	page, err := e.NewPage()
	if err != nil {
		return fail("open a tab", err)
	}
	defer func() { _ = page.Close() }()
	if err := prepare(page, p, j); err != nil {
		return fail("prepare the tab", err)
	}
	if err := page.Navigate(j.URL); err != nil {
		return fail("load the page", err)
	}
	if err := call(page, "a.settle()", nil); err != nil {
		return fail("wait for the page", err)
	}
	var st pageStatus
	if err := call(page, "a.status()", &st); err != nil {
		return fail("read the page's runtimes", err)
	}
	if r := refuse(j, st); r != nil {
		return Shot{}, r
	}
	if j.Playbook != nil {
		if err := replay(page, j, o.Timeout); err != nil {
			return Shot{}, err
		}
		if err := call(page, "a.settle()", nil); err != nil {
			return fail("wait for the page", err)
		}
	}
	var pc pageCapture
	if err := call(page, "a.collect("+collectOptions(o, j.Locale)+")", &pc); err != nil {
		return fail("collect regions", err)
	}
	return screenshot(page, j, pc)
}

// prepare injects the capture agent before any page script, emulates the
// viewport, and sets the fixture login: cookies for the base URL, headers
// only on requests to its origin.
func prepare(page *browse.Page, p *Plan, j Job) error {
	if _, err := page.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": agentScript}); err != nil {
		return err
	}
	v := j.Viewport
	if err := page.SetDeviceMetrics(v.Width, v.Height, scale(v.DeviceScaleFactor), v.Mobile); err != nil {
		return err
	}
	// Overlay scrollbars, as on phones and macOS: the page lays out at the
	// viewport's full width on every platform, and the image is that wide.
	if _, err := page.Call("Emulation.setScrollbarsHidden", map[string]any{"hidden": true}); err != nil {
		return err
	}
	if len(p.Headers) > 0 {
		headers := p.Headers
		if _, err := page.InterceptRequests(browse.RequestRule{
			Name: "glossa-capture-login",
			Decide: func(r browse.InterceptedRequest) browse.RequestVerdict {
				if origin(r.URL) != p.Origin {
					return browse.RequestVerdict{}
				}
				return browse.RequestVerdict{AddHeaders: headers}
			},
		}); err != nil {
			return err
		}
	}
	if len(j.Cookies) > 0 {
		if _, err := page.Call("Network.enable", nil); err != nil {
			return err
		}
	}
	for _, c := range j.Cookies {
		if _, err := page.Call("Network.setCookie", map[string]any{"name": c.Name, "value": c.Value, "url": c.URL}); err != nil {
			// The error names the cookie, never its value.
			return fmt.Errorf("can't set cookie %s: %w", c.Name, err)
		}
	}
	return nil
}

func scale(f float64) float64 {
	if f <= 0 {
		return 1
	}
	return f
}

func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// call runs an expression on the page's capture agent (as a) and decodes
// its JSON result into out (nil: ignore it).
func call(page *browse.Page, expr string, out any) error {
	v, err := page.Evaluate(`(async () => {
  const a = globalThis.__glossaCapture;
  if (!a) throw new Error("the capture agent isn't on the page (not an HTML page?)");
  return JSON.stringify(await ` + expr + `);
})()`)
	if err != nil || out == nil {
		return err
	}
	s, ok := v.(string)
	if !ok {
		return errors.New("the capture agent answered nothing")
	}
	return json.Unmarshal([]byte(s), out)
}

// refuse says why the page must not be captured, or nil: its runtimes
// must report a non-production manifest and render the job's locale.
func refuse(j Job, st pageStatus) *Refusal {
	r := &Refusal{URL: j.URL}
	switch {
	case st.Runtimes == 0:
		r.Code, r.Reason = "no_runtime", "the page created no Glossa runtime (@klarlabs-studio/glossa), so nothing marks its messages"
		return r
	case len(st.Environments) != st.Runtimes || len(st.Locales) != st.Runtimes:
		r.Code, r.Reason = "environment_unknown", "the capture agent's status is malformed"
		return r
	}
	for _, env := range st.Environments {
		switch {
		case env == nil:
			r.Code, r.Reason = "environment_unknown", "a Glossa runtime on the page has no active release, so it can't tell whether the page is production"
			return r
		case *env == "production":
			r.Code, r.Reason = "production_page", "the page's runtime reports a production manifest; capture runs against a preview with fixture data only"
			return r
		}
	}
	var locales []string
	for _, l := range st.Locales {
		if l != nil && strings.EqualFold(*l, j.Locale) {
			return nil
		}
		if l != nil {
			locales = append(locales, *l)
		}
	}
	r.Code, r.Reason = "locale_mismatch", fmt.Sprintf("asked for %s, but the page renders %s", j.Locale, strings.Join(locales, ", "))
	return r
}

// onePage is the scout Browser a playbook replays on: the capture's own
// tab, which it may neither replace nor close.
type onePage struct{ page *browse.Page }

var errOnePage = errors.New("a capture playbook stays on its page")

func (o onePage) NewPage() (*browse.Page, error)         { return nil, errOnePage }
func (o onePage) NewPageAt(string) (*browse.Page, error) { return nil, errOnePage }
func (o onePage) ExistingPage() (*browse.Page, error)    { return o.page, nil }
func (o onePage) Close() error                           { return nil }

var _ browse.Browser = onePage{}

// replay runs the route's playbook on the page (opening a dialog, say).
func replay(page *browse.Page, j Job, timeout time.Duration) error {
	s := agent.NewSessionFromBrowser(onePage{page}, agent.SessionConfig{Headless: true, Timeout: timeout, AllowPrivateIPs: true})
	res, err := s.ReplayPlaybook(j.Playbook)
	if err != nil {
		return &PageError{URL: j.URL, Step: "replay the playbook", Err: err}
	}
	if !res.Success {
		return &PageError{URL: j.URL, Step: fmt.Sprintf("replay the playbook (step %d of %d)", res.FailedAt, res.TotalSteps), Err: errors.New(res.Error)}
	}
	return nil
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// screenshot takes the full page as PNG, as tall as an image may be, and
// pairs it with the regions.
func screenshot(page *browse.Page, j Job, pc pageCapture) (Shot, error) {
	dsf := scale(j.Viewport.DeviceScaleFactor)
	width := math.Min(math.Max(math.Ceil(pc.Width), 1), math.Floor(maxImageSide/dsf))
	height := math.Max(math.Ceil(pc.Height), 1)
	limit := math.Min(math.Floor(maxImageSide/dsf), math.Floor(MaxImagePixels/(width*dsf*dsf)))
	truncated := height > limit
	height = math.Min(height, limit)
	data, err := page.ScreenshotWithOptions(browse.ScreenshotOptions{
		Format: "png", FullPage: true, MaxSize: MaxImageBytes,
		Clip: &browse.ClipRegion{X: 0, Y: 0, Width: width, Height: height},
	})
	if err != nil {
		return Shot{}, &PageError{URL: j.URL, Step: "take the screenshot", Err: err}
	}
	if !bytes.HasPrefix(data, pngSignature) {
		// scout re-encodes an image over MaxSize as JPEG; the API takes PNG only.
		return Shot{}, &PageError{URL: j.URL, Step: "take the screenshot",
			Err: fmt.Errorf("the page's PNG is over %d MB; capture a shorter page or a smaller viewport", MaxImageBytes>>20)}
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Shot{}, &PageError{URL: j.URL, Step: "take the screenshot", Err: err}
	}
	sum := sha256.Sum256(data)
	regions := pc.Regions
	if regions == nil {
		regions = []Region{}
	}
	for i := range regions {
		if regions[i].Box.Y >= height {
			regions[i].Visible = false // below the cut
		}
	}
	renders := pc.Renders
	if renders == nil {
		renders = []Render{}
	}
	vp := Viewport{Width: j.Viewport.Width, Height: j.Viewport.Height}
	if dsf != 1 {
		vp.DeviceScaleFactor = dsf
	}
	return Shot{
		Capture: Capture{Route: j.Route, URL: j.URL, Viewport: vp, Locale: j.Locale,
			Image:   Image{SHA256: hex.EncodeToString(sum[:]), Width: cfg.Width, Height: cfg.Height},
			Renders: renders, Regions: regions},
		PNG: data, Truncated: truncated, Redacted: len(pc.Redacted),
		Probes: pc.Probes, Metrics: pc.Metrics,
	}, nil
}
