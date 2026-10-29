//go:build integration

package app_test

import (
	"context"
	"errors"
	"image/png"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
)

// A screenshot is part of what translating a unit needs (RFC 0006
// §3.3): an assigned member sees the image of a capture that shows one
// of their units, and no other.
//
// The check read the capture's regions, and the store returns a capture
// without them, so it answered "shows nothing" for every capture: an
// assigned member saw no screenshot at all, their own units' included.
// This runs it on the real store, which the fake that hid it does not.
func TestAnAssignedMemberSeesTheScreenshotsOfTheirUnits(t *testing.T) {
	h, _ := withImages(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay", "nav.home")
	pay := pngOf(t, 64, 48, 1, png.BestSpeed)
	home := pngOf(t, 64, 48, 2, png.BestSpeed)
	h.uploadCaptures(t, f, manifestOf(t, "abcdef1", "main",
		capture{"/checkout", "de", pay, []string{"checkout.pay"}},
		capture{"/home", "de", home, []string{"nav.home"}}), partsOf(pay, home))

	owner := authztest.Member(context.Background(), h.tenant, []string{"owner"})
	captureOf := func(key string) app.CaptureView {
		t.Helper()
		page, err := h.svc.CapturesOfKey(owner, f.project, key, app.UsageQuery{})
		if err != nil || len(page.Captures) != 1 {
			t.Fatalf("captures of %s = %+v, %v", key, page, err)
		}
		return page.Captures[0]
	}
	payCapture, homeCapture := captureOf("checkout.pay"), captureOf("nav.home")

	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(context.Background(), h.tenant, cov, "de")
	cov.Assign(member, f.project, f.ids["checkout.pay"], "de")

	if img, err := h.svc.CaptureImage(vendor, f.project, payCapture.ID); err != nil || img.Image.Digest != payCapture.Image.Digest {
		t.Fatalf("the screenshot of their own unit: %+v, %v; want it", img.Image, err)
	}
	if _, err := h.svc.CaptureImage(vendor, f.project, homeCapture.ID); !errors.Is(err, app.ErrCaptureNotFound) {
		t.Fatalf("a screenshot showing only someone else's unit: err = %v, want not found", err)
	}
}
