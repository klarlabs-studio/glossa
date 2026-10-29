//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
)

// The visual findings a capture upload carries (RFC 0005 §5.1): Context
// validates them, resolves their keys and hands them to Quality beside
// the capture it just minted. What the page could not know — the
// fingerprint, and which capture `r_0` is a region of — is completed on
// this side of the wire, and these tests are about the handover.

// recorder is Quality's port, remembering what the ingest handed over.
type recorder struct {
	calls []app.RecordFindings
	// err is what the next call answers instead of recording.
	err error
}

func (r *recorder) RecordFindings(_ context.Context, in app.RecordFindings) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	r.calls = append(r.calls, in)
	n := 0
	for _, c := range in.Captures {
		n += len(c.Findings)
	}
	return n, nil
}

// withFindings puts findings on the manifest's capture at index i.
func withFindings(t *testing.T, manifest []byte, i int, findings ...map[string]any) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(manifest, &doc); err != nil {
		t.Fatal(err)
	}
	captures, _ := doc["captures"].([]any)
	capture, _ := captures[i].(map[string]any)
	list := make([]any, len(findings))
	for j, f := range findings {
		list[j] = f
	}
	capture["findings"] = list
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// probeFinding is a finding as the probe pass writes it: no
// fingerprint, and a region of this capture but no capture.
func probeFinding(code, key, region string) map[string]any {
	f := map[string]any{
		"schema": "glossa.finding/v1", "layer": "visual", "code": code, "severity": "warning",
		"locus": map[string]any{"key": key}, "message": code + " on " + key,
	}
	if region != "" {
		f["locus"].(map[string]any)["region"] = region
	}
	return f
}

func TestIngestCapturesHandsTheVisualFindingsToQuality(t *testing.T) {
	quality := &recorder{}
	h, _ := withImages(t, app.WithFindings(quality))
	f := h.project(t, "shop", []string{"web"}, "checkout.pay", "nav.home")
	pay := pngOf(t, 64, 48, 1, png.BestSpeed)
	manifest := withFindings(t, manifestOf(t, "abcdef1", "main",
		capture{"/checkout", "de", pay, []string{"checkout.pay", "nav.home"}}), 0,
		probeFinding("text-clipped", "checkout.pay", "r_0"),
		probeFinding("runtime-missing-message", "gone.key", ""))

	got := h.uploadCaptures(t, f, manifest, partsOf(pay))
	if got.Findings != 2 {
		t.Errorf("findings = %d, want the two the manifest carried", got.Findings)
	}
	if len(quality.calls) != 1 {
		t.Fatalf("%d handovers", len(quality.calls))
	}
	in := quality.calls[0]
	if in.Project != f.project || in.Ref != "main" || in.Commit == "" || in.At.IsZero() {
		t.Errorf("handover = %+v", in)
	}
	if len(in.Captures) != 1 || len(in.Captures[0].Findings) != 2 {
		t.Fatalf("captures = %+v", in.Captures)
	}
	// The capture is the one this ingest minted, and its locale is the
	// one every finding on it is about unless the probe named another.
	if in.Captures[0].Capture == uuid.Nil || in.Captures[0].Locale != "de" {
		t.Errorf("capture = %+v", in.Captures[0])
	}
	if n := count(t, "SELECT count(*) FROM context_captures WHERE id = $1", in.Captures[0].Capture); n != 1 {
		t.Errorf("capture %s is not a stored capture", in.Captures[0].Capture)
	}
	// The key the catalog knows is resolved; the one it doesn't is kept
	// and carries no message.
	clipped, runtime := in.Captures[0].Findings[0], in.Captures[0].Findings[1]
	if clipped.MessageID == nil || clipped.Key != "checkout.pay" || clipped.Region != "r_0" {
		t.Errorf("finding = %+v", clipped)
	}
	if runtime.MessageID != nil || runtime.Key != "gone.key" || runtime.Region != "" {
		t.Errorf("finding = %+v", runtime)
	}
	// An unknown key a finding names is not an unknown *region* key: the
	// captures' own report stays about what was on the screen.
	if len(got.UnknownKeys) != 0 {
		t.Errorf("unknown region keys = %v", got.UnknownKeys)
	}

	// A replay stores nothing and hands over nothing: the same capture
	// twice is not two sightings.
	again := h.uploadCaptures(t, f, manifest, partsOf(pay))
	if !again.Replayed || again.Findings != 0 || len(quality.calls) != 1 {
		t.Errorf("replay = %+v, %d handovers", again, len(quality.calls))
	}
}

// The scope the two-sighting rule counts in (RFC 0005 §5.2): every
// capture goes over with the previous capture of the same (route,
// viewport, locale) this application took, whatever branch it came
// from. A scope nobody has captured before hands over uuid.Nil, and its
// findings are first sightings — the server never promotes on a guess.
func TestIngestCapturesNamesThePreviousCaptureOfEachScope(t *testing.T) {
	quality := &recorder{}
	h, _ := withImages(t, app.WithFindings(quality))
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	pay := pngOf(t, 64, 48, 1, png.BestSpeed)
	other := pngOf(t, 64, 48, 2, png.BestSpeed)
	shot := func(img []byte) []byte {
		return withFindings(t, manifestOf(t, "abcdef1", "main",
			capture{"/checkout", "de", img, []string{"checkout.pay"}}), 0,
			probeFinding("text-clipped", "checkout.pay", "r_0"))
	}

	h.uploadCaptures(t, f, shot(pay), partsOf(pay))
	if len(quality.calls) != 1 || quality.calls[0].Captures[0].Previous != uuid.Nil {
		t.Fatalf("the first capture of a scope names %v as its previous", quality.calls[0].Captures[0].Previous)
	}
	was := quality.calls[0].Captures[0]
	if was.Route != "/checkout" || was.Viewport.Width != 64 || was.Locale != "de" {
		t.Errorf("scope = %+v", was)
	}

	// A second upload of the same route, viewport and locale — a
	// different commit and a different branch, because the rule is flake
	// control and not blame.
	second := withFindings(t, manifestOf(t, "abcdef2", "feat/copy",
		capture{"/checkout", "de", other, []string{"checkout.pay"}}), 0,
		probeFinding("text-clipped", "checkout.pay", "r_0"))
	h.uploadCaptures(t, f, second, partsOf(other))
	if len(quality.calls) != 2 {
		t.Fatalf("%d handovers", len(quality.calls))
	}
	if got := quality.calls[1].Captures[0].Previous; got != was.Capture {
		t.Errorf("previous = %s, want the capture of the same scope (%s)", got, was.Capture)
	}
	// Another locale of the same route is another scope, and has no
	// previous capture of its own.
	third := withFindings(t, manifestOf(t, "abcdef3", "main",
		capture{"/checkout", "fr", other, []string{"checkout.pay"}}), 0,
		probeFinding("text-clipped", "checkout.pay", "r_0"))
	h.uploadCaptures(t, f, third, partsOf(other))
	if got := quality.calls[2].Captures[0].Previous; got != uuid.Nil {
		t.Errorf("another locale's previous = %s, want none", got)
	}
}

// A capture upload whose findings cannot be recorded stores nothing at
// all: the upload is idempotent by its manifest, so a build committed
// without its findings could never get them back.
func TestIngestCapturesStoresNothingWhenTheFindingsCannotBeRecorded(t *testing.T) {
	quality := &recorder{err: errors.New("quality is down")}
	h, objects := withImages(t, app.WithFindings(quality))
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	pay := pngOf(t, 64, 48, 1, png.BestSpeed)
	manifest := withFindings(t, manifestOf(t, "abcdef1", "main",
		capture{"/checkout", "de", pay, []string{"checkout.pay"}}), 0,
		probeFinding("text-clipped", "checkout.pay", "r_0"))

	if _, err := h.svc.IngestCaptures(h.ci(), app.IngestCaptures{
		Project: f.project, Manifest: manifest, Images: partsOf(pay),
	}); err == nil {
		t.Fatal("upload succeeded")
	}
	if got := storedDigests(t); len(got) != 0 {
		t.Errorf("captures stored = %v", got)
	}
	if n := len(objects.Keys()); n != 0 {
		t.Errorf("%d images left behind", n)
	}
}

// A capture that carries more findings than may be stored is refused,
// not truncated (RFC 0005 §10).
func TestIngestCapturesRefusesTooManyFindings(t *testing.T) {
	quality := &recorder{}
	h, _ := withImages(t, app.WithFindings(quality))
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	pay := pngOf(t, 64, 48, 1, png.BestSpeed)
	many := make([]map[string]any, domain.MaxFindingsPerCapture+1)
	for i := range many {
		many[i] = probeFinding("text-clipped", "checkout.pay", "r_0")
	}
	manifest := withFindings(t, manifestOf(t, "abcdef1", "main",
		capture{"/checkout", "de", pay, []string{"checkout.pay"}}), 0, many...)

	_, err := h.svc.IngestCaptures(h.ci(), app.IngestCaptures{
		Project: f.project, Manifest: manifest, Images: partsOf(pay),
	})
	if !errors.Is(err, domain.ErrTooManyFindings) {
		t.Errorf("error = %v, want ErrTooManyFindings", err)
	}
	if len(quality.calls) != 0 {
		t.Errorf("%d handovers", len(quality.calls))
	}
}
