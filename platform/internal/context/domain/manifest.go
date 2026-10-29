package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// CapturesSchema is the manifest type `glossa capture` writes.
const CapturesSchema = "glossa.captures/v1"

// Limits of a capture upload (RFC 0004 §3.3, §10).
const (
	// MaxManifestBytes bounds a captures manifest.
	MaxManifestBytes = 20 << 20
	// MaxImageBytes bounds one uploaded image.
	MaxImageBytes = 10 << 20
	// MaxCaptureUploadBytes bounds a whole capture upload: the manifest
	// and every image. 500 images of 10 MB each would be 5 GB; a CI run
	// splits a larger capture plan into several builds' worth of
	// uploads, or captures fewer, smaller pages.
	MaxCaptureUploadBytes = 200 << 20
	// MaxBoxCoordinate bounds a region's edges, in CSS pixels either
	// side of the page's origin: a box beyond it is clamped.
	MaxBoxCoordinate = 1 << 20
)

// The captures schema's bounds (captures.v1.schema.json).
const (
	maxURLLen            = 2048
	maxImageSide         = 65535
	maxSchemaViewport    = 16384
	maxLocaleLen         = 35
	maxAttributeLen      = 64
	maxDeviceScaleFactor = 4
)

// The captures schema's patterns, verbatim.
var (
	urlPattern       = regexp.MustCompile(`^https?://[^\s]+$`)
	localePattern    = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`)
	attributePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// CapturesDocument is a glossa.captures/v1 manifest as `glossa
// capture` writes it (RFC 0004 §3.2; the JSON Schema is
// runtimes/testdata/schemas/captures.v1.schema.json). Field names are
// the published contract. Pointers tell an absent member from a zero
// one; optional members are omitted when absent, so the canonical form
// (the digest) has only the members the schema defines.
type CapturesDocument struct {
	Schema      string            `json:"schema"`
	Application string            `json:"application"`
	Commit      string            `json:"commit"`
	Branch      string            `json:"branch"`
	Tool        DocumentTool      `json:"tool"`
	Captures    []DocumentCapture `json:"captures"`
}

// DocumentCapture is one (route, viewport, locale) of the manifest.
type DocumentCapture struct {
	Route    string           `json:"route"`
	URL      string           `json:"url"`
	Viewport DocumentViewport `json:"viewport"`
	Locale   string           `json:"locale"`
	Image    DocumentImage    `json:"image"`
	Renders  []DocumentRender `json:"renders"`
	Regions  []DocumentRegion `json:"regions"`
	// Findings is what the visual probe pass found on this capture
	// (RFC 0005 §5). Absent where the capture was taken without probes,
	// and omitted from the canonical form when it is, so a manifest
	// written before the probe pass keeps the digest it had.
	Findings []DocumentFinding `json:"findings,omitempty"`
}

// DocumentViewport is a capture's viewport in CSS pixels.
type DocumentViewport struct {
	Width             int      `json:"width"`
	Height            int      `json:"height"`
	DeviceScaleFactor *float64 `json:"deviceScaleFactor,omitempty"`
}

// DocumentImage names the uploaded PNG part by its SHA-256.
type DocumentImage struct {
	SHA256 string `json:"sha256"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// DocumentRender is one entry of a capture's render log.
type DocumentRender struct {
	Index  *int   `json:"index"`
	Key    string `json:"key"`
	Locale string `json:"locale"`
}

// DocumentRegion is where a rendered message is on the page: by key
// (a component's host element) or by index into the render log (a
// marked t() string).
type DocumentRegion struct {
	Key       *string     `json:"key,omitempty"`
	Index     *int        `json:"index,omitempty"`
	Kind      string      `json:"kind"`
	Attribute *string     `json:"attribute,omitempty"`
	Box       DocumentBox `json:"box"`
	Visible   *bool       `json:"visible"`
}

// DocumentBox is a region's box in CSS pixels, fractional as the
// browser measured it.
type DocumentBox struct {
	X      *float64 `json:"x"`
	Y      *float64 `json:"y"`
	Width  *float64 `json:"width"`
	Height *float64 `json:"height"`
}

// CaptureUpload is a validated captures manifest: the build's header
// (Upload without usages; Digest is the SHA-256 of the manifest's RFC
// 8785 canonical form) and its captures. Each capture's Image names the
// uploaded part: its digest is the part's, not yet the stored,
// re-encoded image's.
type CaptureUpload struct {
	Upload
	Captures []CaptureInput
}

// Parts returns the distinct images the captures reference, in order
// of first reference: one multipart part each.
func (u CaptureUpload) Parts() []Image {
	seen := map[Digest]bool{}
	var out []Image
	for _, c := range u.Captures {
		if !seen[c.Image.Digest] {
			seen[c.Image.Digest] = true
			out = append(out, c.Image)
		}
	}
	return out
}

// Keys returns the distinct keys of every capture's regions and visual
// findings: what one lookup resolves to message IDs at ingest.
func (u CaptureUpload) Keys() []string {
	var all []Region
	for _, c := range u.Captures {
		all = append(all, c.Regions...)
		for _, f := range c.Findings {
			if f.Key != "" {
				all = append(all, Region{Key: f.Key})
			}
		}
	}
	return RegionKeys(all)
}

// FindingCount is how many visual findings the upload carries.
func (u CaptureUpload) FindingCount() int {
	n := 0
	for _, c := range u.Captures {
		n += len(c.Findings)
	}
	return n
}

// ParseCaptures reads and validates a glossa.captures/v1 manifest by
// its schema's rules, and the server's: at most 500 captures, one per
// (route, viewport, locale), of at most 10 000 regions each, images of
// at most 40 megapixels, viewports of at most 10 000 CSS pixels a
// side, and every region index naming an entry of its render log.
// Unknown members are ignored, as in usages documents; everything else
// is ErrInvalidCaptures.
func ParseCaptures(raw []byte) (CaptureUpload, error) {
	if len(raw) > MaxManifestBytes {
		return CaptureUpload{}, ErrManifestTooLarge
	}
	var doc CapturesDocument
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return CaptureUpload{}, fmt.Errorf("%w: %v", ErrInvalidCaptures, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return CaptureUpload{}, fmt.Errorf("%w: data after the manifest", ErrInvalidCaptures)
	}
	up, err := doc.validate()
	if err != nil {
		return CaptureUpload{}, err
	}
	canonical, err := jcs.Marshal(doc)
	if err != nil {
		return CaptureUpload{}, fmt.Errorf("%w: %v", ErrInvalidCaptures, err)
	}
	up.Digest = DigestOf(canonical)
	return up, nil
}

// shot identifies a capture within a build.
type shot struct {
	route         string
	width, height int
	locale        bcp47.Tag
}

func (doc CapturesDocument) validate() (CaptureUpload, error) {
	header, err := parseHeader(doc.Schema, CapturesSchema, doc.Application, doc.Commit, doc.Branch, doc.Tool)
	if err != nil {
		return CaptureUpload{}, fmt.Errorf("%w: %w", ErrInvalidCaptures, err)
	}
	switch {
	case len(doc.Captures) == 0:
		return CaptureUpload{}, fmt.Errorf("%w: captures must be a list of at least one capture", ErrInvalidCaptures)
	case len(doc.Captures) > MaxCapturesPerBuild:
		return CaptureUpload{}, ErrTooManyCaptures
	}
	up := CaptureUpload{Upload: header, Captures: make([]CaptureInput, len(doc.Captures))}
	shots := map[shot]bool{}
	images := map[Digest]Image{}
	findings := 0
	for i, dc := range doc.Captures {
		c, err := dc.parse()
		if err != nil {
			return CaptureUpload{}, wrapAt(fmt.Sprintf("captures[%d]", i), err)
		}
		s := shot{c.Route, c.Viewport.Width, c.Viewport.Height, c.Locale}
		if shots[s] {
			return CaptureUpload{}, fmt.Errorf("%w: captures[%d]: %s at %d×%d in %s is captured twice", ErrInvalidCaptures, i,
				c.Route, c.Viewport.Width, c.Viewport.Height, c.Locale)
		}
		shots[s] = true
		if seen, ok := images[c.Image.Digest]; ok && seen != c.Image {
			return CaptureUpload{}, fmt.Errorf("%w: captures[%d]: image %s has two sizes", ErrInvalidCaptures, i, c.Image.Digest)
		}
		images[c.Image.Digest] = c.Image
		up.Captures[i] = c
		// One upload becomes one check run, and a run holds at most
		// 10 000 findings (RFC 0005 §10). A plan that finds more is
		// split, as one past 200 MB is; truncating would make the number
		// a dashboard shows untrue.
		if findings += len(c.Findings); findings > MaxFindingsPerUpload {
			return CaptureUpload{}, fmt.Errorf("%w: an upload carries at most %d visual findings; split the capture plan",
				ErrTooManyFindings, MaxFindingsPerUpload)
		}
	}
	return up, nil
}

// wrapAt places err at a path of the manifest; the limits' own errors
// stay as they are.
func wrapAt(path string, err error) error {
	if errors.Is(err, ErrTooManyRegions) || errors.Is(err, ErrTooManyFindings) {
		return err
	}
	return fmt.Errorf("%w: %s: %v", ErrInvalidCaptures, path, err)
}

func (dc DocumentCapture) parse() (CaptureInput, error) {
	if !textWithin(dc.Route, 1, MaxRouteLen) || !routePattern.MatchString(dc.Route) || !noSpace(dc.Route) {
		return CaptureInput{}, fmt.Errorf("route must be a route pattern (/checkout/[step]) of at most %d characters", MaxRouteLen)
	}
	if !validURL(dc.URL) {
		return CaptureInput{}, fmt.Errorf("url must be an http(s) URL of at most %d characters", maxURLLen)
	}
	if err := dc.Viewport.validate(); err != nil {
		return CaptureInput{}, err
	}
	locale, err := parseLocale(dc.Locale)
	if err != nil {
		return CaptureInput{}, err
	}
	img, err := dc.Image.parse()
	if err != nil {
		return CaptureInput{}, err
	}
	keys, err := renderLog(dc.Renders)
	if err != nil {
		return CaptureInput{}, err
	}
	if dc.Regions == nil {
		return CaptureInput{}, errors.New("regions must be a list")
	}
	if len(dc.Regions) > MaxRegionsPerCapture {
		return CaptureInput{}, ErrTooManyRegions
	}
	regions := make([]Region, len(dc.Regions))
	for i, dr := range dc.Regions {
		if regions[i], err = dr.parse(keys); err != nil {
			return CaptureInput{}, fmt.Errorf("regions[%d]: %v", i, err)
		}
	}
	findings, err := dc.findings(len(regions))
	if err != nil {
		return CaptureInput{}, err
	}
	in := CaptureInput{
		Route: dc.Route, Viewport: Viewport{Width: dc.Viewport.Width, Height: dc.Viewport.Height}, Locale: locale,
		Image: img, Regions: regions, Findings: findings,
	}
	// The domain's own rules (viewports of at most 10 000 pixels a side,
	// images of at most 40 megapixels) apply to every capture.
	return in, in.validate()
}

// findings validates a capture's visual findings against the regions it
// carries. A capture with no findings member has none: the probe pass
// is optional, and a capture taken without it says nothing about
// whether anything on it was clipped.
func (dc DocumentCapture) findings(regions int) ([]VisualFinding, error) {
	if len(dc.Findings) == 0 {
		return nil, nil
	}
	if len(dc.Findings) > MaxFindingsPerCapture {
		return nil, fmt.Errorf("%w: a capture carries at most %d visual findings", ErrTooManyFindings, MaxFindingsPerCapture)
	}
	out := make([]VisualFinding, len(dc.Findings))
	for i, df := range dc.Findings {
		f, err := df.parse(regions)
		if err != nil {
			return nil, fmt.Errorf("findings[%d]: %v", i, err)
		}
		out[i] = f
	}
	return out, nil
}

func validURL(s string) bool {
	if len(s) > maxURLLen || !urlPattern.MatchString(s) || !noSpace(s) {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.IsAbs() && u.Host != ""
}

func (v DocumentViewport) validate() error {
	if v.Width < 1 || v.Width > maxSchemaViewport || v.Height < 1 || v.Height > maxSchemaViewport {
		return fmt.Errorf("viewport sides must be 1–%d pixels", maxSchemaViewport)
	}
	if f := v.DeviceScaleFactor; f != nil && (*f <= 0 || *f > maxDeviceScaleFactor) {
		return fmt.Errorf("viewport.deviceScaleFactor must be above 0 and at most %d", maxDeviceScaleFactor)
	}
	return nil
}

func validLocale(s string) bool { return len(s) <= maxLocaleLen && localePattern.MatchString(s) }

func parseLocale(s string) (bcp47.Tag, error) {
	if !validLocale(s) {
		return bcp47.Tag{}, errors.New("locale must be a BCP 47 tag of at most 35 characters")
	}
	return bcp47.Parse(s)
}

func (i DocumentImage) parse() (Image, error) {
	d, err := ParseDigest(i.SHA256)
	if err != nil {
		return Image{}, errors.New("image.sha256 must be 64 lowercase hexadecimal digits")
	}
	if i.Width < 1 || i.Width > maxImageSide || i.Height < 1 || i.Height > maxImageSide {
		return Image{}, fmt.Errorf("image sides must be 1–%d pixels", maxImageSide)
	}
	return Image{Digest: d, Width: i.Width, Height: i.Height}, nil
}

// renderLog maps each render's index to its key.
func renderLog(renders []DocumentRender) (map[int]string, error) {
	if renders == nil {
		return nil, errors.New("renders must be a list")
	}
	keys := make(map[int]string, len(renders))
	for i, r := range renders {
		switch {
		case r.Index == nil || *r.Index < 0:
			return nil, fmt.Errorf("renders[%d]: index must be a non-negative integer", i)
		case !validKey(r.Key):
			return nil, fmt.Errorf("renders[%d]: key must be a message key of at most %d characters", i, MaxKeyLen)
		case !validLocale(r.Locale):
			return nil, fmt.Errorf("renders[%d]: locale must be a BCP 47 tag of at most 35 characters", i)
		}
		if _, dup := keys[*r.Index]; dup {
			return nil, fmt.Errorf("renders[%d]: index %d appears twice", i, *r.Index)
		}
		keys[*r.Index] = r.Key
	}
	return keys, nil
}

func validKey(k string) bool { return textWithin(k, 1, MaxKeyLen) && keyPattern.MatchString(k) }

func (r DocumentRegion) parse(renders map[int]string) (Region, error) {
	var key string
	switch {
	case (r.Key == nil) == (r.Index == nil):
		return Region{}, errors.New("a region has either a key or an index")
	case r.Key != nil && !validKey(*r.Key):
		return Region{}, fmt.Errorf("key must be a message key of at most %d characters", MaxKeyLen)
	case r.Key != nil:
		key = *r.Key
	case *r.Index < 0:
		return Region{}, errors.New("index must be a non-negative integer")
	default:
		k, ok := renders[*r.Index]
		if !ok {
			return Region{}, fmt.Errorf("index %d is not in the render log", *r.Index)
		}
		key = k
	}
	kind := RegionKind(r.Kind)
	switch {
	case kind != RegionElement && kind != RegionText && kind != RegionAttribute:
		return Region{}, errors.New("kind must be element, text or attribute")
	case kind == RegionAttribute && r.Attribute == nil:
		return Region{}, errors.New("an attribute region names its attribute")
	case kind != RegionAttribute && r.Attribute != nil:
		return Region{}, errors.New("only an attribute region names an attribute")
	case r.Attribute != nil && (len(*r.Attribute) > maxAttributeLen || !attributePattern.MatchString(*r.Attribute)):
		return Region{}, fmt.Errorf("attribute must be an attribute name of at most %d characters", maxAttributeLen)
	case r.Visible == nil:
		return Region{}, errors.New("visible is required")
	}
	box, err := r.Box.pixels()
	if err != nil {
		return Region{}, err
	}
	return Region{Key: key, Kind: kind, Box: box, Visible: *r.Visible}, nil
}

// pixels returns the whole-pixel box that covers b: its edges rounded
// outwards and clamped to ±MaxBoxCoordinate.
func (b DocumentBox) pixels() (Box, error) {
	if b.X == nil || b.Y == nil || b.Width == nil || b.Height == nil {
		return Box{}, errors.New("box needs x, y, width and height")
	}
	if *b.Width < 0 || *b.Height < 0 {
		return Box{}, errors.New("a box has no negative size")
	}
	x0, y0 := edge(math.Floor(*b.X)), edge(math.Floor(*b.Y))
	x1, y1 := edge(math.Ceil(*b.X+*b.Width)), edge(math.Ceil(*b.Y+*b.Height))
	return Box{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}, nil
}

func edge(v float64) int {
	return int(math.Max(-MaxBoxCoordinate, math.Min(MaxBoxCoordinate, v)))
}

// ImageKey is where a project's image is stored (RFC 0004 §3.3):
// content-addressed by the digest of its re-encoded PNG, so the same
// pixels across builds are stored once.
func ImageKey(tenant tenancy.ID, project uuid.UUID, d Digest) string {
	return "context/" + tenant.String() + "/" + project.String() + "/img/" + d.String() + ".png"
}
