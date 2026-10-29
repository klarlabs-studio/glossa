//go:build integration

package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/context/adapters/imaging"
	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
)

// pngOf is a w×h PNG whose pixels depend on seed, encoded at level (so
// the same pixels can be uploaded as different bytes).
func pngOf(t *testing.T, w, h int, seed uint8, level png.CompressionLevel) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x) + seed, G: uint8(y), B: seed, A: 255}) //nolint:gosec // test pattern
		}
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: level}).Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func hexDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// capture is one capture of a manifest: its route, locale, image and
// regions by key (visible unless the key starts with "hidden:").
type capture struct {
	route, locale string
	image         []byte
	keys          []string
}

func manifestOf(t *testing.T, commit, branch string, captures ...capture) []byte {
	t.Helper()
	var cs []map[string]any
	for _, c := range captures {
		cfg, err := png.DecodeConfig(bytes.NewReader(c.image))
		if err != nil {
			t.Fatal(err)
		}
		regions := []map[string]any{}
		renders := []map[string]any{}
		for i, k := range c.keys {
			visible := !strings.HasPrefix(k, "hidden:")
			k = strings.TrimPrefix(k, "hidden:")
			box := map[string]any{"x": 10 * i, "y": 5.5, "width": 100, "height": 20}
			if i%2 == 1 { // a t() string, through the render log
				renders = append(renders, map[string]any{"index": i, "key": k, "locale": c.locale})
				regions = append(regions, map[string]any{"index": i, "kind": "text", "box": box, "visible": visible})
			} else {
				regions = append(regions, map[string]any{"key": k, "kind": "element", "box": box, "visible": visible})
			}
		}
		cs = append(cs, map[string]any{
			"route": c.route, "url": "http://localhost:4173" + c.route, "locale": c.locale,
			"viewport": map[string]any{"width": cfg.Width, "height": 800},
			"image":    map[string]any{"sha256": hexDigest(c.image), "width": cfg.Width, "height": cfg.Height},
			"renders":  renders, "regions": regions,
		})
	}
	doc, err := json.Marshal(map[string]any{
		"schema": "glossa.captures/v1", "application": "web", "commit": sha(commit), "branch": branch,
		"tool": map[string]any{"name": "glossa", "version": "0.9.0"}, "captures": cs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// parts is an upload's image parts.
type parts struct {
	names  []string
	bodies [][]byte
	read   int
}

func partsOf(images ...[]byte) *parts {
	p := &parts{}
	seen := map[string]bool{}
	for _, img := range images {
		if d := hexDigest(img); !seen[d] {
			seen[d] = true
			p.add(d, img)
		}
	}
	return p
}

func (p *parts) add(name string, body []byte) *parts {
	p.names, p.bodies = append(p.names, name), append(p.bodies, body)
	return p
}

func (p *parts) Next() (string, io.Reader, error) {
	if p.read == len(p.names) {
		return "", nil, io.EOF
	}
	p.read++
	return p.names[p.read-1], bytes.NewReader(p.bodies[p.read-1]), nil
}

// withImages is a harness whose service stores images in memory.
func withImages(t *testing.T, opts ...app.Option) (*harness, *objectstore.Memory) {
	t.Helper()
	objects := objectstore.NewMemory()
	return withObjects(t, objects, opts...), objects
}

// withObjects is a harness whose service stores images in objects (the
// retention tests point it at a real MinIO).
func withObjects(t *testing.T, objects app.Objects, opts ...app.Option) *harness {
	t.Helper()
	return newHarness(t, append(opts, app.WithImages(objects, imaging.New(t.TempDir(), 0)))...)
}

func (h *harness) uploadCaptures(t *testing.T, f fixture, manifest []byte, p *parts) app.CapturesIngested {
	t.Helper()
	out, err := h.svc.IngestCaptures(h.ci(), app.IngestCaptures{Project: f.project, Manifest: manifest, Images: p})
	if err != nil {
		t.Fatalf("upload captures: %v", err)
	}
	h.clock.advance(time.Minute)
	return out
}

func (h *harness) imageKey(f fixture, d string) string {
	return domain.ImageKey(h.tenant, f.project, domain.Digest(d))
}

func storedDigests(t *testing.T) []string {
	t.Helper()
	rows, err := env.Super.Query(context.Background(), "SELECT DISTINCT image_digest FROM context_captures ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func TestIngestCapturesStoresReencodedImagesCapturesAndRegions(t *testing.T) {
	h, objects := withImages(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay", "checkout.total", "nav.home")
	pay := pngOf(t, 64, 48, 1, png.BestSpeed)
	home := pngOf(t, 32, 20, 2, png.BestSpeed)
	manifest := manifestOf(t, "abcdef1", "main",
		capture{"/checkout", "de", pay, []string{"checkout.pay", "checkout.total", "gone.key", "hidden:nav.home"}},
		capture{"/", "en", home, []string{"nav.home"}})

	got := h.uploadCaptures(t, f, manifest, partsOf(pay, home))
	if got.Replayed || got.Captures != 2 || got.ImagesStored != 2 || got.ImagesDeduplicated != 0 ||
		!slices.Equal(got.UnknownKeys, []string{"gone.key"}) {
		t.Errorf("ingested = %+v", got)
	}
	b := got.Build
	if b.Source != domain.SourceCapture || !b.OnDefaultBranch || b.UsageCount != 0 || b.ApplicationID != f.apps["web"] {
		t.Errorf("build = %+v", b)
	}
	// The images are stored re-encoded: by their own digest, not the
	// upload's.
	stored := storedDigests(t)
	if len(stored) != 2 || slices.Contains(stored, hexDigest(pay)) {
		t.Fatalf("stored digests = %v", stored)
	}
	for _, d := range stored {
		body, err := objects.Get(context.Background(), h.imageKey(f, d), domain.MaxImageBytes)
		if err != nil || hexDigest(body) != d {
			t.Errorf("object %s: %v", d, err)
		}
	}
	if n := count(t, "SELECT count(*) FROM context_regions WHERE message_id = $1 AND kind = 'text'", f.ids["checkout.total"]); n != 1 {
		t.Errorf("the render log's region of checkout.total = %d", n)
	}
	if n := count(t, "SELECT count(*) FROM context_regions WHERE message_id IS NULL AND message_key = 'gone.key'"); n != 1 {
		t.Errorf("unknown key regions = %d", n)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'context.capture.ingested'"); n != 2 {
		t.Errorf("capture events = %d", n)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'context.build.ingested' AND aggregate_id = $1", b.ID.String()); n != 1 {
		t.Errorf("build events = %d", n)
	}

	// The same manifest again is a replay: its images aren't read.
	unread := &parts{}
	unread.add("0000", []byte("never read"))
	again := h.uploadCaptures(t, f, manifest, unread)
	if !again.Replayed || again.Build.ID != b.ID || again.Captures != 2 || again.ImagesStored != 0 ||
		!slices.Equal(again.UnknownKeys, []string{"gone.key"}) || unread.read != 0 {
		t.Errorf("replay = %+v (parts read %d)", again, unread.read)
	}

	// The same pixels, encoded differently, in another build: stored once.
	payAgain := pngOf(t, 64, 48, 1, png.BestCompression)
	next := h.uploadCaptures(t, f, manifestOf(t, "abcdef2", "main",
		capture{"/checkout", "de", payAgain, []string{"checkout.pay"}}), partsOf(payAgain))
	if next.ImagesStored != 0 || next.ImagesDeduplicated != 1 || next.Build.ID == b.ID {
		t.Errorf("dedupe = %+v", next)
	}
	if n := len(storedDigests(t)); n != 2 {
		t.Errorf("distinct images = %d, want 2", n)
	}
	h.drain(t)
}

func TestIngestCapturesRefusals(t *testing.T) {
	h, objects := withImages(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	pay := pngOf(t, 16, 16, 1, png.DefaultCompression)
	other := pngOf(t, 16, 16, 9, png.DefaultCompression)
	manifest := manifestOf(t, "abcdef1", "main", capture{"/checkout", "de", pay, []string{"checkout.pay"}})
	wrongSize := manifestOf(t, "abcdef1", "main", capture{"/checkout", "de", pngOf(t, 17, 16, 1, png.DefaultCompression), nil})
	var wrong map[string]any
	_ = json.Unmarshal(wrongSize, &wrong)
	img := wrong["captures"].([]any)[0].(map[string]any)["image"].(map[string]any)
	img["sha256"] = hexDigest(pay) // says 17×16, is 16×16
	wrongSize, _ = json.Marshal(wrong)
	webp := []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")

	cases := map[string]struct {
		ctx      context.Context
		manifest []byte
		parts    *parts
		want     error
	}{
		"invalid manifest":   {h.ci(), []byte(`{"schema":"glossa.captures/v1"}`), partsOf(pay), domain.ErrInvalidCaptures},
		"missing part":       {h.ci(), manifest, partsOf(), domain.ErrInvalidCaptures},
		"unreferenced part":  {h.ci(), manifest, partsOf(pay, other), domain.ErrInvalidCaptures},
		"part named oddly":   {h.ci(), manifest, partsOf(pay).add("manifest", []byte("{}")), domain.ErrInvalidCaptures},
		"part twice":         {h.ci(), manifest, partsOf(pay).add(hexDigest(pay), pay), domain.ErrInvalidCaptures},
		"part not its name":  {h.ci(), manifest, (&parts{}).add(hexDigest(pay), other), domain.ErrInvalidImage},
		"not a PNG":          {h.ci(), manifestOf(t, "abcdef1", "main", capture{"/c", "de", pay, nil}), (&parts{}).add(hexDigest(pay), webp), domain.ErrInvalidImage},
		"size isn't the PNG": {h.ci(), wrongSize, partsOf(pay), domain.ErrInvalidImage},
		"translator":         {h.translator(), manifest, partsOf(pay), authz.ErrForbidden},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := h.svc.IngestCaptures(tc.ctx, app.IngestCaptures{Project: f.project, Manifest: tc.manifest, Images: tc.parts})
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	unknownApp := bytes.Replace(manifest, []byte(`"application":"web"`), []byte(`"application":"ios"`), 1)
	if _, err := h.svc.IngestCaptures(h.ci(), app.IngestCaptures{Project: f.project, Manifest: unknownApp, Images: partsOf(pay)}); !errors.Is(err, app.ErrApplicationNotFound) {
		t.Errorf("unknown application: err = %v", err)
	}
	// Images are stored as they arrive; a refused upload removes the ones
	// it wrote.
	twoShots := manifestOf(t, "abcdef3", "main", capture{"/a", "de", pay, nil}, capture{"/b", "de", other, nil})
	_, err := h.svc.IngestCaptures(h.ci(), app.IngestCaptures{Project: f.project, Manifest: twoShots,
		Images: partsOf(pay).add(hexDigest(other), webp)})
	if !errors.Is(err, domain.ErrInvalidImage) {
		t.Errorf("second part broken: err = %v", err)
	}
	if n := count(t, "SELECT count(*) FROM context_builds"); n != 0 {
		t.Errorf("builds after refusals = %d", n)
	}
	if n := objectCount(t, objects, h, f, pay, other); n != 0 {
		t.Errorf("objects after refusals = %d", n)
	}
	// Without object storage there is nowhere to put images.
	bare := newHarness(t)
	g := bare.project(t, "shop", []string{"web"})
	if _, err := bare.svc.IngestCaptures(bare.ci(), app.IngestCaptures{Project: g.project, Manifest: manifest, Images: partsOf(pay)}); err == nil {
		t.Error("a service without images accepted captures")
	}
}

// objectCount counts the stored objects of the given pixels' re-encoded
// images.
func objectCount(t *testing.T, objects *objectstore.Memory, h *harness, f fixture, images ...[]byte) int {
	t.Helper()
	n := 0
	for _, img := range images {
		normalized, err := imaging.New(t.TempDir(), 0).Normalize(context.Background(), bytes.NewReader(img), domain.Digest(hexDigest(img)))
		if err != nil {
			t.Fatal(err)
		}
		if ok, _ := objects.Exists(context.Background(), h.imageKey(f, normalized.Image().Digest.String())); ok {
			n++
		}
		_ = normalized.Close()
	}
	return n
}

// TestCaptureUploadsStopAtTheTenantsStorageQuota drives the per-tenant
// cap on capture images (RFC 0004 §3.3, §10): the upload that would
// pass it is refused whole, deduplicated pixels cost nothing, another
// tenant is unaffected, and retention frees the quota again.
func TestCaptureUploadsStopAtTheTenantsStorageQuota(t *testing.T) {
	a := pngOf(t, 200, 200, 1, png.BestSpeed)
	b := pngOf(t, 200, 200, 2, png.BestSpeed)
	c := pngOf(t, 200, 200, 3, png.BestSpeed)
	sa, sb, sc := storedSize(t, a), storedSize(t, b), storedSize(t, c)
	// Room for any two of the three, never all three.
	quota := sa + sb + sc - 1
	m := &metrics{}
	// Keep no history, so one purge frees the images only an older
	// build showed.
	h, objects := withImages(t, app.WithStorageQuota(quota), app.WithMetrics(m),
		app.WithRetention(domain.RetentionPolicy{Keep: 0, ClosedBranchGrace: 7 * 24 * time.Hour}))
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	shot := func(commit string, img []byte) []byte {
		return manifestOf(t, commit, "main", capture{"/checkout", "de", img, []string{"checkout.pay"}})
	}

	h.uploadCaptures(t, f, shot("abcdef1", a), partsOf(a))
	if used := m.storage(h.tenant); used != [2]int64{sa, quota} {
		t.Errorf("storage after the first upload = %v, want %d of %d", used, sa, quota)
	}
	h.uploadCaptures(t, f, shot("abcdef2", b), partsOf(b))

	// The same pixels again are deduplicated, so they cost nothing and
	// fit where new ones wouldn't.
	h.uploadCaptures(t, f, shot("abcdef3", a), partsOf(a))
	if used := m.storage(h.tenant); used[0] != sa+sb {
		t.Errorf("storage after a deduplicated upload = %v, want %d", used, sa+sb)
	}

	// New pixels don't fit: the upload is refused whole, and the image
	// it had written is gone again.
	over := shot("abcdef4", c)
	_, err := h.svc.IngestCaptures(h.ci(), app.IngestCaptures{Project: f.project, Manifest: over, Images: partsOf(c)})
	if !errors.Is(err, app.ErrStorageQuotaExceeded) {
		t.Fatalf("over the quota: err = %v", err)
	}
	if !strings.Contains(err.Error(), "past its") {
		t.Errorf("the refusal doesn't say how far past the quota it is: %v", err)
	}
	if n := objectCount(t, objects, h, f, c); n != 0 {
		t.Errorf("the refused upload left %d objects", n)
	}
	if n := count(t, "SELECT count(*) FROM context_builds"); n != 3 {
		t.Errorf("builds = %d, want only the three that fit", n)
	}

	// Retention frees the quota: the second build's image is the one no
	// surviving capture shows, so the refused upload fits afterwards.
	if _, err := h.svc.PurgeProject(h.developer(), f.project); err != nil {
		t.Fatal(err)
	}
	if used := m.storage(h.tenant); used[0] != sa {
		t.Errorf("storage after the purge = %v, want %d", used, sa)
	}
	h.uploadCaptures(t, f, over, partsOf(c))

	// The quota is per tenant, not per deployment: another tenant at the
	// same limit starts from nothing.
	other := harnessFor(t, "bolt", app.WithImages(objects, imaging.New(t.TempDir(), 0)), app.WithStorageQuota(quota))
	g := other.project(t, "blog", []string{"web"}, "checkout.pay")
	other.uploadCaptures(t, g, shot("abcdef9", b), partsOf(b))
}

// storedSize is how many bytes an image occupies once the server has
// re-encoded it.
func storedSize(t *testing.T, img []byte) int64 {
	t.Helper()
	n, err := imaging.New(t.TempDir(), 0).Normalize(context.Background(), bytes.NewReader(img), domain.Digest(hexDigest(img)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = n.Close() }()
	return n.Size()
}

func TestCaptureUploadsShareTheUsagesRateLimit(t *testing.T) {
	l := &limiter{n: 1, seen: map[string]int{}}
	h, _ := withImages(t, app.WithLimiter(l))
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	pay := pngOf(t, 8, 8, 1, png.DefaultCompression)
	manifest := manifestOf(t, "abcdef1", "main", capture{"/", "de", pay, nil})
	h.uploadCaptures(t, f, manifest, partsOf(pay))
	if _, err := h.svc.IngestCaptures(h.ci(), app.IngestCaptures{Project: f.project, Manifest: manifest, Images: partsOf(pay)}); !errors.Is(err, app.ErrRateLimited) {
		t.Errorf("second upload: err = %v", err)
	}
}

func TestCapturesOfKeyAndTheirImages(t *testing.T) {
	h, _ := withImages(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay", "nav.home")
	g := h.project(t, "blog", []string{"web"})
	main := pngOf(t, 40, 30, 1, png.DefaultCompression)
	mobile := pngOf(t, 20, 30, 2, png.DefaultCompression)
	branch := pngOf(t, 40, 30, 3, png.DefaultCompression)
	h.uploadCaptures(t, f, manifestOf(t, "aaaaaaa", "main",
		capture{"/checkout", "de", main, []string{"checkout.pay", "nav.home", "checkout.pay"}},
		capture{"/checkout", "en", mobile, []string{"nav.home"}}), partsOf(main, mobile))
	h.uploadCaptures(t, f, manifestOf(t, "bbbbbbb", "feat/copy",
		capture{"/checkout", "de", branch, []string{"hidden:checkout.pay"}}), partsOf(branch))

	ctx := h.translator()
	got, err := h.svc.CapturesOfKey(ctx, f.project, "checkout.pay", app.UsageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got.MessageID != f.ids["checkout.pay"] || got.Truncated || len(got.Captures) != 1 {
		t.Fatalf("default view = %+v", got)
	}
	c := got.Captures[0]
	if !c.OnDefaultBranch || c.Route != "/checkout" || c.Locale.String() != "de" || len(c.Regions) != 2 ||
		c.Regions[0].Kind != domain.RegionElement || c.Regions[1].Kind != domain.RegionElement || !c.Regions[0].Visible {
		t.Errorf("capture = %+v", c)
	}
	// The branch view uses the branch's capture build.
	onBranch, err := h.svc.CapturesOfKey(ctx, f.project, "checkout.pay", app.UsageQuery{Branch: "feat/copy"})
	if err != nil || len(onBranch.Captures) != 1 || onBranch.Captures[0].OnDefaultBranch || onBranch.Captures[0].Regions[0].Visible {
		t.Errorf("branch view = %+v, %v", onBranch, err)
	}
	home, err := h.svc.CapturesOfKey(ctx, f.project, "nav.home", app.UsageQuery{Limit: 1})
	if err != nil || len(home.Captures) != 1 || !home.Truncated || home.Captures[0].Locale.String() != "de" {
		t.Errorf("nav.home limit 1 = %+v, %v", home, err)
	}
	if _, err := h.svc.CapturesOfKey(ctx, f.project, "no.such", app.UsageQuery{}); !errors.Is(err, app.ErrMessageNotFound) {
		t.Errorf("unknown key: err = %v", err)
	}

	img, err := h.svc.CaptureImage(ctx, f.project, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := img.Open()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r)
	_ = r.Close()
	if hexDigest(body) != img.Digest.String() || img.Width != 40 || img.Height != 30 {
		t.Errorf("image = %+v, body digest %s", img.Image, hexDigest(body))
	}
	if _, err := h.svc.CaptureImage(ctx, g.project, c.ID); !errors.Is(err, app.ErrCaptureNotFound) {
		t.Errorf("another project's capture: err = %v", err)
	}
	if _, err := h.svc.CaptureImage(ctx, f.project, uuid.New()); !errors.Is(err, app.ErrCaptureNotFound) {
		t.Errorf("unknown capture: err = %v", err)
	}
	other := harnessFor(t, "bolt")
	if _, err := other.svc.CaptureImage(other.translator(), f.project, c.ID); err == nil {
		t.Error("another tenant read the image")
	}
}

func TestRetentionDeletesImagesNoCaptureReferences(t *testing.T) {
	h, objects := withImages(t)
	f := h.project(t, "shop", []string{"web", "api"}, "checkout.pay")
	var images [][]byte
	for i := range 5 {
		img := pngOf(t, 8, 8, uint8(i), png.DefaultCompression) //nolint:gosec // small
		if i == 4 {
			img = images[1] // the newest build shares the second's pixels
		}
		images = append(images, img)
		h.uploadCaptures(t, f, manifestOf(t, "abc000"+string(rune('0'+i)), "main",
			capture{"/", "de", img, []string{"checkout.pay"}}), partsOf(img))
	}
	got, err := h.svc.PurgeProject(h.developer(), f.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Builds) != 2 || len(got.OrphanedImages) != 1 {
		t.Fatalf("purged = %+v", got)
	}
	if objectCount(t, objects, h, f, images[0]) != 0 || objectCount(t, objects, h, f, images[1:4]...) != 3 {
		t.Error("the purge deleted the wrong images")
	}

	// A deleted application takes the images only it referenced.
	apiImg := pngOf(t, 8, 8, 77, png.DefaultCompression)
	apiManifest := bytes.Replace(manifestOf(t, "abcdef9", "main", capture{"/", "de", apiImg, nil}),
		[]byte(`"application":"web"`), []byte(`"application":"api"`), 1)
	h.uploadCaptures(t, f, apiManifest, partsOf(apiImg))
	if err := h.catalog.DeleteApplication(h.developer(), catalogdomain.ProjectID(f.project), catalogdomain.ApplicationID(f.apps["api"]), nil); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	if objectCount(t, objects, h, f, apiImg) != 0 || objectCount(t, objects, h, f, images[1:4]...) != 3 {
		t.Error("deleting the api application left its image or took another")
	}

	// A deleted project takes every image.
	admin := authztest.Member(context.Background(), h.tenant, []string{"admin"})
	if err := h.catalog.DeleteProject(admin, catalogdomain.ProjectID(f.project), nil); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	if n := objectCount(t, objects, h, f, images...); n != 0 {
		t.Errorf("images left after deleting the project = %d", n)
	}
}

func TestCaptureUploadsRecordMetricsAndCoverage(t *testing.T) {
	m := &metrics{coverage: map[uuid.UUID][2]int{}}
	h, _ := withImages(t, app.WithMetrics(m))
	f := h.project(t, "shop", []string{"web"}, "a.one", "b.two", "c.three", "d.four")
	img := pngOf(t, 8, 8, 1, png.DefaultCompression)
	manifest := manifestOf(t, "aaaaaaa", "main", capture{"/", "de", img, []string{"a.one", "b.two", "hidden:c.three"}})
	h.uploadCaptures(t, f, manifest, partsOf(img))
	h.uploadCaptures(t, f, manifest, partsOf(img))
	h.drain(t)
	if want := []string{"capture:new:0/0", "capture:replay:0/0"}; !slices.Equal(m.ingested, want) {
		t.Errorf("builds = %v, want %v", m.ingested, want)
	}
	if want := []string{"1 captures, 3 regions, 1 stored, 0 deduplicated, bytes true"}; !slices.Equal(m.captures, want) {
		t.Errorf("captures = %v, want %v", m.captures, want)
	}
	// 2 of 4 active messages have a visible region; c.three's is hidden.
	if got := m.captured[f.project]; got != [2]int{4, 2} {
		t.Errorf("capture coverage = %v, want 4 active, 2 captured", got)
	}
}
