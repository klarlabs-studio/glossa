//go:build integration

package app_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
	"go.klarlabs.de/glossa/platform/internal/release/releasetest"
)

var shop = map[string]string{
	"checkout.pay": "Pay {amount, number}",
	"cart.items":   "{count, plural, one {# item} other {# items}}",
	"home.title":   "Welcome",
}

func firstPage() pagination.Page { return pagination.Page{Size: pagination.DefaultPageSize} }

// served reads and verifies the manifest an environment serves, and the
// artifacts it names, the way a runtime would.
func (h *harness) served(t *testing.T, project uuid.UUID, environment string) (domain.Manifest, map[string]map[string]string) {
	t.Helper()
	ctx := context.Background()
	body, err := h.objects.Get(ctx, delivery.ManifestPath(project.String(), environment), delivery.MaxManifestBytes)
	if err != nil {
		t.Fatalf("manifest of %s: %v", environment, err)
	}
	releasetest.Manifest(t, body)
	var m domain.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	signed, err := jcs.Without(body, "signatures")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Signatures) != 1 {
		t.Fatalf("signatures %+v", m.Signatures)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(m.Signatures[0].Sig)
	if !ed25519.Verify(h.signingKey, signed, sig) {
		t.Fatal("manifest signature doesn't verify")
	}
	texts := map[string]map[string]string{}
	for locale, namespaces := range m.Artifacts {
		texts[locale] = map[string]string{}
		for _, ref := range namespaces {
			a, err := h.objects.Get(ctx, delivery.ArtifactPath(project.String(), ref.SHA256), delivery.MaxArtifactBytes)
			if err != nil || delivery.Digest(a) != ref.SHA256 || int64(len(a)) != ref.Size {
				t.Fatalf("artifact %s: %v", ref.SHA256, err)
			}
			releasetest.Artifact(t, a)
			msgs, _ := domain.ArtifactMessages(a)
			for id, model := range msgs {
				texts[locale][id] = string(model)
			}
		}
	}
	return m, texts
}

func TestPublishPromoteRollback(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, []string{"de", "ar"}, shop)
	h.translate(t, p, "checkout.pay", "de", "{amount, number} bezahlen", "approved")
	h.translate(t, p, "home.title", "de", "Willkommen", "draft")
	ctx := h.as("developer")

	// Production ships approved text only.
	prod1, replayed, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production", Note: "first"}, "pub-1")
	if err != nil || replayed {
		t.Fatalf("publish: %v replayed=%v", err, replayed)
	}
	if prod1.Version != 1 || prod1.Parent != uuid.Nil || prod1.Stats.Messages != 3 ||
		prod1.Stats.Locales["de"].Messages != 1 || prod1.Stats.NewArtifacts != prod1.Stats.Artifacts {
		t.Fatalf("release %+v", prod1)
	}
	m, texts := h.served(t, p, "production")
	if m.Release.ID != prod1.ID.String() || m.Environment != "production" || m.SourceLocale != "en" ||
		len(m.Locales) != 3 || m.Locales[1].Code != "ar" || m.Locales[1].Direction != "rtl" {
		t.Fatalf("manifest %+v", m)
	}
	if _, ok := texts["de"]["home.title"]; ok || !strings.Contains(texts["de"]["checkout.pay"], "bezahlen") || len(texts["en"]) != 3 {
		t.Errorf("production texts %v", texts)
	}

	// The replay returns the first release; the key can't be reused.
	again, replayed, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production", Note: "first"}, "pub-1")
	if err != nil || !replayed || again.ID != prod1.ID {
		t.Fatalf("replay: %v %v %v", err, replayed, again.ID)
	}
	if _, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "preview"}, "pub-1"); !errors.Is(err, app.ErrIdempotencyReuse) {
		t.Errorf("reused key: %v", err)
	}

	// Preview ships drafts too.
	prev, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "preview"}, "")
	if err != nil || prev.Version != 2 {
		t.Fatalf("preview: %v %+v", err, prev)
	}
	if _, texts := h.served(t, p, "preview"); !strings.Contains(texts["de"]["home.title"], "Willkommen") {
		t.Errorf("preview texts %v", texts["de"])
	}

	// Unchanged catalog: nothing to upload, same digest.
	prod2, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, "")
	if err != nil || prod2.Version != 3 || prod2.Stats.NewArtifacts != 0 || prod2.Digest != prod1.Digest || prod2.Parent != prod1.ID {
		t.Fatalf("unchanged publish: %v %+v", err, prod2)
	}

	// Promote: a preview release can't reach production; production's can reach staging.
	if _, err := h.svc.Promote(ctx, p, "production", prev.ID, app.PromoteInput{}); !errors.Is(err, domain.ErrIneligible) {
		t.Errorf("draft release promoted into production: %v", err)
	}
	staging, err := h.svc.Promote(ctx, p, "staging", prod1.ID, app.PromoteInput{})
	if err != nil || staging.Current != prod1.ID {
		t.Fatalf("promote: %v %+v", err, staging)
	}
	if m, _ := h.served(t, p, "staging"); m.Environment != "staging" || m.Release.ID != prod1.ID.String() {
		t.Errorf("staging manifest %+v", m)
	}
	if again, err := h.svc.Promote(ctx, p, "staging", prod1.ID, app.PromoteInput{}); err != nil || again.Version != staging.Version {
		t.Errorf("promoting the served release moved the pointer: %v %d→%d", err, staging.Version, again.Version)
	}

	// A new production release, then roll back twice: v4 → v3 → v1.
	h.translate(t, p, "home.title", "de", "Willkommen", "approved")
	prod3, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, "")
	// Its de artifact is byte-identical to preview's (same text, now approved), so
	// it is already in storage: dedupe spans environments.
	if err != nil || prod3.Version != 4 || prod3.Digest == prod1.Digest || prod3.Stats.NewArtifacts != 0 {
		t.Fatalf("publish 4: %v %+v", err, prod3)
	}
	back, err := h.svc.Rollback(ctx, p, "production", nil)
	if err != nil || back.Current != prod2.ID {
		t.Fatalf("rollback: %v current=%v want %v", err, back.Current, prod2.ID)
	}
	back, err = h.svc.Rollback(ctx, p, "production", nil)
	if err != nil || back.Current != prod1.ID {
		t.Fatalf("second rollback: %v current=%v", err, back.Current)
	}
	if _, err := h.svc.Rollback(ctx, p, "production", nil); !errors.Is(err, domain.ErrNoRollbackTarget) {
		t.Errorf("rollback past the first release: %v", err)
	}
	if m, _ := h.served(t, p, "production"); m.Release.ID != prod1.ID.String() {
		t.Errorf("served after rollback: %s", m.Release.ID)
	}
	// Rolling back to a release production never served is refused.
	if _, err := h.svc.Rollback(ctx, p, "production", &prev.ID); !errors.Is(err, domain.ErrNotInHistory) {
		t.Errorf("rollback to a foreign release: %v", err)
	}
	fwd, err := h.svc.Rollback(ctx, p, "production", &prod3.ID)
	if err != nil || fwd.Current != prod3.ID {
		t.Errorf("explicit rollback target: %v", err)
	}
	deps, _, err := h.svc.ListDeployments(ctx, p, "production", firstPage())
	if err != nil || len(deps) != 6 || deps[0].Action != domain.ActionRollback || deps[0].Previous != prod1.ID ||
		deps[len(deps)-1].Action != domain.ActionPublish {
		t.Errorf("history %v %+v", err, deps)
	}

	for typ, want := range map[string]int{"release.published": 4, "release.promoted": 1, "release.rolled_back": 3} {
		if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = $1", typ); n != want {
			t.Errorf("%d %s events, want %d", n, typ, want)
		}
	}
	list, next, err := h.svc.ListReleases(ctx, p, pagination.Page{Size: 3})
	if err != nil || len(list) != 3 || list[0].Version != 4 || next == nil {
		t.Errorf("list: %v %d %v", err, len(list), next)
	}
}

func TestDiff(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, []string{"de"}, shop)
	h.translate(t, p, "checkout.pay", "de", "{amount, number} bezahlen", "approved")
	ctx := h.as("developer")
	first, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, "")
	if err != nil {
		t.Fatal(err)
	}
	h.translate(t, p, "checkout.pay", "de", "Jetzt {amount, number} zahlen", "approved")
	h.translate(t, p, "home.title", "de", "Willkommen", "approved")
	h.push(t, p, map[string]string{"home.subtitle": "Fresh bread"})
	if _, err := h.catalog.ObsoleteMessage(h.owner(), catalogdomain.ProjectID(p), "cart.items", nil); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	second, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, "")
	if err != nil {
		t.Fatal(err)
	}
	d, err := h.svc.Diff(ctx, p, second.ID, nil)
	if err != nil || d.Base.ID != first.ID {
		t.Fatalf("diff: %v base %v", err, d.Base.ID)
	}
	byLocale := map[string]domain.LocaleDiff{}
	for _, l := range d.Locales {
		byLocale[l.Locale] = l
	}
	en, de := byLocale["en"], byLocale["de"]
	if !slices.Equal(en.Added, []string{"home.subtitle"}) || !slices.Equal(en.Removed, []string{"cart.items"}) || len(en.Changed) != 0 {
		t.Errorf("en %+v", en)
	}
	if !slices.Equal(de.Added, []string{"home.title"}) || !slices.Equal(de.Changed, []string{"checkout.pay"}) || len(de.Removed) != 0 {
		t.Errorf("de %+v", de)
	}
	// The first release has no parent: everything is added.
	d, err = h.svc.Diff(ctx, p, first.ID, nil)
	if err != nil || d.Base.ID != uuid.Nil || len(d.Locales) != 2 || len(d.Locales[0].Added) != 3 {
		t.Errorf("first diff: %v %+v", err, d.Locales)
	}
	if _, err := h.svc.Diff(ctx, p, second.ID, ptr(uuid.New())); !errors.Is(err, app.ErrReleaseNotInProject) {
		t.Errorf("unknown base: %v", err)
	}
}

func TestEnvironments(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	ctx := h.as("developer")
	envs, _, err := h.svc.ListEnvironments(ctx, p, firstPage())
	if err != nil || len(envs) != 4 {
		t.Fatalf("defaults: %v %+v", err, envs)
	}
	prod, err := h.svc.GetEnvironment(ctx, p, "production")
	if err != nil || !slices.Equal(prod.Policy.States, []string{"approved"}) || prod.Current != uuid.Nil {
		t.Fatalf("production %+v %v", prod, err)
	}
	if _, err := h.svc.GetEnvironment(ctx, p, "nope"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown environment: %v", err)
	}

	pr, err := h.svc.CreateEnvironment(ctx, p, "qa", nil)
	if err != nil || !slices.Equal(pr.Policy.States, []string{"draft", "needs_review", "approved"}) {
		t.Fatalf("custom: %v %+v", err, pr)
	}
	if _, err := h.svc.CreateEnvironment(ctx, p, "qa", nil); !errors.Is(err, app.ErrEnvironmentExists) {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := h.svc.CreateEnvironment(ctx, p, "a", nil); !errors.Is(err, domain.ErrInvalidEnvironment) {
		t.Errorf("reserved name: %v", err)
	}

	strict := domain.Policy{States: []string{"approved"}}
	if _, err := h.svc.UpdateEnvironment(ctx, p, "qa", 99, strict); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("stale If-Match: %v", err)
	}
	updated, err := h.svc.UpdateEnvironment(ctx, p, "qa", pr.Version, strict)
	if err != nil || updated.Version != 2 || updated.Policy.IncludeOutdated {
		t.Fatalf("update: %v %+v", err, updated)
	}
	if _, err := h.svc.UpdateEnvironment(ctx, p, "qa", 2, domain.Policy{States: []string{"rejected"}}); !errors.Is(err, domain.ErrInvalidPolicy) {
		t.Errorf("rejected in a policy: %v", err)
	}
	envs, _, _ = h.svc.ListEnvironments(ctx, p, firstPage())
	if len(envs) != 5 {
		t.Errorf("environments %d", len(envs))
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type LIKE 'release.environment.%'"); n != 2 {
		t.Errorf("%d environment events", n)
	}
}

func TestPermissions(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	translator := authztest.Member(context.Background(), h.tenant, []string{"translator"})
	if _, _, err := h.svc.Publish(translator, p, app.PublishInput{Environment: "production"}, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator published: %v", err)
	}
	if _, _, err := h.svc.ListEnvironments(translator, p, firstPage()); err != nil {
		t.Errorf("translator can't read environments: %v", err)
	}
	if _, _, err := h.svc.CreateDeliveryKey(translator, p, app.NewDeliveryKey{Name: "web"}, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator created a key: %v", err)
	}
	reader := authztest.Token(context.Background(), h.tenant, "read")
	if _, err := h.svc.Promote(reader, p, "production", uuid.New(), app.PromoteInput{}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("read token promoted: %v", err)
	}
	publisher := authztest.Token(context.Background(), h.tenant, "publish")
	if _, _, err := h.svc.Publish(publisher, p, app.PublishInput{Environment: "production"}, ""); err != nil {
		t.Errorf("publish token: %v", err)
	}
	if _, _, err := h.svc.Publish(h.as("developer"), uuid.New(), app.PublishInput{Environment: "production"}, ""); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown project: %v", err)
	}
}

func TestDeliveryKeys(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	ctx := h.as("developer")
	k, replayed, err := h.svc.CreateDeliveryKey(ctx, p, app.NewDeliveryKey{Name: "web"}, "k-1")
	if err != nil || replayed || !delivery.ValidKey(k.Key) || !k.Scope.Equal(delivery.DefaultScope()) {
		t.Fatalf("create: %v %+v", err, k)
	}
	if again, replayed, err := h.svc.CreateDeliveryKey(ctx, p, app.NewDeliveryKey{Name: "web"}, "k-1"); err != nil || !replayed || again.Key != k.Key {
		t.Errorf("replay: %v %v", err, replayed)
	}
	preview := delivery.Scope{Environments: []string{"preview"}, Branches: true}
	if _, _, err := h.svc.CreateDeliveryKey(ctx, p, app.NewDeliveryKey{Name: "web", Scope: &preview}, "k-1"); !errors.Is(err, app.ErrIdempotencyReuse) {
		t.Errorf("same Idempotency-Key, other scope: %v", err)
	}
	idx, err := h.objects.Get(context.Background(), delivery.KeyIndexPath(k.Key), delivery.MaxKeyIndexBytes)
	if err != nil {
		t.Fatal(err)
	}
	releasetest.KeyIndex(t, idx)
	// New keys read production only (RFC 0004 §4.3).
	if got, err := delivery.DecodeKeyIndex(idx); err != nil || got.Project != p.String() ||
		!slices.Equal(got.Environments, []string{"production"}) || got.Branches {
		t.Fatalf("index %s %v", idx, err)
	}
	pk, _, err := h.svc.CreateDeliveryKey(ctx, p, app.NewDeliveryKey{Name: "preview deployments", Scope: &preview}, "")
	if err != nil {
		t.Fatal(err)
	}
	listed, _, _ := h.svc.ListDeliveryKeys(ctx, p, firstPage())
	if i := slices.IndexFunc(listed, func(k domain.DeliveryKey) bool { return k.ID == pk.ID }); len(listed) != 2 || i < 0 || !listed[i].Scope.Equal(preview) {
		t.Errorf("stored scope: %+v", listed)
	}
	idx, _ = h.objects.Get(context.Background(), delivery.KeyIndexPath(pk.Key), delivery.MaxKeyIndexBytes)
	if got, err := delivery.DecodeKeyIndex(idx); err != nil || !got.Branches || !slices.Equal(got.Environments, []string{"preview"}) {
		t.Fatalf("preview key index %s %v", idx, err)
	}
	bad := delivery.Scope{Environments: []string{"pr-1"}}
	if _, _, err := h.svc.CreateDeliveryKey(ctx, p, app.NewDeliveryKey{Name: "x", Scope: &bad}, ""); !errors.Is(err, delivery.ErrInvalidScope) {
		t.Errorf("a branch environment by name: %v", err)
	}
	if err := h.svc.RevokeDeliveryKey(ctx, p, pk.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RevokeDeliveryKey(ctx, p, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.objects.Get(context.Background(), delivery.KeyIndexPath(k.Key), delivery.MaxKeyIndexBytes); !errors.Is(err, objectstore.ErrNotFound) {
		t.Errorf("revoked key still indexed: %v", err)
	}
	if err := h.svc.RevokeDeliveryKey(ctx, p, k.ID); !errors.Is(err, domain.ErrKeyRevoked) {
		t.Errorf("revoke twice: %v", err)
	}
	keys, _, err := h.svc.ListDeliveryKeys(ctx, p, firstPage())
	if err != nil || len(keys) != 2 || keys[0].Active() || keys[1].Active() {
		t.Errorf("list: %v %+v", err, keys)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type LIKE 'release.delivery_key.%' AND payload::text LIKE '%glossa_pk_%'"); n != 0 {
		t.Error("a key leaked into an event payload")
	}
}

// The outbox rewrites storage when the write right after a commit was
// lost, and retires a deleted project.
func TestOutboxKeepsStorageInStep(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	ctx := h.as("developer")
	if _, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, ""); err != nil {
		t.Fatal(err)
	}
	k, _, err := h.svc.CreateDeliveryKey(ctx, p, app.NewDeliveryKey{Name: "web"}, "")
	if err != nil {
		t.Fatal(err)
	}
	manifest := delivery.ManifestPath(p.String(), "production")
	_ = h.objects.Delete(context.Background(), manifest)
	_ = h.objects.Delete(context.Background(), delivery.KeyIndexPath(k.Key))
	h.drain(t)
	if ok, _ := h.objects.Exists(context.Background(), manifest); !ok {
		t.Error("manifest not rewritten by the outbox")
	}
	if ok, _ := h.objects.Exists(context.Background(), delivery.KeyIndexPath(k.Key)); !ok {
		t.Error("key index not rewritten by the outbox")
	}

	if err := h.catalog.DeleteProject(h.owner(), catalogdomain.ProjectID(p), nil); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	if ok, _ := h.objects.Exists(context.Background(), manifest); ok {
		t.Error("deleted project's manifest still served")
	}
	if ok, _ := h.objects.Exists(context.Background(), delivery.KeyIndexPath(k.Key)); ok {
		t.Error("deleted project's key still resolves")
	}
	if n := count(t, "SELECT count(*) FROM release_releases WHERE project_id = $1", p); n != 1 {
		t.Errorf("releases of a deleted project: %d (history is kept)", n)
	}
}

func TestReleasesAreImmutable(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	rel, _, err := h.svc.Publish(h.as("developer"), p, app.PublishInput{Environment: "production"}, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, stmt := range []string{
		"UPDATE release_releases SET note = 'changed' WHERE id = $1",
		"DELETE FROM release_releases WHERE id = $1",
	} {
		// The superuser: not even it gets past the trigger.
		if _, err := env.Super.Exec(ctx, stmt, rel.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Errorf("%s: %v", stmt, err)
		}
	}
	// Erasing the tenant cascades through the trigger.
	if _, err := env.Super.Exec(ctx, "DELETE FROM tenants WHERE id = $1", h.tenant.UUID()); err != nil {
		t.Fatalf("tenant erasure: %v", err)
	}
	if n := count(t, "SELECT count(*) FROM release_releases"); n != 0 {
		t.Errorf("%d releases survived their tenant", n)
	}
}
