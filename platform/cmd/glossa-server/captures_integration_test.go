//go:build integration

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// testPNG is a w×h PNG whose pixels depend on seed.
func testPNG(t *testing.T, w, h int, seed uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x) + seed, G: uint8(y), B: seed, A: 255}) //nolint:gosec // test pattern
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// capturesUpload is a multipart capture upload: the manifest, then one
// part per image named by its digest.
func capturesUpload(t *testing.T, manifest any, images ...[]byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	m, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="manifest"`)
	h.Set("Content-Type", "application/json")
	part, _ := w.CreatePart(h)
	_, _ = part.Write(m)
	for _, img := range images {
		h := textproto.MIMEHeader{}
		d := sha256Hex(img)
		h.Set("Content-Disposition", `form-data; name="`+d+`"; filename="`+d+`.png"`)
		h.Set("Content-Type", "image/png")
		part, _ := w.CreatePart(h)
		_, _ = part.Write(img)
	}
	_ = w.Close()
	return buf.Bytes(), w.FormDataContentType()
}

func capturesManifest(commit string, img []byte, w, h int) map[string]any {
	return map[string]any{
		"schema": "glossa.captures/v1", "application": "web", "commit": commit, "branch": "main",
		"tool": map[string]any{"name": "glossa", "version": "0.9.0"},
		"captures": []any{map[string]any{
			"route": "/checkout", "url": "http://localhost:4173/checkout", "locale": "de",
			"viewport": map[string]any{"width": w, "height": 800, "deviceScaleFactor": 1},
			"image":    map[string]any{"sha256": sha256Hex(img), "width": w, "height": h},
			"renders":  []any{map[string]any{"index": 0, "key": "checkout.total", "locale": "de"}},
			"regions": []any{
				map[string]any{"key": "checkout.pay", "kind": "element", "box": map[string]any{"x": 4, "y": 8.5, "width": 20, "height": 10}, "visible": true},
				map[string]any{"index": 0, "kind": "text", "box": map[string]any{"x": 0, "y": 0, "width": 0, "height": 0}, "visible": false},
				map[string]any{"key": "gone.key", "kind": "attribute", "attribute": "title", "box": map[string]any{"x": 1, "y": 1, "width": 2, "height": 2}, "visible": true},
			},
		}},
	}
}

// The Captures API (RFC 0004 §3.2–§3.4, §9): CI uploads captures with a
// write token as multipart; members read where a message appears on
// screen and the image, through the API.
func TestCapturesAPIOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	var project struct{ ID string }
	s.do(call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "shop", "name": "Shop", "source_locale": "en"}}).decode(t, &project)
	p := base + "/projects/" + project.ID
	s.do(call{method: "POST", path: p + "/applications", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "web", "name": "Web", "platform": "web"}}).want(t, http.StatusCreated, "")
	var tok struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "ci", "scopes": []string{"write"}}}).decode(t, &tok)
	s.do(call{method: "POST", path: p + "/message-upserts", bearer: tok.Secret, body: map[string]any{"items": []map[string]any{
		{"key": "checkout.pay", "text": "Pay"}, {"key": "checkout.total", "text": "Total"},
	}}}).want(t, http.StatusOK, "")

	commit := strings.Repeat("9f2c1e7a", 5)
	img := testPNG(t, 48, 32, 1)
	body, contentType := capturesUpload(t, capturesManifest(commit, img, 48, 32), img)
	upload := call{method: "POST", path: p + "/captures", bearer: tok.Secret, raw: body, contentType: contentType}
	var got struct {
		Build struct {
			ID     string `json:"id"`
			Source string `json:"source"`
			Usages int    `json:"usages"`
		} `json:"build"`
		Captures           int      `json:"captures"`
		ImagesStored       int      `json:"images_stored"`
		ImagesDeduplicated int      `json:"images_deduplicated"`
		UnknownKeys        []string `json:"unknown_keys"`
	}
	r := s.do(upload)
	r.want(t, http.StatusCreated, "")
	r.decode(t, &got)
	if got.Build.Source != "capture" || got.Captures != 1 || got.ImagesStored != 1 || got.ImagesDeduplicated != 0 ||
		len(got.UnknownKeys) != 1 || got.UnknownKeys[0] != "gone.key" {
		t.Fatalf("upload = %s", r.body)
	}
	r = s.do(upload)
	r.want(t, http.StatusOK, "")
	if r.header.Get("Idempotent-Replayed") != "true" || !strings.Contains(string(r.body), got.Build.ID) {
		t.Errorf("replay = %d %v %s", r.status, r.header, r.body)
	}

	// Refusals.
	other := testPNG(t, 48, 32, 2)
	bad, ct := capturesUpload(t, capturesManifest(strings.Repeat("b", 40), img, 48, 32), other)
	s.do(call{method: "POST", path: p + "/captures", bearer: tok.Secret, raw: bad, contentType: ct}).
		want(t, http.StatusBadRequest, "invalid_captures")
	notPNG := []byte("GIF89a not a png")
	bad, ct = capturesUpload(t, capturesManifest(strings.Repeat("a", 40), notPNG, 48, 32), notPNG)
	s.do(call{method: "POST", path: p + "/captures", bearer: tok.Secret, raw: bad, contentType: ct}).
		want(t, http.StatusBadRequest, "invalid_image")
	s.do(call{method: "POST", path: p + "/captures", bearer: tok.Secret, body: map[string]any{"manifest": 1}}).
		want(t, http.StatusBadRequest, "invalid_request")
	s.do(call{method: "POST", path: p + "/captures", cookie: ada.cookie, raw: body, contentType: contentType}).
		want(t, http.StatusForbidden, "csrf_invalid")

	// Where checkout.pay appears on screen.
	var captures struct {
		MessageID string `json:"message_id"`
		Truncated bool   `json:"truncated"`
		Captures  []struct {
			ID              string `json:"id"`
			Route           string `json:"route"`
			Locale          string `json:"locale"`
			OnDefaultBranch bool   `json:"on_default_branch"`
			Viewport        struct{ Width, Height int }
			Image           struct {
				Digest string `json:"digest"`
				Width  int    `json:"width"`
				URL    string `json:"url"`
			} `json:"image"`
			Regions []struct {
				Kind    string                            `json:"kind"`
				Visible bool                              `json:"visible"`
				Box     struct{ X, Y, Width, Height int } `json:"box"`
			} `json:"regions"`
		} `json:"captures"`
	}
	s.do(call{method: "GET", path: p + "/messages/checkout.pay/captures", cookie: ada.cookie}).decode(t, &captures)
	if len(captures.Captures) != 1 || captures.MessageID == "" || captures.Truncated {
		t.Fatalf("captures of checkout.pay = %+v", captures)
	}
	c := captures.Captures[0]
	if c.Route != "/checkout" || c.Locale != "de" || !c.OnDefaultBranch || c.Viewport.Width != 48 || c.Image.Width != 48 ||
		len(c.Regions) != 1 || c.Regions[0].Kind != "element" || c.Regions[0].Box.Y != 8 || c.Regions[0].Box.Height != 11 ||
		c.Image.URL != p+"/captures/"+c.ID+"/image" || len(c.Image.Digest) != 64 {
		t.Errorf("capture = %+v", c)
	}
	var total struct {
		Captures []struct {
			Regions []struct {
				Visible bool `json:"visible"`
			} `json:"regions"`
		} `json:"captures"`
	}
	s.do(call{method: "GET", path: p + "/messages/checkout.total/captures?branch=feat/x", cookie: ada.cookie}).decode(t, &total)
	if len(total.Captures) != 1 || len(total.Captures[0].Regions) != 1 || total.Captures[0].Regions[0].Visible {
		t.Errorf("checkout.total on a branch view = %+v", total)
	}
	s.do(call{method: "GET", path: p + "/messages/nope.none/captures", cookie: ada.cookie}).want(t, http.StatusNotFound, "not_found")

	// The image, through the API.
	r = s.do(call{method: "GET", path: c.Image.URL, cookie: ada.cookie})
	if r.status != http.StatusOK || r.header.Get("Content-Type") != "image/png" ||
		r.header.Get("Cache-Control") != "private, max-age=31536000, immutable" ||
		r.header.Get("ETag") != `"`+c.Image.Digest+`"` || sha256Hex(r.body) != c.Image.Digest {
		t.Fatalf("image = %d %v (%d bytes)", r.status, r.header, len(r.body))
	}
	if decoded, err := png.Decode(bytes.NewReader(r.body)); err != nil || decoded.Bounds().Dx() != 48 {
		t.Errorf("stored image doesn't decode: %v", err)
	}
	r = s.do(call{method: "GET", path: c.Image.URL, cookie: ada.cookie, headers: map[string]string{"If-None-Match": `"` + c.Image.Digest + `"`}})
	if r.status != http.StatusNotModified || len(r.body) != 0 {
		t.Errorf("If-None-Match = %d (%d bytes)", r.status, len(r.body))
	}
	s.do(call{method: "GET", path: p + "/captures/" + got.Build.ID + "/image", cookie: ada.cookie}).want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "GET", path: p + "/captures/not-a-uuid/image", cookie: ada.cookie}).want(t, http.StatusNotFound, "not_found")

	// Capture coverage: checkout.pay has a visible region, checkout.total none.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(s.metrics(), `glossa_context_capture_coverage_ratio{project="`+project.ID+`",tenant="`+org.ID+`"} 0.5`) {
		if time.Now().After(deadline) {
			t.Fatalf("no capture coverage metric:\n%s", s.metrics())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(s.metrics(), `glossa_context_capture_images_total{outcome="stored",tenant="`+org.ID+`"} 1`) {
		t.Error("stored images not counted")
	}
}
