//go:build integration

package app_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/edge"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// pushBranch pushes items (key → source) from branch, without a pull
// request: its environment is br-<hash>.
func (h *harness) pushBranch(t *testing.T, project uuid.UUID, branch string, items map[string]string) {
	t.Helper()
	h.pushBranchPR(t, project, branch, 0, items)
}

// pushBranchPR pushes from a branch with a pull request number, the way
// CI does (glossa push --branch --pr): its environment is pr-<pr>.
func (h *harness) pushBranchPR(t *testing.T, project uuid.UUID, branch string, pr int, items map[string]string) {
	t.Helper()
	var push []catalogapp.UpsertItem
	for k, v := range items {
		push = append(push, catalogapp.UpsertItem{Key: k, Text: v})
	}
	in := catalogapp.BranchPush{Branch: branch, Items: push}
	if pr > 0 {
		in.PushInfo = catalogdomain.PushInfo{PR: &pr}
	}
	if _, err := h.catalog.PushBranch(h.owner(), catalogdomain.ProjectID(project), in); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
}

// RFC 0004 §4.2: a branch environment's release is the main catalog plus
// that branch's overlay only — its proposed messages, translated or not,
// and its source proposals in the source locale. It can't be promoted,
// and its policy is fixed.
func TestBranchEnvironmentServesItsOverlayOnly(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, []string{"de"}, shop)
	h.translate(t, p, "checkout.pay", "de", "{amount, number} bezahlen", "approved")
	h.pushBranchPR(t, p, "feature/tip", 42, map[string]string{"checkout.pay": "Pay securely {amount, number}", "checkout.tip": "Add a tip"})
	h.pushBranch(t, p, "feature/other", map[string]string{"checkout.other": "Another branch", "home.title": "Hello"})
	h.translate(t, p, "checkout.tip", "de", "Trinkgeld geben", "draft")

	// The push's event opened the environment; opening it again returns
	// that one (created is false).
	ctx := h.as("developer")
	env, created, err := h.svc.OpenBranchEnvironment(ctx, p, "feature/tip", 42)
	if err != nil || created || env.Name != "pr-42" || env.Kind != domain.KindBranch || env.Branch != "feature/tip" ||
		!env.Policy.Equal(domain.BranchPolicy()) {
		t.Fatalf("open: %+v, %v", env, err)
	}
	rel, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "pr-42"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Branch != "feature/tip" || rel.Stats.Messages != 4 {
		t.Errorf("release %+v", rel)
	}
	_, texts := h.served(t, p, "pr-42")
	if !strings.Contains(texts["en"]["checkout.tip"], "Add a tip") || !strings.Contains(texts["en"]["checkout.pay"], "securely") {
		t.Errorf("en lacks the branch's overlay: %v", texts["en"])
	}
	if !strings.Contains(texts["de"]["checkout.tip"], "Trinkgeld") || !strings.Contains(texts["de"]["checkout.pay"], "bezahlen") {
		t.Errorf("de: %v", texts["de"])
	}
	for locale, msgs := range texts {
		if _, ok := msgs["checkout.other"]; ok || strings.Contains(msgs["home.title"], "Hello") {
			t.Errorf("%s ships another branch's overlay: %v", locale, msgs)
		}
	}
	// A preview of the next publish builds the same way.
	if pv, err := h.svc.PreviewPublish(ctx, p, "pr-42"); err != nil || pv.Digest != rel.Digest {
		t.Errorf("preview: %v, digest %s, want %s", err, pv.Digest, rel.Digest)
	}
	stored, err := h.svc.GetRelease(ctx, p, rel.ID)
	if err != nil || stored.Branch != "feature/tip" {
		t.Errorf("stored release: %+v, %v", stored, err)
	}

	for _, target := range []string{"staging", "production", "preview", "development"} {
		if _, err := h.svc.Promote(ctx, p, target, rel.ID, app.PromoteInput{}); !errors.Is(err, domain.ErrBranchReleaseNotPromotable) {
			t.Errorf("promote to %s: %v", target, err)
		}
	}
	env, _ = h.svc.GetEnvironment(ctx, p, "pr-42")
	if _, err := h.svc.UpdateEnvironment(ctx, p, "pr-42", env.Version, domain.DefaultPolicy("production")); !errors.Is(err, domain.ErrFixedPolicy) {
		t.Errorf("policy change: %v", err)
	}
	if _, err := h.svc.CreateEnvironment(ctx, p, "pr-43", nil); !errors.Is(err, domain.ErrInvalidEnvironment) {
		t.Errorf("a standard environment with a branch name: %v", err)
	}
	// Every other environment keeps excluding the overlay.
	main, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "preview"}, "")
	if err != nil || main.Branch != "" || main.Stats.Messages != 3 {
		t.Fatalf("preview: %+v, %v", main, err)
	}
	if _, err := h.svc.Promote(ctx, p, "development", main.ID, app.PromoteInput{}); err != nil {
		t.Errorf("main releases still promote: %v", err)
	}
}

func TestOpenBranchEnvironment(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	ctx := h.as("developer")
	first, _, err := h.svc.OpenBranchEnvironment(ctx, p, "feature/a", 7)
	if err != nil {
		t.Fatal(err)
	}
	again, created, err := h.svc.OpenBranchEnvironment(ctx, p, "feature/a", 7)
	if err != nil || created || again.Name != first.Name || again.Version != first.Version {
		t.Errorf("open again: %+v %v %v", again, created, err)
	}
	if _, _, err := h.svc.OpenBranchEnvironment(ctx, p, "feature/b", 7); !errors.Is(err, app.ErrEnvironmentExists) {
		t.Errorf("another branch with the same PR: %v", err)
	}
	noPR, _, err := h.svc.OpenBranchEnvironment(ctx, p, "spike", 0)
	if err != nil || noPR.Name != delivery.BranchEnvironmentName("spike", 0) {
		t.Errorf("without a PR: %+v %v", noPR, err)
	}
	if _, _, err := h.svc.OpenBranchEnvironment(h.as("translator"), p, "x", 0); err == nil {
		t.Error("a translator opened a branch environment")
	}
	envs, _, err := h.svc.ListEnvironments(ctx, p, firstPage())
	if err != nil || len(envs) != 6 {
		t.Errorf("environments %d, %v", len(envs), err)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'release.environment.created' AND payload->>'kind' = 'branch'"); n != 2 {
		t.Errorf("%d created events for branch environments", n)
	}
}

func TestTooManyBranches(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	ctx := h.as("developer")
	for i := 1; i <= domain.MaxBranchEnvironments; i++ {
		if _, _, err := h.svc.OpenBranchEnvironment(ctx, p, fmt.Sprintf("feature/%d", i), i); err != nil {
			t.Fatalf("branch %d: %v", i, err)
		}
	}
	if _, _, err := h.svc.OpenBranchEnvironment(ctx, p, "feature/51", 51); !errors.Is(err, domain.ErrTooManyBranches) {
		t.Fatalf("51st branch: %v", err)
	}
	// An open branch reopening its environment isn't a new one.
	if _, _, err := h.svc.OpenBranchEnvironment(ctx, p, "feature/1", 1); err != nil {
		t.Errorf("reopen: %v", err)
	}
	if err := h.svc.DestroyBranchEnvironment(ctx, p, "feature/1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.OpenBranchEnvironment(ctx, p, "feature/51", 51); err != nil {
		t.Errorf("after one was destroyed: %v", err)
	}
}

// Destroying a branch environment removes its manifest (the edge answers
// 404); its releases and deployments stay as history.
func TestDestroyBranchEnvironment(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	h.pushBranchPR(t, p, "feature/tip", 42, map[string]string{"checkout.tip": "Add a tip"})
	ctx := h.as("developer")
	if _, _, err := h.svc.OpenBranchEnvironment(ctx, p, "feature/tip", 42); err != nil {
		t.Fatal(err)
	}
	rel, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "pr-42"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RequestBranchPublish(ctx, p, "feature/tip"); err != nil {
		t.Fatal(err)
	}
	manifest := delivery.ManifestPath(p.String(), "pr-42")
	if ok, _ := h.objects.Exists(context.Background(), manifest); !ok {
		t.Fatal("no manifest")
	}
	if err := h.svc.DestroyBranchEnvironment(ctx, p, "feature/tip"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := h.objects.Exists(context.Background(), manifest); ok {
		t.Error("destroyed environment's manifest still served")
	}
	if _, err := h.svc.GetEnvironment(ctx, p, "pr-42"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("destroyed environment: %v", err)
	}
	if _, err := h.svc.GetRelease(ctx, p, rel.ID); err != nil {
		t.Errorf("its release: %v", err)
	}
	if n := count(t, "SELECT count(*) FROM release_deployments WHERE project_id = $1 AND environment = 'pr-42'", p); n != 1 {
		t.Errorf("deployments: %d", n)
	}
	if n := count(t, "SELECT count(*) FROM release_publish_requests"); n != 0 {
		t.Errorf("pending publish survived: %d", n)
	}
	if err := h.svc.DestroyBranchEnvironment(ctx, p, "feature/tip"); err != nil {
		t.Errorf("destroy twice: %v", err)
	}
	if _, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "pr-42"}, ""); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("publish to a destroyed environment: %v", err)
	}
	// The outbox removes it again, and a lost removal is repaired.
	_ = h.objects.Put(context.Background(), manifest, []byte(`{}`), "application/json")
	h.drain(t)
	if ok, _ := h.objects.Exists(context.Background(), manifest); ok {
		t.Error("the outbox didn't remove the manifest")
	}
}

// Publish-on-push is debounced: a burst of requests is one publish, 30 s
// after the last, which the publisher runs once due on the service's
// clock.
func TestBranchPublishIsDebounced(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	h.pushBranchPR(t, p, "feature/tip", 42, map[string]string{"checkout.tip": "Add a tip"})
	ctx := h.as("developer")
	if _, _, err := h.svc.OpenBranchEnvironment(ctx, p, "feature/tip", 42); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RequestBranchPublish(ctx, p, "feature/unknown"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("a branch without an environment: %v", err)
	}
	publisher := app.NewPublisher(h.svc, h.scanner, time.Second)
	runOnce := func(want int) {
		t.Helper()
		n, err := publisher.RunOnce(context.Background())
		if err != nil || n != want {
			t.Fatalf("published %d, want %d: %v", n, want, err)
		}
	}
	first, err := h.svc.RequestBranchPublish(ctx, p, "feature/tip")
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(10 * time.Second)
	runOnce(0)
	h.clock.Advance(10 * time.Second)
	second, err := h.svc.RequestBranchPublish(ctx, p, "feature/tip")
	if err != nil || second.ID == first.ID || !second.NotBefore.After(first.NotBefore) {
		t.Fatalf("renewed: %+v %v", second, err)
	}
	h.clock.Advance(25 * time.Second) // 45 s after the first, 25 s after the last
	runOnce(0)
	h.clock.Advance(5 * time.Second)
	runOnce(1)
	runOnce(0)
	env, err := h.svc.GetEnvironment(ctx, p, "pr-42")
	if err != nil || env.Current == uuid.Nil {
		t.Fatalf("not published: %+v %v", env, err)
	}
	rel, _ := h.svc.GetRelease(ctx, p, env.Current)
	if rel.Branch != "feature/tip" || !strings.HasPrefix(rel.Author, "system:") || rel.Note == "" {
		t.Errorf("release %+v", rel)
	}
	_, texts := h.served(t, p, "pr-42")
	if _, ok := texts["en"]["checkout.tip"]; !ok {
		t.Errorf("served %v", texts["en"])
	}
	// Two from the push (the branch opened, then pushed) and the two
	// this test made.
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'release.environment.publish_requested'"); n != 4 {
		t.Errorf("%d publish_requested events", n)
	}
}

// A publish request is published once, however often it runs: its ID is
// the publish's idempotency key.
func TestPublishDueIsIdempotent(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	ctx := h.as("developer")
	if _, _, err := h.svc.OpenBranchEnvironment(ctx, p, "feature/tip", 42); err != nil {
		t.Fatal(err)
	}
	r, err := h.svc.RequestBranchPublish(ctx, p, "feature/tip")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := h.svc.PublishDue(ctx, p, "pr-42"); err != nil || ok {
		t.Fatalf("not due yet: %v %v", ok, err)
	}
	h.clock.Advance(domain.PublishDebounce)
	// A publisher that published and crashed before clearing the request.
	if _, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "pr-42", Note: "Published automatically after changes on the branch."}, r.ID.String()); err != nil {
		t.Fatal(err)
	}
	if ok, err := h.svc.PublishDue(ctx, p, "pr-42"); err != nil || !ok {
		t.Fatalf("due: %v %v", ok, err)
	}
	if n := count(t, "SELECT count(*) FROM release_releases WHERE project_id = $1", p); n != 1 {
		t.Errorf("%d releases, want 1", n)
	}
	if ok, err := h.svc.PublishDue(ctx, p, "pr-42"); err != nil || ok {
		t.Errorf("request cleared: %v %v", ok, err)
	}
}

// End to end over storage: a production key can't read a branch
// environment at the edge, a preview key can, and a destroyed branch
// environment answers 404 to both.
func TestBranchEnvironmentsAtTheEdge(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	h.pushBranchPR(t, p, "feature/tip", 42, map[string]string{"checkout.tip": "Add a tip"})
	ctx := h.as("developer")
	if _, _, err := h.svc.OpenBranchEnvironment(ctx, p, "feature/tip", 42); err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"pr-42", "production"} {
		if _, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: env}, ""); err != nil {
			t.Fatal(err)
		}
	}
	prod, _, err := h.svc.CreateDeliveryKey(ctx, p, app.NewDeliveryKey{Name: "web"}, "")
	if err != nil {
		t.Fatal(err)
	}
	previewScope := delivery.Scope{Environments: []string{"preview"}, Branches: true}
	preview, _, err := h.svc.CreateDeliveryKey(ctx, p, app.NewDeliveryKey{Name: "previews", Scope: &previewScope}, "")
	if err != nil {
		t.Fatal(err)
	}
	e := edge.New(h.objects, edge.Config{CacheBytes: 1 << 20, KeyTTL: time.Nanosecond, ManifestTTL: time.Nanosecond},
		slog.New(slog.DiscardHandler), observability.NewRegistry())
	mux := http.NewServeMux()
	e.Routes(mux)
	status := func(key, env string) int {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/"+key+"/"+env+"/manifest.json", nil))
		return rec.Code
	}
	for _, c := range []struct {
		key, env string
		want     int
	}{
		{prod.Key, "production", 200}, {prod.Key, "pr-42", 404},
		{preview.Key, "pr-42", 200}, {preview.Key, "production", 404},
	} {
		if got := status(c.key, c.env); got != c.want {
			t.Errorf("%s key, %s: %d, want %d", map[string]string{prod.Key: "production", preview.Key: "preview"}[c.key], c.env, got, c.want)
		}
	}
	if err := h.svc.DestroyBranchEnvironment(ctx, p, "feature/tip"); err != nil {
		t.Fatal(err)
	}
	if got := status(preview.Key, "pr-42"); got != 404 {
		t.Errorf("destroyed branch environment: %d", got)
	}
}

// Migration 0015 gives existing keys the default environments; the key
// index task rewrites their index objects, which predate scopes, and
// only theirs, once.
func TestRewriteKeyIndexes(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	ctx := h.as("developer")
	var old []domain.DeliveryKey
	for _, name := range []string{"web", "go-emails", "revoked"} {
		k, _, err := h.svc.CreateDeliveryKey(ctx, p, app.NewDeliveryKey{Name: name}, "")
		if err != nil {
			t.Fatal(err)
		}
		old = append(old, k)
	}
	if err := h.svc.RevokeDeliveryKey(ctx, p, old[2].ID); err != nil {
		t.Fatal(err)
	}
	current, _, err := h.svc.CreateDeliveryKey(ctx, p, app.NewDeliveryKey{Name: "new"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// Before 0015: the rows had no scope, and their objects none either.
	for _, k := range old[:2] {
		if _, err := env.Super.Exec(context.Background(),
			"UPDATE release_delivery_keys SET index_version = 1, environments = '{development,preview,staging,production}' WHERE id = $1", k.ID); err != nil {
			t.Fatal(err)
		}
		legacy := `{"schema":"glossa.delivery-key/v1","project":"` + p.String() + `","key_id":"` + k.ID.String() + `"}`
		if err := h.objects.Put(context.Background(), delivery.KeyIndexPath(k.Key), []byte(legacy), "application/json"); err != nil {
			t.Fatal(err)
		}
	}
	unchanged, _ := h.objects.Get(context.Background(), delivery.KeyIndexPath(current.Key), delivery.MaxKeyIndexBytes)

	n, err := h.svc.RewriteKeyIndexes(context.Background(), h.scanner)
	if err != nil || n != 2 {
		t.Fatalf("rewrote %d: %v", n, err)
	}
	for _, k := range old[:2] {
		body, err := h.objects.Get(context.Background(), delivery.KeyIndexPath(k.Key), delivery.MaxKeyIndexBytes)
		if err != nil || !strings.Contains(string(body), `"environments":["development","preview","staging","production"]`) ||
			!strings.Contains(string(body), `"branches":false`) {
			t.Errorf("%s: %s %v", k.Name, body, err)
		}
	}
	if ok, _ := h.objects.Exists(context.Background(), delivery.KeyIndexPath(old[2].Key)); ok {
		t.Error("a revoked key's index was written")
	}
	if body, _ := h.objects.Get(context.Background(), delivery.KeyIndexPath(current.Key), delivery.MaxKeyIndexBytes); string(body) != string(unchanged) {
		t.Errorf("a current key's index changed: %s", body)
	}
	if n, err := h.svc.RewriteKeyIndexes(context.Background(), h.scanner); err != nil || n != 0 {
		t.Errorf("second run rewrote %d: %v", n, err)
	}
}
