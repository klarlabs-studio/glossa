//go:build integration

package app_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// footprint is everything a publish can write: Release's tables, the
// outbox and object storage.
type footprint struct {
	environments, releases, deployments, events int
	objects                                     []string
}

func (h *harness) footprint(t *testing.T) footprint {
	t.Helper()
	return footprint{
		environments: count(t, "SELECT count(*) FROM release_environments"),
		releases:     count(t, "SELECT count(*) FROM release_releases"),
		deployments:  count(t, "SELECT count(*) FROM release_deployments"),
		events:       count(t, "SELECT count(*) FROM outbox_events"),
		objects:      h.objects.Keys(),
	}
}

func (f footprint) equal(o footprint) bool {
	return f.environments == o.environments && f.releases == o.releases && f.deployments == o.deployments &&
		f.events == o.events && slices.Equal(f.objects, o.objects)
}

func byLocale(ds []domain.LocaleDiff) map[string]domain.LocaleDiff {
	out := map[string]domain.LocaleDiff{}
	for _, d := range ds {
		out[d.Locale] = d
	}
	return out
}

func TestPreviewPublishWritesNothing(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, []string{"de"}, shop)
	h.translate(t, p, "checkout.pay", "de", "{amount, number} bezahlen", "approved")
	h.translate(t, p, "home.title", "de", "Willkommen", "draft")
	ctx := h.as("developer")

	// A project that never used Release: the preview doesn't even
	// create its default environments.
	before := h.footprint(t)
	pv, err := h.svc.PreviewPublish(ctx, p, "production")
	if err != nil {
		t.Fatal(err)
	}
	if after := h.footprint(t); !after.equal(before) {
		t.Fatalf("preview wrote something: %+v → %+v", before, after)
	}
	if !pv.Releasable() || pv.Base.ID != uuid.Nil || !slices.Equal(pv.Environment.Policy.States, []string{"approved"}) {
		t.Fatalf("preview %+v", pv)
	}
	st := pv.Built.Stats
	if st.Messages != 3 || st.Locales["en"].Messages != 3 || st.Locales["de"].Messages != 1 || st.NewArtifacts != st.Artifacts {
		t.Errorf("stats %+v", st)
	}
	en := byLocale(pv.Changes)["en"]
	if len(en.Added) != 3 || len(en.Changed)+len(en.Removed) != 0 {
		t.Errorf("first preview changes %+v", pv.Changes)
	}

	// The publish that follows builds exactly what the preview showed.
	first, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != pv.Digest {
		t.Errorf("publish digest %s, preview said %s", first.Digest, pv.Digest)
	}

	// Against the release production serves: only what changed.
	h.translate(t, p, "home.title", "de", "Willkommen", "approved")
	h.translate(t, p, "checkout.pay", "de", "Jetzt {amount, number} zahlen", "approved")
	before = h.footprint(t)
	pv, err = h.svc.PreviewPublish(ctx, p, "production")
	if err != nil {
		t.Fatal(err)
	}
	if after := h.footprint(t); !after.equal(before) {
		t.Fatalf("second preview wrote something: %+v → %+v", before, after)
	}
	de := byLocale(pv.Changes)["de"]
	if pv.Base.ID != first.ID || !slices.Equal(de.Added, []string{"home.title"}) || !slices.Equal(de.Changed, []string{"checkout.pay"}) ||
		!byLocale(pv.Changes)["en"].Empty() {
		t.Errorf("changes against v1: base %v %+v", pv.Base.ID, pv.Changes)
	}
	if pv.Built.Stats.NewArtifacts != 1 || pv.Built.Stats.Locales["de"].Messages != 2 {
		t.Errorf("stats %+v: only de/default is new", pv.Built.Stats)
	}

	// An unchanged catalog previews as no change at all.
	second, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, "")
	if err != nil {
		t.Fatal(err)
	}
	pv, err = h.svc.PreviewPublish(ctx, p, "production")
	if err != nil || pv.Digest != second.Digest || pv.Built.Stats.NewArtifacts != 0 {
		t.Fatalf("unchanged: %v %+v", err, pv)
	}
	for _, d := range pv.Changes {
		if !d.Empty() {
			t.Errorf("unchanged catalog previews changes: %+v", d)
		}
	}
}

func TestPreviewPublishReportsProblems(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, []string{"de"}, shop)
	// Catalog allows a 64-character namespace; artifacts don't.
	long := strings.Repeat("n", 64)
	if _, err := h.catalog.UpsertMessages(h.owner(), catalogdomain.ProjectID(p), []catalogapp.UpsertItem{
		{Key: "legal.terms", Namespace: &long, Text: "Terms"},
		{Key: "legal.privacy", Namespace: &long, Text: "Privacy"},
	}); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	ctx := h.as("developer")
	before := h.footprint(t)
	pv, err := h.svc.PreviewPublish(ctx, p, "development")
	if err != nil {
		t.Fatal(err)
	}
	if pv.Releasable() || len(pv.Problems) != 2 || pv.Problems[0].Key != "legal.privacy" || pv.Problems[1].Key != "legal.terms" {
		t.Fatalf("problems %+v", pv.Problems)
	}
	if after := h.footprint(t); !after.equal(before) {
		t.Fatalf("preview wrote something: %+v → %+v", before, after)
	}
	// Publish refuses the same catalog with the same problems.
	_, _, err = h.svc.Publish(ctx, p, app.PublishInput{Environment: "development"}, "")
	var nr *domain.NotReleasableError
	if !errors.As(err, &nr) || len(nr.Problems) != 2 {
		t.Errorf("publish: %v", err)
	}
}

func TestPreviewPublishAccess(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, nil, shop)
	reader := authztest.Token(context.Background(), h.tenant, "read")
	if _, err := h.svc.PreviewPublish(reader, p, "staging"); err != nil {
		t.Errorf("a read token can't preview: %v", err)
	}
	if _, err := h.svc.PreviewPublish(h.as("developer"), p, "nope"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown environment: %v", err)
	}
	if _, err := h.svc.PreviewPublish(h.as("developer"), p, "A"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("invalid environment name: %v", err)
	}
	if _, err := h.svc.PreviewPublish(h.as("developer"), uuid.New(), "production"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown project: %v", err)
	}
	if _, err := h.svc.PreviewPublish(context.Background(), p, "production"); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("anonymous: %v", err)
	}
	// A custom environment previews under its own policy.
	if _, err := h.svc.CreateEnvironment(h.as("developer"), p, "qa-7", &domain.Policy{States: []string{"approved"}}); err != nil {
		t.Fatal(err)
	}
	pv, err := h.svc.PreviewPublish(h.as("developer"), p, "qa-7")
	if err != nil || pv.Environment.Policy.IncludeOutdated {
		t.Errorf("custom: %v %+v", err, pv.Environment)
	}
}

func TestPromoteRefusalExplainsThePolicies(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, []string{"de"}, shop)
	ctx := h.as("developer")
	dev, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "development"}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.Promote(ctx, p, "production", dev.ID, app.PromoteInput{})
	var ie *domain.IneligibleError
	if !errors.As(err, &ie) || ie.From != "development" || ie.Version != dev.Version || !strings.Contains(err.Error(), "staging") {
		t.Fatalf("promote dev → production: %v", err)
	}
	// The documented path: publish to staging, promote to production.
	stg, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "staging"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if prod, err := h.svc.Promote(ctx, p, "production", stg.ID, app.PromoteInput{}); err != nil || prod.Current != stg.ID {
		t.Fatalf("promote staging → production: %v", err)
	}
	if m, _ := h.served(t, p, "production"); m.Release.ID != stg.ID.String() {
		t.Errorf("production serves %s", m.Release.ID)
	}
}
