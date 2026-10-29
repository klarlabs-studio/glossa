package capture_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"go.klarlabs.de/glossa/platform/internal/cli/capture"
	"go.klarlabs.de/glossa/platform/internal/cli/extract"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// schemaErrors validates raw against captures.v1 (which references
// usages.v1), from runtimes/testdata/schemas.
func schemaErrors(t *testing.T, raw []byte) error {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "runtimes", "testdata", "schemas")
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	for id, name := range map[string]string{
		"https://glossa.dev/schemas/usages/v1.json": "usages.v1.schema.json",
		// A capture's findings are glossa.finding/v1 findings, minus the
		// two members the page cannot know (RFC 0005 §5).
		"https://glossa.dev/schemas/finding/v1.json":  "finding.v1.schema.json",
		"https://glossa.dev/schemas/captures/v1.json": "captures.v1.schema.json",
	} {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource(id, doc); err != nil {
			t.Fatal(err)
		}
	}
	s, err := c.Compile("https://glossa.dev/schemas/captures/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return s.Validate(inst)
}

func index(n int) *int { return &n }

func sample() capture.Document {
	return capture.NewDocument(extract.Header{Application: "web", Commit: "9f2c1e7a4b3d5c6e8f0a1b2c3d4e5f6a7b8c9d0e", Branch: "feat/copy",
		Tool: extract.Tool{Name: "glossa", Version: "0.0.0-dev"}}, []capture.Capture{{
		Route: "/checkout/[step]", URL: "http://localhost:4173/checkout/payment?lang=de",
		Viewport: capture.Viewport{Width: 390, Height: 844, DeviceScaleFactor: 2}, Locale: "de",
		Image:   capture.Image{SHA256: "3b1f0a4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708", Width: 780, Height: 3120},
		Renders: []capture.Render{{Index: 0, Key: "checkout.pay", Locale: "de"}, {Index: 1, Key: "checkout.card", Locale: "und"}},
		Regions: []capture.Region{
			{Key: "cart.checkout", Kind: "element", Box: capture.Box{X: 16, Y: 720.5, Width: 358, Height: 48}, Visible: true},
			{Index: index(0), Kind: "text", Box: capture.Box{X: 24, Y: 1180, Width: 142.25, Height: 20}, Visible: true},
			{Index: index(1), Kind: "attribute", Attribute: "placeholder", Box: capture.Box{}, Visible: false},
		},
		Findings: capture.Findings(probes()),
	}})
}

// probes is what the page's probe pass measured, in the shape it writes
// it: a glossa.finding/v1 finding with no fingerprint and no capture.
func probes() []domain.Finding {
	return []domain.Finding{
		{
			Schema: domain.Schema, Layer: domain.LayerVisual, Code: "text-clipped", Severity: domain.Warning,
			Locus:   domain.Locus{Key: "checkout.pay", Locale: "de", Region: "r_0"},
			Message: "Clipped: 210×20 px of text in 148×20 px.",
			// Evidence is free-form per code; the schema bounds its size
			// and says nothing about its keys.
			Evidence: map[string]any{"box": []any{148, 20}, "content": []any{210, 20}},
		},
		{
			Schema: domain.Schema, Layer: domain.LayerVisual, Code: "region-overlap", Severity: domain.Warning,
			Locus:   domain.Locus{Key: "cart.checkout", Region: "r_1"},
			Message: "Overlaps checkout.pay by 62 %.", Subject: "checkout.pay",
		},
	}
}

func TestDocumentMatchesTheSchema(t *testing.T) {
	raw, err := json.Marshal(sample())
	if err != nil {
		t.Fatal(err)
	}
	if err := schemaErrors(t, raw); err != nil {
		t.Fatalf("schema: %v\n%s", err, raw)
	}
	// Index 0 is written; a viewport at scale 1 has no deviceScaleFactor.
	d := sample()
	d.Captures[0].Viewport.DeviceScaleFactor = 0
	raw, _ = json.Marshal(d)
	if !bytes.Contains(raw, []byte(`"index":0`)) || bytes.Contains(raw, []byte("deviceScaleFactor")) {
		t.Errorf("json = %s", raw)
	}
}

// The probe findings the manifest carries are the schema's findings,
// without the two members the page cannot know: the ingest mints the
// capture, and the fingerprint is hashed over the catalog message ID a
// browser never has (RFC 0005 §5). A manifest asserting either would be
// a client deciding an identity the waiver list computes differently.
func TestFindingsAreCarriedWithoutTheServersTwoMembers(t *testing.T) {
	d := sample()
	fs := d.Captures[0].Findings
	if fs == nil || len(*fs) != len(probes()) {
		t.Fatalf("findings = %v, want the probe pass's", fs)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := schemaErrors(t, raw); err != nil {
		t.Fatalf("schema: %v\n%s", err, raw)
	}
	for _, member := range []string{`"fingerprint"`, `"capture"`, `"source_revision"`, `"waiver"`} {
		if bytes.Contains(raw, []byte(member)) {
			t.Errorf("the manifest asserts %s:\n%s", member, raw)
		}
	}

	// Absent and empty are different answers: a capture nobody probed
	// against one the probes found nothing on.
	if capture.Findings(nil) != nil {
		t.Error("a capture taken without probes claims an empty finding list")
	}
	empty := sample()
	empty.Captures[0].Findings = capture.Findings([]domain.Finding{})
	raw, _ = json.Marshal(empty)
	if !bytes.Contains(raw, []byte(`"findings":[]`)) {
		t.Errorf("a probe pass that found nothing said nothing:\n%s", raw)
	}
	none := sample()
	none.Captures[0].Findings = nil
	raw, _ = json.Marshal(none)
	if bytes.Contains(raw, []byte(`"findings"`)) {
		t.Errorf("a capture taken without probes carries a findings member:\n%s", raw)
	}
}

func TestCoverListsUsedMessagesWithoutAVisibleRegion(t *testing.T) {
	usages := []capture.Usage{
		{Key: "checkout.pay", File: "src/Pay.vue", Line: 3, Route: "/checkout/[step]"},
		{Key: "checkout.card", File: "src/Pay.vue", Line: 9, Route: "/checkout/[step]"},
		{Key: "cart.checkout", File: "src/Cart.vue", Line: 1},
		{Key: "home.title", File: "src/Home.vue", Line: 2, Route: "/"},
		{Key: "home.title", File: "src/Hero.vue", Line: 7, Route: "/landing"},
		{Key: "home.title", File: "src/Home.vue", Line: 9, Route: "/"},
	}
	got := capture.Cover(usages, sample())
	want := capture.Coverage{Messages: 4, Captured: 2, NotCaptured: []capture.NotCaptured{
		{Key: "checkout.card", Usages: 1, File: "src/Pay.vue", Line: 9, Routes: []string{"/checkout/[step]"}}, // only an invisible region
		{Key: "home.title", Usages: 3, File: "src/Home.vue", Line: 2, Routes: []string{"/", "/landing"}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Cover =\n%+v\nwant\n%+v", got, want)
	}
	if empty := capture.Cover(nil, sample()); empty.Messages != 0 || empty.NotCaptured == nil {
		t.Errorf("no usages = %+v", empty)
	}
}
