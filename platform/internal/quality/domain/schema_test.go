package domain_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The finding is a contract, not a Go type that happens to serialize:
// `glossa.finding/v1` is what the CLI, the PR check, Studio, the
// runtimes' fixtures and MCP all read. These tests keep the Go type and
// the published schema from drifting apart in either direction — the
// schema accepts what domain.Finding writes, and domain.Finding reads
// back everything the published example says.

// schemas is runtimes/testdata/schemas, four levels up from here.
const schemas = "../../../../runtimes/testdata/schemas"

func findingSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	for _, name := range []string{"usages.v1.schema.json", "finding.v1.schema.json"} {
		path := filepath.Join(schemas, name)
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		defer f.Close() //nolint:errcheck // a test read
		doc, err := jsonschema.UnmarshalJSON(f)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		id, _ := doc.(map[string]any)["$id"].(string)
		if err := c.AddResource(id, doc); err != nil {
			t.Fatalf("add %s: %v", path, err)
		}
	}
	s, err := c.Compile("https://glossa.dev/schemas/finding/v1.json")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return s
}

func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// Every finding a layer can build validates: the minimal one, one with
// every field set, and a waived one.
func TestFindingsValidateAgainstTheSchema(t *testing.T) {
	s := findingSchema(t)
	rev, to := 9, 28
	for name, f := range map[string]domain.Finding{
		"minimal": domain.New(domain.Finding{
			Layer: domain.LayerCompleteness, Code: "missing-translation", Severity: domain.Error,
			Locus: domain.Locus{Key: "checkout.pay", Locale: "fr"}, Message: "missing translation",
		}),
		"complete": domain.New(domain.Finding{
			Layer: domain.LayerLength, Code: "expansion-excessive", Severity: domain.Warning,
			Locus: domain.Locus{
				Message: "0192f5a1-2b3c-7d4e-8f90-1a2b3c4d5e6f", Key: "checkout.pay", Locale: "fr",
				Revision: "0192f5b0-3c4d-7e5f-8091-2b3c4d5e6f70", Namespace: "checkout",
				File: "src/checkout/PaymentFooter.vue", Line: 42, Column: 14,
				Route: "/checkout/payment", Component: "PaymentFooter",
				Capture: "0192f5c2-4d5e-7f60-8192-3c4d5e6f7081", Region: "r_18",
				Span: &domain.Span{Side: domain.SideTarget, Start: 12, End: 19},
			},
			Message: "French is 74 % longer than the German source.", Subject: "Payer maintenant tout de suite",
			Detail: "ratio", Evidence: map[string]any{"ratio": 1.74, "region_width_px": 148},
			Fix: &domain.Fix{Kind: domain.FixShorten, To: &to, Hint: "Payer maintenant"}, SourceRevision: &rev,
		}),
		"waived": domain.New(domain.Finding{
			Layer: domain.LayerTerminology, Code: "term_forbidden", Severity: domain.Error,
			Locus:   domain.Locus{Key: "nav.login", Locale: "de", Span: &domain.Span{Side: domain.SideTarget, Start: 0, End: 5}},
			Message: "the translation uses a forbidden term", Subject: "Anmeldung",
		}).Waive("0192f5d3-5e6f-7081-82a3-4d5e6f708192"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := s.Validate(asJSON(t, f)); err != nil {
				t.Errorf("%#v does not validate:\n%v", f, err)
			}
		})
	}
}

// The published example is what everyone else reads the contract from,
// so domain.Finding must read it whole and write it back unchanged.
func TestPublishedExampleRoundTrips(t *testing.T) {
	path := filepath.Join(schemas, "examples", "finding.v1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var want any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parse the example: %v", err)
	}
	var f domain.Finding
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("the example does not read into domain.Finding: %v", err)
	}
	if got := asJSON(t, f); !reflect.DeepEqual(got, want) {
		t.Errorf("the example does not round-trip through domain.Finding:\n got %v\nwant %v", got, want)
	}
	if err := findingSchema(t).Validate(want); err != nil {
		t.Errorf("the published example does not validate against its own schema:\n%v", err)
	}
}
