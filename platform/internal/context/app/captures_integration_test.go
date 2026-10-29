//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

func shot(route, image string) domain.CaptureInput {
	return domain.CaptureInput{
		Route: route, Viewport: domain.Viewport{Width: 1280, Height: 800}, Locale: bcp47.MustParse("de"),
		Image: domain.Image{Digest: digest(image), Width: 1280, Height: 2000},
		Regions: []domain.Region{
			{Key: "checkout.pay", Kind: domain.RegionElement, Box: domain.Box{X: 10, Y: 20, Width: 100, Height: 30}, Visible: true},
			{Key: "checkout.help", Kind: domain.RegionAttribute, Box: domain.Box{X: 5, Y: 5, Width: 16, Height: 16}, Visible: true},
			{Key: "not.in.catalog", Kind: domain.RegionText, Box: domain.Box{X: -50, Y: 0}, Visible: false},
		},
	}
}

func TestIngestCaptureStoresRegionsAndPublishes(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay", "checkout.help")
	b := h.ingest(t, f, "capture", "web", "abcdef1", "main").Build

	got, err := h.svc.IngestCapture(h.ci(), app.IngestCapture{Project: f.project, Build: b.ID, Capture: shot("/checkout", "png-1")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Replayed || got.UnknownKeys != 1 || got.Capture.BuildID != b.ID || len(got.Capture.Regions) != 3 {
		t.Errorf("stored = %+v", got)
	}
	if n := count(t, "SELECT count(*) FROM context_regions WHERE capture_id = $1 AND message_id = $2 AND kind = 'element'",
		got.Capture.ID, f.ids["checkout.pay"]); n != 1 {
		t.Errorf("resolved element regions = %d", n)
	}
	if n := count(t, "SELECT count(*) FROM context_regions WHERE capture_id = $1 AND message_id IS NULL AND NOT visible",
		got.Capture.ID); n != 1 {
		t.Errorf("unknown invisible regions = %d", n)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'context.capture.ingested' AND aggregate_id = $1",
		got.Capture.ID.String()); n != 1 {
		t.Errorf("capture events = %d", n)
	}

	again, err := h.svc.IngestCapture(h.ci(), app.IngestCapture{Project: f.project, Build: b.ID, Capture: shot("/checkout", "png-1")})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Replayed || again.Capture.ID != got.Capture.ID || again.UnknownKeys != 1 || len(again.Capture.Regions) != 3 {
		t.Errorf("repeat = %+v", again)
	}
	_, err = h.svc.IngestCapture(h.ci(), app.IngestCapture{Project: f.project, Build: b.ID, Capture: shot("/checkout", "png-2")})
	if !errors.Is(err, app.ErrCaptureConflict) {
		t.Errorf("different pixels for the same shot: err = %v, want ErrCaptureConflict", err)
	}
	if n := count(t, "SELECT count(*) FROM context_captures"); n != 1 {
		t.Errorf("captures = %d, want 1", n)
	}
	h.drain(t)
}

func TestIngestCaptureRefusals(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	g := h.project(t, "blog", []string{"web"})
	b := h.ingest(t, f, "capture", "web", "abcdef1", "main").Build

	cases := map[string]struct {
		ctx  context.Context
		in   app.IngestCapture
		want error
	}{
		"translator":      {h.translator(), app.IngestCapture{Project: f.project, Build: b.ID, Capture: shot("/a", "x")}, authz.ErrForbidden},
		"unknown build":   {h.ci(), app.IngestCapture{Project: f.project, Build: uuid.New(), Capture: shot("/a", "x")}, app.ErrBuildNotFound},
		"another project": {h.ci(), app.IngestCapture{Project: g.project, Build: b.ID, Capture: shot("/a", "x")}, app.ErrBuildNotFound},
		"invalid capture": {h.ci(), app.IngestCapture{Project: f.project, Build: b.ID, Capture: domain.CaptureInput{}}, domain.ErrInvalidCapture},
		"unknown project": {h.ci(), app.IngestCapture{Project: uuid.New(), Build: b.ID, Capture: shot("/a", "x")}, app.ErrProjectNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := h.svc.IngestCapture(tc.ctx, tc.in); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestIngestCaptureHoldsTheBuildLimit(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	b := h.ingest(t, f, "capture", "web", "abcdef1", "main").Build
	// 499 captures already, written past the service for speed.
	if _, err := env.Super.Exec(context.Background(), `
		INSERT INTO context_captures (id, tenant_id, build_id, project_id, route, viewport_width, viewport_height, locale,
		                              image_digest, image_width, image_height, created_by, created_at)
		SELECT gen_random_uuid(), $1, $2, $3, '/r' || n, 1280, 800, 'de', repeat('a', 64), 10, 10, 'test', now()
		FROM generate_series(1, $4::int) AS n`,
		h.tenant.UUID(), b.ID, f.project, domain.MaxCapturesPerBuild-1); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.IngestCapture(h.ci(), app.IngestCapture{Project: f.project, Build: b.ID, Capture: shot("/last", "x")}); err != nil {
		t.Fatalf("the 500th capture: %v", err)
	}
	_, err := h.svc.IngestCapture(h.ci(), app.IngestCapture{Project: f.project, Build: b.ID, Capture: shot("/one-more", "y")})
	if !errors.Is(err, domain.ErrTooManyCaptures) {
		t.Errorf("the 501st capture: err = %v, want ErrTooManyCaptures", err)
	}
}
