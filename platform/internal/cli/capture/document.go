package capture

import (
	"go.klarlabs.de/glossa/platform/internal/cli/extract"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Schema is the document type Document carries.
const Schema = "glossa.captures/v1"

// Document is a glossa.captures/v1 document
// (runtimes/testdata/schemas/captures.v1.schema.json): one build's
// captures, each a page image and the regions where messages were
// rendered on it.
type Document struct {
	Schema      string       `json:"schema"`
	Application string       `json:"application"`
	Commit      string       `json:"commit"`
	Branch      string       `json:"branch"`
	Tool        extract.Tool `json:"tool"`
	Captures    []Capture    `json:"captures"`
}

// Capture is one (route, viewport, locale).
type Capture struct {
	Route    string   `json:"route"`
	URL      string   `json:"url"`
	Viewport Viewport `json:"viewport"`
	Locale   string   `json:"locale"`
	Image    Image    `json:"image"`
	Renders  []Render `json:"renders"`
	Regions  []Region `json:"regions"`
	// Findings are what the visual probe pass measured on this capture
	// (RFC 0005 §5), carried to the server so the check that grades the
	// pull request sees what the page saw. Without them a visual finding
	// exists only in the terminal that captured it.
	//
	// It is a pointer because the schema distinguishes three answers and
	// a slice distinguishes two: absent is a capture taken without
	// probes, and an empty list is the probes having run and found
	// nothing. Saying "nothing" where the truth is "nobody looked" is
	// the one thing a quality signal may never do.
	Findings *[]Finding `json:"findings,omitempty"`
}

// Finding is one probe finding as the manifest carries it: a
// glossa.finding/v1 finding minus the two members the page cannot know
// (runtimes/testdata/schemas/captures.v1.schema.json).
//
// The fingerprint is absent because it hashes the catalog message ID a
// browser never has, and locus.capture because the capture is minted at
// the ingest. Both are completed there. A manifest that carried a
// fingerprint of its own would be a client asserting an identity the
// waiver list computes differently, which is how a waiver quietly stops
// matching.
type Finding struct {
	Schema   string         `json:"schema"`
	Layer    string         `json:"layer"`
	Code     string         `json:"code"`
	Severity string         `json:"severity"`
	Locus    FindingLocus   `json:"locus"`
	Message  string         `json:"message"`
	Subject  string         `json:"subject,omitempty"`
	Evidence map[string]any `json:"evidence,omitempty"`
}

// FindingLocus is as much of a finding's locus as the page knows.
type FindingLocus struct {
	Key    string `json:"key,omitempty"`
	Locale string `json:"locale,omitempty"`
	// Region is `r_<index into this capture's regions>`; the ingest
	// pairs it with the capture it is on.
	Region string `json:"region,omitempty"`
}

// Findings is the probe pass's findings in the shape the manifest
// carries them, or nil where no probe pass ran.
//
// Nothing is completed on the way: an empty route or locale stays
// empty, because the server fills both from the capture the finding is
// on (quality/layers.seal) and a value invented here would be a second
// answer to a question that already has one.
func Findings(probes []domain.Finding) *[]Finding {
	if probes == nil {
		return nil
	}
	out := make([]Finding, 0, len(probes))
	for _, p := range probes {
		out = append(out, Finding{
			Schema: p.Schema, Layer: string(p.Layer), Code: p.Code, Severity: string(p.Severity),
			Locus:   FindingLocus{Key: p.Locus.Key, Locale: p.Locus.Locale, Region: p.Locus.Region},
			Message: p.Message, Subject: p.Subject, Evidence: p.Evidence,
		})
	}
	return &out
}

// Viewport is the CSS viewport; the image is its pixels times
// DeviceScaleFactor (omitted when 1).
type Viewport struct {
	Width             int     `json:"width"`
	Height            int     `json:"height"`
	DeviceScaleFactor float64 `json:"deviceScaleFactor,omitempty"`
}

// Image is the full-page PNG: its lowercase hex SHA-256 names its
// multipart part and its file.
type Image struct {
	SHA256 string `json:"sha256"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Render is an entry of the page's render log: what a t() marker's index
// rendered.
type Render struct {
	Index  int    `json:"index"`
	Key    string `json:"key"`
	Locale string `json:"locale"`
}

// Region is where a rendered message is on the page: a component host
// (Key) or a marked t() string (Index into the renders).
type Region struct {
	Key   string `json:"key,omitempty"`
	Index *int   `json:"index,omitempty"`
	// Kind is element, text or attribute; Attribute names the attribute.
	Kind      string `json:"kind"`
	Attribute string `json:"attribute,omitempty"`
	Box       Box    `json:"box"`
	// Visible is false when the message rendered zero-size, off-screen,
	// hidden, or under a redaction.
	Visible bool `json:"visible"`
}

// Box is in CSS pixels from the top-left of the full page.
type Box struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// NewDocument returns the document for h and captures, in the plan's
// order.
func NewDocument(h extract.Header, captures []Capture) Document {
	if captures == nil {
		captures = []Capture{}
	}
	return Document{Schema: Schema, Application: h.Application, Commit: h.Commit, Branch: h.Branch, Tool: h.Tool, Captures: captures}
}

// VisibleKeys are the keys with at least one visible region in doc.
func (d Document) VisibleKeys() map[string]bool {
	seen := map[string]bool{}
	for _, c := range d.Captures {
		keys := make(map[int]string, len(c.Renders))
		for _, r := range c.Renders {
			keys[r.Index] = r.Key
		}
		for _, r := range c.Regions {
			switch {
			case !r.Visible:
			case r.Key != "":
				seen[r.Key] = true
			case r.Index != nil && keys[*r.Index] != "":
				seen[keys[*r.Index]] = true
			}
		}
	}
	return seen
}
