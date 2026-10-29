package domain_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/context/domain"
)

// A capture's visual findings (RFC 0005 §5) on the way in: what the
// probe pass wrote in the page, held to the shape a page is allowed to
// write and to the caps of RFC 0005 §10. What the page could not know —
// the fingerprint and the capture — is not here: the ingest completes
// it, and nothing a manifest says can influence either.

// findingsManifest is a one-capture manifest with two regions carrying
// the findings in raw JSON.
func findingsManifest(findings string) []byte {
	return fmt.Appendf(nil, `{
      "schema": "glossa.captures/v1",
      "application": "web",
      "commit": "%s",
      "branch": "main",
      "tool": {"name": "glossa", "version": "0.9.0"},
      "captures": [{
        "route": "/checkout",
        "url": "http://localhost:4173/checkout",
        "viewport": {"width": 1280, "height": 800},
        "locale": "de",
        "image": {"sha256": "%s", "width": 1280, "height": 2400},
        "renders": [],
        "regions": [
          {"key": "checkout.pay", "kind": "element", "box": {"x": 0, "y": 0, "width": 10, "height": 10}, "visible": true},
          {"key": "checkout.help", "kind": "element", "box": {"x": 0, "y": 20, "width": 10, "height": 10}, "visible": true}
        ],
        "findings": [%s]
      }]
    }`, strings.Repeat("9", 40), strings.Repeat("ab", 32), findings)
}

// probeFinding is one finding as the probe pass writes it.
func probeFinding(members ...string) string {
	return `{"schema": "glossa.finding/v1", "layer": "visual", "code": "text-clipped", "severity": "warning",
	         "locus": {"key": "checkout.pay", "region": "r_0"},
	         "message": "Clipped: 412x20 px of text in 358x20 px."` + strings.Join(append([]string{""}, members...), ", ") + `}`
}

func TestParseCapturesReadsTheProbePassFindings(t *testing.T) {
	up, err := domain.ParseCaptures(findingsManifest(probeFinding(
		`"subject": "checkout.help"`, `"evidence": {"box": [358, 20], "content": [412, 20]}`)))
	if err != nil {
		t.Fatal(err)
	}
	got := up.Captures[0].Findings
	if len(got) != 1 {
		t.Fatalf("findings = %+v", got)
	}
	f := got[0]
	if f.Code != "text-clipped" || f.Key != "checkout.pay" || f.Region != "r_0" || f.Subject != "checkout.help" ||
		f.Message == "" || f.MessageID != nil {
		t.Errorf("finding = %+v", f)
	}
	if fmt.Sprint(f.Evidence["content"]) != "[412 20]" {
		t.Errorf("evidence = %v", f.Evidence)
	}
	if up.FindingCount() != 1 {
		t.Errorf("FindingCount = %d", up.FindingCount())
	}
}

// The keys a lookup has to resolve are the regions' and the findings':
// one lookup, so a finding's message is the same message the region's
// is (and a rename never splits them).
func TestCaptureUploadKeysCoverTheFindings(t *testing.T) {
	up, err := domain.ParseCaptures(findingsManifest(probeFinding() + "," +
		strings.Replace(probeFinding(), `"key": "checkout.pay", "region": "r_0"`, `"key": "cart.total"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"checkout.pay", "checkout.help", "cart.total"}
	if got := up.Keys(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("keys = %v, want %v", got, want)
	}
}

func TestParseCapturesRefusesAFindingAPageCouldNotHaveWritten(t *testing.T) {
	cases := map[string]string{
		// A page reports what a page can see, at the one severity it may
		// emit: promotion to `error` needs two consecutive captures
		// (RFC 0005 §5.2) and only the server sees those.
		"another layer":   `"layer": "length"`,
		"another schema":  `"schema": "glossa.finding/v2"`,
		"error severity":  `"severity": "error"`,
		"waived severity": `"severity": "waived"`,
		// Everything the finding.v1 schema bounds.
		"an uppercase code":     `"code": "textClipped"`,
		"an empty code":         `"code": ""`,
		"an empty message":      `"message": ""`,
		"a 4001-byte message":   fmt.Sprintf(`"message": %q`, strings.Repeat("m", 4001)),
		"a 501-byte subject":    fmt.Sprintf(`"subject": %q`, strings.Repeat("s", 501)),
		"a key that isn't one":  `"locus": {"key": "Checkout Pay"}`,
		"a locale that isn't":   `"locus": {"locale": "de_DE"}`,
		"evidence over 4 kB":    fmt.Sprintf(`"evidence": {"t": %q}`, strings.Repeat("e", 4100)),
		"a region name":         `"locus": {"region": "checkout.pay"}`,
		"a region of no index":  `"locus": {"region": "r_"}`,
		"a region past the end": `"locus": {"region": "r_2"}`,
		"a huge region index":   `"locus": {"region": "r_999999999999999999999"}`,
	}
	for why, member := range cases {
		t.Run(why, func(t *testing.T) {
			// A repeated member wins over the default one, in JSON as in Go.
			if _, err := domain.ParseCaptures(findingsManifest(probeFinding(member))); err == nil {
				t.Errorf("ParseCaptures accepted %s", member)
			}
		})
	}
}

// A page that floods findings must not be able to flood storage
// (RFC 0005 §10): at most 500 on a capture, at most 10 000 in an
// upload — and an upload past either is refused, not truncated, because
// a number a dashboard shows has to be true.
func TestParseCapturesRefusesTooManyFindings(t *testing.T) {
	many := func(n int) string {
		fs := make([]string, n)
		for i := range fs {
			fs[i] = strings.Replace(probeFinding(), `"code": "text-clipped"`, fmt.Sprintf(`"code": "c-%d"`, i), 1)
		}
		return strings.Join(fs, ",")
	}
	if _, err := domain.ParseCaptures(findingsManifest(many(domain.MaxFindingsPerCapture))); err != nil {
		t.Errorf("%d findings: %v", domain.MaxFindingsPerCapture, err)
	}
	_, err := domain.ParseCaptures(findingsManifest(many(domain.MaxFindingsPerCapture + 1)))
	if !errors.Is(err, domain.ErrTooManyFindings) {
		t.Errorf("%d findings: %v", domain.MaxFindingsPerCapture+1, err)
	}
}

// A manifest written before the probe pass keeps the digest it had: the
// upload is idempotent by that digest, and a capture with no findings
// member must not suddenly re-upload as a new build.
func TestCapturesDigestIgnoresAnAbsentFindingsMember(t *testing.T) {
	empty := findingsManifest("")
	without, err := domain.ParseCaptures([]byte(strings.Replace(string(empty), `,
        "findings": []`, "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	none, err := domain.ParseCaptures(empty)
	if err != nil {
		t.Fatal(err)
	}
	if without.Digest != none.Digest {
		t.Errorf("a manifest with no findings member digests as %s, one with an empty list as %s",
			without.Digest, none.Digest)
	}
	with, err := domain.ParseCaptures(findingsManifest(probeFinding()))
	if err != nil {
		t.Fatal(err)
	}
	if with.Digest == without.Digest {
		t.Error("a manifest with findings has the digest of one without")
	}
}

func TestResolveFindings(t *testing.T) {
	id := uuid.MustParse("0192f5a1-0000-7000-8000-00000000beef")
	findings := []domain.VisualFinding{
		{Key: "checkout.pay"}, {Key: "cart.total"}, {Code: "runtime-format"},
	}
	if unknown := domain.ResolveFindings(findings, map[string]uuid.UUID{"checkout.pay": id}); unknown != 1 {
		t.Errorf("unknown = %d", unknown)
	}
	if findings[0].MessageID == nil || *findings[0].MessageID != id {
		t.Errorf("resolved = %+v", findings[0])
	}
	// A finding about a key the catalog doesn't know is kept: it is
	// still true, and its fingerprint falls back to the key.
	if findings[1].MessageID != nil || findings[2].MessageID != nil {
		t.Errorf("unknown keys resolved: %+v", findings[1:])
	}
	if got := domain.FindingKeys(findings); strings.Join(got, ",") != "checkout.pay,cart.total" {
		t.Errorf("FindingKeys = %v", got)
	}
}
