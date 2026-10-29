//go:build integration

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// The branch environment lifecycle over the outbox (RFC 0004 §4.2):
// Catalog's branch events open, publish and destroy the environment,
// and a translation revised on a branch's message publishes its
// preview again.

func (h *harness) closeBranch(t *testing.T, project uuid.UUID, branch string) {
	t.Helper()
	if _, err := h.catalog.CloseBranch(h.owner(), catalogdomain.ProjectID(project), branch); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
}

func TestBranchEventsRunTheEnvironmentLifecycle(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, []string{"de"}, shop)
	pr := 42
	if _, _, err := h.catalog.UpsertBranch(h.owner(), catalogdomain.ProjectID(p), catalogapp.BranchUpsert{
		Branch: "feature/tip", PushInfo: catalogdomain.PushInfo{PR: &pr},
	}); err != nil {
		t.Fatal(err)
	}
	h.drain(t)

	ctx := h.as("developer")
	env, err := h.svc.GetEnvironment(ctx, p, "pr-42")
	if err != nil || env.Kind != domain.KindBranch || env.Branch != "feature/tip" {
		t.Fatalf("opened by the event: %+v %v", env, err)
	}
	if n := count(t, "SELECT count(*) FROM release_publish_requests WHERE environment = 'pr-42'"); n != 1 {
		t.Errorf("publish requests after opening: %d, want 1", n)
	}

	// A push asks again (debounced, so still one pending request), and a
	// translation of one of the branch's messages does too.
	h.pushBranch(t, p, "feature/tip", map[string]string{"checkout.tip": "Add a tip"})
	h.translate(t, p, "checkout.tip", "de", "Trinkgeld geben", "draft")
	h.drain(t)
	if n := count(t, "SELECT count(*) FROM release_publish_requests WHERE environment = 'pr-42'"); n != 1 {
		t.Errorf("publish requests after the push: %d, want 1", n)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'release.environment.publish_requested'"); n < 3 {
		t.Errorf("%d publish_requested events, want one per change", n)
	}

	// The publisher publishes the branch's overlay once the debounce is over.
	h.clock.Advance(domain.PublishDebounce)
	publisher := app.NewPublisher(h.svc, h.scanner, time.Second)
	if n, err := publisher.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("publisher: %d %v", n, err)
	}
	h.drain(t)
	_, texts := h.served(t, p, "pr-42")
	if !strings.Contains(texts["de"]["checkout.tip"], "Trinkgeld") {
		t.Errorf("served de %v", texts["de"])
	}

	// Closing the branch destroys its environment and its manifest.
	h.closeBranch(t, p, "feature/tip")
	if _, err := h.svc.GetEnvironment(ctx, p, "pr-42"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("environment after close: %v", err)
	}
	if ok, _ := h.objects.Exists(context.Background(), delivery.ManifestPath(p.String(), "pr-42")); ok {
		t.Error("manifest of a closed branch still served")
	}

	// A push reopens the branch, and with it its environment.
	h.pushBranch(t, p, "feature/tip", map[string]string{"checkout.tip": "Add a tip"})
	if _, err := h.svc.GetEnvironment(ctx, p, "pr-42"); err != nil {
		t.Errorf("environment after the branch reopened: %v", err)
	}
}

// A translation of a message no open branch proposes changes no branch
// preview.
func TestTranslationOutsideABranchRequestsNoPublish(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, []string{"de"}, shop)
	h.pushBranch(t, p, "feature/tip", map[string]string{"checkout.tip": "Add a tip"})
	h.drain(t)
	if n := count(t, "SELECT count(*) FROM release_publish_requests"); n != 1 {
		t.Fatalf("publish requests: %d", n)
	}
	h.clock.Advance(domain.PublishDebounce)
	if n, err := app.NewPublisher(h.svc, h.scanner, time.Second).RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("publisher: %d %v", n, err)
	}
	h.drain(t)

	h.translate(t, p, "checkout.pay", "de", "{amount, number} bezahlen", "approved")
	h.drain(t)
	if n := count(t, "SELECT count(*) FROM release_publish_requests"); n != 0 {
		t.Errorf("a translation outside the branch asked for %d publishes", n)
	}
}

// A delivery key's scope can be changed: the index object follows, and
// the edge sees the new scope.
func TestChangeDeliveryKeyScope(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	ctx := h.as("developer")
	k, _, err := h.svc.CreateDeliveryKey(ctx, p, app.NewDeliveryKey{Name: "web"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !k.Scope.Equal(delivery.DefaultScope()) {
		t.Fatalf("a new key reads %+v, want production only", k.Scope)
	}
	scope := delivery.Scope{Environments: []string{"preview", "staging"}, Branches: true}
	changed, err := h.svc.ChangeDeliveryKeyScope(ctx, p, k.ID, scope)
	if err != nil || !changed.Scope.Equal(scope) || changed.Key != k.Key {
		t.Fatalf("change: %+v %v", changed, err)
	}
	body, err := h.objects.Get(context.Background(), delivery.KeyIndexPath(k.Key), delivery.MaxKeyIndexBytes)
	if err != nil || !strings.Contains(string(body), `"environments":["preview","staging"]`) ||
		!strings.Contains(string(body), `"branches":true`) {
		t.Errorf("index object: %s %v", body, err)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'release.delivery_key.scope_changed'"); n != 1 {
		t.Errorf("%d scope_changed events", n)
	}
	h.drain(t)

	// An invalid scope, a branch environment by name and a revoked key
	// are refused; an unchanged scope is a no-op.
	if _, err := h.svc.ChangeDeliveryKeyScope(ctx, p, k.ID, delivery.Scope{}); !errors.Is(err, delivery.ErrInvalidScope) {
		t.Errorf("empty scope: %v", err)
	}
	if _, err := h.svc.ChangeDeliveryKeyScope(ctx, p, k.ID, delivery.Scope{Environments: []string{"pr-7"}}); !errors.Is(err, delivery.ErrInvalidScope) {
		t.Errorf("a branch environment by name: %v", err)
	}
	if again, err := h.svc.ChangeDeliveryKeyScope(ctx, p, k.ID, scope); err != nil || !again.Scope.Equal(scope) {
		t.Errorf("unchanged: %+v %v", again, err)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'release.delivery_key.scope_changed'"); n != 1 {
		t.Errorf("an unchanged scope published an event")
	}
	if err := h.svc.RevokeDeliveryKey(ctx, p, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ChangeDeliveryKeyScope(ctx, p, k.ID, delivery.DefaultScope()); !errors.Is(err, domain.ErrKeyRevoked) {
		t.Errorf("revoked key: %v", err)
	}
	if _, err := h.svc.ChangeDeliveryKeyScope(h.as("translator"), p, k.ID, scope); err == nil {
		t.Error("a translator changed a key's scope")
	}
	h.drain(t)
}
