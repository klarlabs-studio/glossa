package domain_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"go.klarlabs.de/glossa/platform/internal/context/domain"
)

// The server validates an upload by the glossa.usages/v1 schema's rules
// (runtimes/testdata/schemas/usages.v1.schema.json). These tests hold
// ParseUpload to the schema itself: over the example, every fixture's
// expected document and a list of variants, ParseUpload accepts exactly
// what the schema accepts — apart from the deviations listed in
// deviations, each with its reason.

// testdata is runtimes/testdata, found from this file's location.
func testdata(t *testing.T, parts ...string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(append([]string{filepath.Dir(file), "..", "..", "..", "..", "runtimes", "testdata"}, parts...)...)
}

func usagesSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	f, err := os.Open(testdata(t, "schemas", "usages.v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource("usages.v1.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("usages.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func schemaAccepts(t *testing.T, s *jsonschema.Schema, doc []byte) bool {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return false
	}
	return s.Validate(inst) == nil
}

// variant replaces the value at path (object members and array indexes)
// in a copy of doc; a nil value deletes it.
type variant struct {
	why   string
	path  []any
	value any
}

var u0 = []any{"usages", 0}

func at(path []any, more ...any) []any { return append(append([]any{}, path...), more...) }

var deleted = struct{}{}

// variants are the Python checker's (runtimes/testdata/gen/check_usages.py)
// plus the edges of every length and pattern.
var variants = []variant{
	{"SHA-256 commit", []any{"commit"}, strings.Repeat("a", 64)},
	{"dotfile directory and '...' segment", at(u0, "file"), ".storybook/.../preview.ts"},
	{"scoped tool name and prerelease version", []any{"tool"}, map[string]any{"name": "@klarlabs-studio/glossa", "version": "0.1.0-rc.1+build.5"}},
	{"nested branch name", []any{"branch"}, "renovate/vite-6.x"},
	{"short commit", []any{"commit"}, "9f2c1e7"},
	{"41-digit commit", []any{"commit"}, strings.Repeat("a", 41)},
	{"uppercase commit", []any{"commit"}, strings.Repeat("A", 40)},
	{"branch with '..'", []any{"branch"}, "feat/../main"},
	{"branch component starting with '.'", []any{"branch"}, "feat/.hidden"},
	{"branch with a space", []any{"branch"}, "my branch"},
	{"branch ending in '.'", []any{"branch"}, "release."},
	{"branch with a brace", []any{"branch"}, "feat/{x}"},
	{"branch with a tilde", []any{"branch"}, "a~1"},
	{"empty branch", []any{"branch"}, ""},
	{"256-byte branch", []any{"branch"}, strings.Repeat("b", 256)},
	{"application that isn't a slug", []any{"application"}, "Web App"},
	{"application with a trailing hyphen", []any{"application"}, "web-"},
	{"unknown schema version", []any{"schema"}, "glossa.usages/v2"},
	{"missing tool version", []any{"tool"}, map[string]any{"name": "glossa"}},
	{"tool name with a space", []any{"tool", "name"}, "glossa extract"},
	{"tool version that isn't semver", []any{"tool", "version"}, "v1"},
	{"missing usages", []any{"usages"}, deleted},
	{"absolute path", at(u0, "file"), "/src/App.vue"},
	{"parent segment", at(u0, "file"), "src/../App.vue"},
	{"leading parent segment", at(u0, "file"), "../App.vue"},
	{"current-directory segment", at(u0, "file"), "./src/App.vue"},
	{"backslash separators", at(u0, "file"), "src\\App.vue"},
	{"drive letter", at(u0, "file"), "C:/src/App.vue"},
	{"empty segment", at(u0, "file"), "src//App.vue"},
	{"trailing slash", at(u0, "file"), "src/"},
	{"non-ASCII file", at(u0, "file"), "src/Größe.vue"},
	{"line 0", at(u0, "line"), 0},
	{"column 0", at(u0, "column"), 0},
	{"missing column", at(u0, "column"), deleted},
	{"fractional line", at(u0, "line"), 1.5},
	{"kind outside the enum", at(u0, "kind"), "go"},
	{"typed is now accessor", at(u0, "kind"), "typed"},
	{"key that isn't a message key", at(u0, "key"), "Checkout Pay"},
	{"key of 200 characters", at(u0, "key"), strings.Repeat("a", 200)},
	{"key over 200 characters", at(u0, "key"), strings.Repeat("a", 201)},
	{"route that is a URL", at(u0, "route"), "https://shop.example.com/checkout"},
	{"route with a query", at(u0, "route"), "/checkout?step=2"},
	{"empty route", at(u0, "route"), ""},
	{"route of 1024 characters", at(u0, "route"), "/" + strings.Repeat("r", 1023)},
	{"route over 1024 characters", at(u0, "route"), "/" + strings.Repeat("r", 1024)},
	{"empty component", at(u0, "component"), ""},
	{"component with spaces", at(u0, "component"), "PaymentFooter > PrimaryButton"},
	{"Go method component", at(u0, "component"), "mail.(*Mailer).Send"},
	{"component of 512 characters", at(u0, "component"), strings.Repeat("C", 512)},
	{"component over 512 characters", at(u0, "component"), strings.Repeat("C", 513)},
	{"no component", at(u0, "component"), deleted},
	{"no route", at(u0, "route"), deleted},
	{"branch '@'", []any{"branch"}, "@"},
	{"branch component ending in .lock", []any{"branch"}, "feat/x.lock/y"},
	{"branch starting with '-'", []any{"branch"}, "-x"},
	{"component with a no-break space", at(u0, "component"), "Payment Footer"},
	{"extra top-level field", []any{"digest"}, "abc"},
	{"extra usage field", at(u0, "messageId"), "msg_1"},
}

// deviations are the variants where ParseUpload deliberately differs
// from the schema, and why.
var deviations = map[string]string{
	// Unknown fields are ignored within v1 so a newer collector's
	// optional field doesn't break an older server; the schema is closed
	// so collectors can't drift.
	"extra top-level field": "ignored",
	"extra usage field":     "ignored",
	// Git refuses these branch names (git-check-ref-format); no
	// repository has one.
	"branch '@'":                       "refused",
	"branch component ending in .lock": "refused",
	"branch starting with '-'":         "refused",
	// The schema's \S is ECMA-262's, which excludes Unicode white space;
	// Go's regexp (the validator in this test) is ASCII only.
	"component with a no-break space": "refused",
}

func mutate(t *testing.T, doc []byte, v variant) []byte {
	t.Helper()
	var root any
	if err := json.Unmarshal(doc, &root); err != nil {
		t.Fatal(err)
	}
	parent := root
	for _, step := range v.path[:len(v.path)-1] {
		switch s := step.(type) {
		case string:
			parent = parent.(map[string]any)[s]
		case int:
			parent = parent.([]any)[s]
		}
	}
	last := v.path[len(v.path)-1]
	switch p := parent.(type) {
	case map[string]any:
		if v.value == deleted {
			delete(p, last.(string))
		} else {
			p[last.(string)] = v.value
		}
	case []any:
		p[last.(int)] = v.value
	}
	out, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParseUploadAgreesWithTheSchemaOnVariants(t *testing.T) {
	s := usagesSchema(t)
	example, err := os.ReadFile(testdata(t, "schemas", "examples", "usages.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !schemaAccepts(t, s, example) {
		t.Fatal("the schema rejects its own example")
	}
	if _, err := domain.ParseUpload(example); err != nil {
		t.Fatalf("ParseUpload rejects the schema's example: %v", err)
	}
	for _, v := range variants {
		t.Run(v.why, func(t *testing.T) {
			doc := mutate(t, example, v)
			want := schemaAccepts(t, s, doc)
			switch deviations[v.why] {
			case "ignored":
				want = true
			case "refused":
				want = false
			}
			_, err := domain.ParseUpload(doc)
			if got := err == nil; got != want {
				t.Errorf("ParseUpload accepted = %t (%v), schema accepted = %t\n%s", got, err, want, doc)
			}
		})
	}
}

func TestParseUploadAcceptsEveryFixtureDocument(t *testing.T) {
	s := usagesSchema(t)
	cases, err := filepath.Glob(testdata(t, "usages", "*", "expected.json"))
	if err != nil || len(cases) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, path := range cases {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			doc, err := os.ReadFile(path) //nolint:gosec // fixtures in the repository
			if err != nil {
				t.Fatal(err)
			}
			if !schemaAccepts(t, s, doc) {
				t.Fatal("the schema rejects the fixture")
			}
			if _, err := domain.ParseUpload(doc); err != nil {
				t.Errorf("ParseUpload: %v", err)
			}
		})
	}
}

// capturesSchema compiles captures.v1.schema.json with the schemas it
// refers to by $id: usages, and finding — a capture's findings are
// glossa.finding/v1 findings, minus the two members the page cannot
// know (RFC 0005 §5).
func capturesSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	for _, name := range []string{"usages", "finding", "captures"} {
		f, err := os.Open(testdata(t, "schemas", name+".v1.schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource("https://glossa.dev/schemas/"+name+"/v1.json", doc); err != nil {
			t.Fatal(err)
		}
	}
	s, err := c.Compile("https://glossa.dev/schemas/captures/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var (
	c0  = []any{"captures", 0}
	r0  = at(c0, "regions", 0)
	r1  = at(c0, "regions", 1)
	r2  = at(c0, "regions", 2)
	rl0 = at(c0, "renders", 0)
	f0  = at(c0, "findings", 0)
	f2  = at(c0, "findings", 2)
)

// captureVariants are the edges of the captures schema's rules.
var captureVariants = []variant{
	{"no captures", []any{"captures"}, []any{}},
	{"missing captures", []any{"captures"}, deleted},
	{"unknown schema version", []any{"schema"}, "glossa.captures/v2"},
	{"short commit", []any{"commit"}, "9f2c1e7"},
	{"application that isn't a slug", []any{"application"}, "Web App"},
	{"branch with '..'", []any{"branch"}, "feat/../main"},
	{"tool version that isn't semver", []any{"tool", "version"}, "v1"},
	{"route that is a URL", at(c0, "route"), "https://shop.example.com/checkout"},
	{"missing url", at(c0, "url"), deleted},
	{"ftp url", at(c0, "url"), "ftp://example.com/x"},
	{"url with a space", at(c0, "url"), "http://localhost/a b"},
	{"url of 2049 characters", at(c0, "url"), "http://h/" + strings.Repeat("u", 2040)},
	{"viewport width 0", at(c0, "viewport", "width"), 0},
	{"fractional viewport", at(c0, "viewport", "height"), 800.5},
	{"viewport 16385 wide", at(c0, "viewport", "width"), 16385},
	{"viewport 16384 wide", at(c0, "viewport", "width"), 16384},
	{"device scale factor 0", at(c0, "viewport", "deviceScaleFactor"), 0},
	{"device scale factor 4", at(c0, "viewport", "deviceScaleFactor"), 4},
	{"device scale factor 4.5", at(c0, "viewport", "deviceScaleFactor"), 4.5},
	{"no device scale factor", at(c0, "viewport", "deviceScaleFactor"), deleted},
	{"extra viewport field", at(c0, "viewport", "isMobile"), true},
	{"locale with an underscore", at(c0, "locale"), "de_DE"},
	{"regional locale", at(c0, "locale"), "de-CH"},
	{"one-letter locale", at(c0, "locale"), "d"},
	{"uppercase image digest", at(c0, "image", "sha256"), strings.Repeat("A", 64)},
	{"short image digest", at(c0, "image", "sha256"), "abc"},
	{"image width 0", at(c0, "image", "width"), 0},
	{"image width 65536", at(c0, "image", "width"), 65536},
	{"missing renders", at(c0, "renders"), deleted},
	{"missing regions", at(c0, "regions"), deleted},
	{"render with a negative index", at(rl0, "index"), -1},
	{"render without a locale", at(rl0, "locale"), deleted},
	{"render key that isn't a message key", at(rl0, "key"), "Checkout Pay"},
	{"region with neither key nor index", r0, map[string]any{
		"kind": "element", "box": map[string]any{"x": 0, "y": 0, "width": 1, "height": 1}, "visible": true,
	}},
	{"region with both key and index", at(r1, "key"), "checkout.pay"},
	{"region kind outside the enum", at(r0, "kind"), "image"},
	{"attribute region without its attribute", at(r2, "attribute"), deleted},
	{"element region with an attribute", at(r0, "attribute"), "title"},
	{"attribute name with uppercase", at(r2, "attribute"), "ariaLabel"},
	{"negative box width", at(r0, "box", "width"), -1},
	{"negative box position", at(r0, "box", "x"), -40.5},
	{"box without a height", at(r0, "box", "height"), deleted},
	{"missing visible", at(r0, "visible"), deleted},
	{"string visible", at(r0, "visible"), "yes"},
	{"extra region field", at(r0, "selector"), "#pay"},
	{"extra top-level field", []any{"generated_at"}, "2026-09-19"},
	{"image over 40 megapixels", at(c0, "image"), map[string]any{"sha256": strings.Repeat("a", 64), "width": 8000, "height": 5001}},
	{"viewport 10001 wide", at(c0, "viewport", "width"), 10001},
	{"region index outside the render log", at(r1, "index"), 7},
	// The visual probe pass's findings (RFC 0005 §5).
	{"no findings", at(c0, "findings"), deleted},
	{"empty findings", at(c0, "findings"), []any{}},
	{"finding with a fingerprint", at(f0, "fingerprint"), "f_0123456789abcdef"},
	{"finding with a capture in its locus", at(f0, "locus", "capture"), "0192f5c2-0000-7000-8000-00000000c0de"},
	{"finding of another layer", at(f0, "layer"), "length"},
	{"finding at error severity", at(f0, "severity"), "error"},
	{"finding code with a capital", at(f0, "code"), "textClipped"},
	{"finding without a message", at(f0, "message"), deleted},
	{"empty finding message", at(f0, "message"), ""},
	{"finding locus with no members", at(f0, "locus"), map[string]any{}},
	{"finding key that isn't a message key", at(f0, "locus", "key"), "Cart Checkout"},
	{"finding locale with an underscore", at(f2, "locus", "locale"), "de_DE"},
	{"region name that isn't an index", at(f0, "locus", "region"), "cart.checkout"},
	{"region name with a leading zero", at(f0, "locus", "region"), "r_01"},
	{"region outside this capture's regions", at(f0, "locus", "region"), "r_4"},
	{"finding subject", at(f0, "subject"), "cart.total"},
	{"finding evidence that isn't an object", at(f0, "evidence"), []any{1, 2}},
	{"finding evidence of 5 kB", at(f0, "evidence"), map[string]any{"text": strings.Repeat("x", 5000)}},
	{"extra finding field", at(f0, "promoted"), true},
}

// captureDeviations are the variants where ParseCaptures deliberately
// differs from the schema, and why.
var captureDeviations = map[string]string{
	// Undefined members are ignored within v1, as in usages documents.
	"extra viewport field":  "ignored",
	"extra region field":    "ignored",
	"extra top-level field": "ignored",
	// The server's limits (RFC 0004 §3.3, §10): images of at most 40
	// megapixels and viewports of at most 10 000 CSS pixels a side.
	"image over 40 megapixels": "refused",
	"viewport 10001 wide":      "refused",
	"viewport 16384 wide":      "refused",
	// "Every region index refers to one entry" of the render log: the
	// schema says so in prose; the server holds uploads to it.
	"region index outside the render log": "refused",
	// Undefined members are ignored within v1 here too. The schema
	// refuses a `fingerprint` and a `locus.capture` on a capture's
	// finding — they are the two the ingest completes, because only the
	// server knows the catalog message ID one hashes and the capture the
	// other names — and the parser ignores them like any other member it
	// doesn't define. Either way the stored finding carries the
	// server's, so a page can never mint a fingerprint that a waiver
	// would then fail to match.
	"finding with a fingerprint":          "ignored",
	"finding with a capture in its locus": "ignored",
	"extra finding field":                 "ignored",
	// A finding whose region is not a region of this capture could never
	// be drawn on it, and the schema's dependentRequired makes the
	// ingest pair the two (RFC 0005 §2.1).
	"region outside this capture's regions": "refused",
	// The evidence object is free-form per code; its size is not.
	"finding evidence of 5 kB": "refused",
}

func TestParseCapturesAgreesWithTheSchemaOnVariants(t *testing.T) {
	s := capturesSchema(t)
	example, err := os.ReadFile(testdata(t, "schemas", "examples", "captures.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !schemaAccepts(t, s, example) {
		t.Fatal("the schema rejects its own example")
	}
	for _, v := range captureVariants {
		t.Run(v.why, func(t *testing.T) {
			doc := mutate(t, example, v)
			want := schemaAccepts(t, s, doc)
			switch captureDeviations[v.why] {
			case "ignored":
				want = true
			case "refused":
				want = false
			}
			_, err := domain.ParseCaptures(doc)
			if got := err == nil; got != want {
				t.Errorf("ParseCaptures accepted = %t (%v), schema accepted = %t\n%s", got, err, want, doc)
			}
		})
	}
}
