package cli

import (
	"encoding/json"
	"reflect"
	"testing"

	contextdomain "go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// What `glossa capture --upload` carries to the server (RFC 0005 §5.1).
//
// Both ends of the visual layer were built and tested apart: the page
// measures findings, and the Captures API accepts, resolves and records
// them. Between them stood a manifest with no `findings` member, so the
// server stored none — which left the pull-request check nothing to
// grade and `visual: enforce` nothing to gate. Every test of either end
// passed throughout.
//
// So these tests are of the seam and not of either side: the manifest
// the CLI actually uploaded, read back with the server's own parser.

func TestCaptureUploadCarriesTheProbeFindings(t *testing.T) {
	srv, w := capturing(t)
	fakeProbedRun(t, []domain.Finding{probeFinding("text-clipped", "home.title")}, nil)

	var doc captureDoc
	w.json(&doc, "capture", "--upload", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
	if doc.Upload == nil || doc.Upload.Captures != 2 {
		t.Fatalf("--json = %+v", doc)
	}
	raw := uploadedManifest(t, srv)

	up, err := contextdomain.ParseCaptures(raw)
	if err != nil {
		t.Fatalf("the server can't read the manifest the CLI wrote: %v\n%s", err, raw)
	}
	if up.FindingCount() != 2 {
		t.Fatalf("the upload carries %d visual findings, want one per capture:\n%s", up.FindingCount(), raw)
	}
	f := up.Captures[0].Findings[0]
	if f.Code != "text-clipped" || f.Key != "home.title" || f.Region != "r_0" {
		t.Errorf("finding = %+v", f)
	}

	// And the command says so: what each capture measured, and how many
	// the server stored. The second is the server's number, because it
	// is the one that says the visual layer reached the pull request
	// rather than stopping in this terminal.
	if doc.Upload.Findings != 2 {
		t.Errorf("upload = %+v, want the server's findings count", doc.Upload)
	}
	for _, c := range doc.Captures {
		if c.Probes != 1 {
			t.Errorf("capture %s/%s reports %d probes, want 1", c.Route, c.Locale, c.Probes)
		}
	}
	if f.Message == "" {
		t.Error("the finding reached the server without its explanation")
	}

	// The two members only the server can know are left to it: a
	// fingerprint minted over a key here would not be the one the waiver
	// list computes over the catalog message ID, and the capture does not
	// exist until the ingest mints it.
	wire := uploadedFindings(t, raw)[0]
	if _, ok := wire["fingerprint"]; ok {
		t.Errorf("the manifest asserts a fingerprint: %v", wire)
	}
	if locus, _ := wire["locus"].(map[string]any); locus["capture"] != nil {
		t.Errorf("the manifest asserts a capture: %v", locus)
	}
}

// "The probes found nothing" and "nobody probed" are different answers,
// and the manifest keeps them apart: an empty list against no member at
// all. Reporting silence as a clean bill is the one thing a quality
// signal may not do.
func TestCaptureUploadDistinguishesNoFindingsFromNoProbePass(t *testing.T) {
	for _, tc := range []struct {
		name   string
		probes []domain.Finding
		want   any
	}{
		{"the probes ran and found nothing", []domain.Finding{}, []any{}},
		{"no probe pass ran", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, w := capturing(t)
			fakeProbedRun(t, tc.probes, nil)
			var doc captureDoc
			w.json(&doc, "capture", "--upload", "--no-coverage", "--commit", testCommit, "--branch", "main").want(t, ExitOK)
			var carried struct {
				Captures []map[string]any `json:"captures"`
			}
			if err := json.Unmarshal(uploadedManifest(t, srv), &carried); err != nil {
				t.Fatal(err)
			}
			if got := carried.Captures[0]["findings"]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findings = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// uploadedManifest is the manifest part of the one upload the fake
// Captures API received, verbatim.
func uploadedManifest(t *testing.T, srv *fakeServer) []byte {
	t.Helper()
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.ctx.captures) != 1 {
		t.Fatalf("the server received %d uploads, want 1", len(srv.ctx.captures))
	}
	return srv.ctx.captures[0].raw
}

// uploadedFindings is the first capture's findings as JSON objects, so a
// test can assert about a member the typed shapes deliberately lack.
func uploadedFindings(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var carried struct {
		Captures []struct {
			Findings []map[string]any `json:"findings"`
		} `json:"captures"`
	}
	if err := json.Unmarshal(raw, &carried); err != nil {
		t.Fatal(err)
	}
	if len(carried.Captures) == 0 || len(carried.Captures[0].Findings) == 0 {
		t.Fatalf("the manifest carries no findings:\n%s", raw)
	}
	return carried.Captures[0].Findings
}
