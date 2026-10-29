//go:build integration

package app_test

import (
	"strings"
	"testing"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/release/app"
)

// RFC 0004 §4.2: environments other than a branch's build without the
// branch overlay. A branch's new keys (proposed messages, translated or
// not) and its source proposals reach no release until the default
// branch's push brings them in.
func TestReleasesExcludeTheBranchOverlay(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, []string{"de"}, shop)
	h.translate(t, p, "checkout.pay", "de", "{amount, number} bezahlen", "approved")
	if _, err := h.catalog.PushBranch(h.owner(), catalogdomain.ProjectID(p), catalogapp.BranchPush{
		Branch: "feature/tip", Items: []catalogapp.UpsertItem{
			{Key: "checkout.pay", Text: "Pay securely {amount, number}"},
			{Key: "checkout.tip", Text: "Add a tip"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	h.translate(t, p, "checkout.tip", "de", "Trinkgeld geben", "approved")
	h.drain(t)

	ctx := h.as("developer")
	for _, env := range []string{"production", "preview"} {
		r, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: env}, "")
		if err != nil {
			t.Fatalf("publish %s: %v", env, err)
		}
		if r.Stats.Messages != 3 {
			t.Errorf("%s ships %d messages, want the 3 live ones", env, r.Stats.Messages)
		}
		_, texts := h.served(t, p, env)
		for locale, msgs := range texts {
			if _, ok := msgs["checkout.tip"]; ok {
				t.Errorf("%s/%s ships the proposed message", env, locale)
			}
			if strings.Contains(msgs["checkout.pay"], "securely") {
				t.Errorf("%s/%s ships the source proposal", env, locale)
			}
		}
		if !strings.Contains(texts["de"]["checkout.pay"], "bezahlen") {
			t.Errorf("%s de lost the live translation: %v", env, texts["de"])
		}
	}

	// The default branch's push brings both in.
	h.push(t, p, map[string]string{"checkout.pay": "Pay securely {amount, number}", "checkout.tip": "Add a tip"})
	if _, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "preview"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, texts := h.served(t, p, "preview"); !strings.Contains(texts["en"]["checkout.pay"], "securely") ||
		!strings.Contains(texts["de"]["checkout.tip"], "Trinkgeld") {
		t.Errorf("after merge: %v", texts)
	}
}
