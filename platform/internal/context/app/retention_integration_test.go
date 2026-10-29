//go:build integration

package app_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"

	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
)

func TestPurgeProjectKeepsTheLatestThreeAndReportsOrphanedImages(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	var builds []uuid.UUID
	for i := range 5 {
		b := h.ingest(t, f, "plugin", "web", fmt.Sprintf("%07x", 0xabc0000+i), "main", use{"checkout.pay", "a.vue", i + 1})
		builds = append(builds, b.Build.ID)
	}
	// The oldest build's capture has pixels of its own; the second's
	// share their image with a build that stays.
	for build, image := range map[uuid.UUID]string{builds[0]: "only-oldest", builds[1]: "shared", builds[3]: "shared"} {
		if _, err := h.svc.IngestCapture(h.ci(), app.IngestCapture{Project: f.project, Build: build,
			Capture: shot("/checkout", image)}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := h.svc.PurgeProject(h.developer(), f.project)
	if err != nil {
		t.Fatal(err)
	}
	if !sameUUIDs(got.Builds, builds[:2]) {
		t.Errorf("purged %v, want the two oldest %v", got.Builds, builds[:2])
	}
	if !slices.Equal(got.OrphanedImages, []domain.Digest{digest("only-oldest")}) {
		t.Errorf("orphaned images = %v", got.OrphanedImages)
	}
	if n := count(t, "SELECT count(*) FROM context_builds"); n != 3 {
		t.Errorf("builds left = %d, want 3", n)
	}
	if n := count(t, "SELECT count(*) FROM context_usages WHERE build_id = ANY($1)", builds[:2]); n != 0 {
		t.Errorf("usages of purged builds = %d", n)
	}
	if n := count(t, "SELECT count(*) FROM context_captures"); n != 1 {
		t.Errorf("captures left = %d, want 1", n)
	}
	if n := count(t, "SELECT count(*) FROM context_regions"); n != 3 {
		t.Errorf("regions left = %d, want 3", n)
	}

	again, err := h.svc.PurgeProject(h.developer(), f.project)
	if err != nil || len(again.Builds) != 0 {
		t.Errorf("second purge = %+v, %v", again, err)
	}
	if _, err := h.svc.PurgeProject(h.translator(), f.project); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator: err = %v", err)
	}
}

func sameUUIDs(a, b []uuid.UUID) bool {
	sorted := func(ids []uuid.UUID) []string {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = id.String()
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(sorted(a), sorted(b))
}

func TestPurgeRunsAcrossTenants(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "k")
	other := harnessFor(t, "bolt")
	g := other.project(t, "blog", []string{"web"}, "k")
	for i := range 4 {
		h.ingest(t, f, "extract", "web", fmt.Sprintf("%07x", 0xa000000+i), "main", use{"k", "a.go", 1})
		other.ingest(t, g, "extract", "web", fmt.Sprintf("%07x", 0xb000000+i), "main", use{"k", "b.go", 1})
	}

	got, err := h.svc.Purge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("purged projects = %+v, want both tenants' projects", got)
	}
	for _, p := range got {
		if len(p.Builds) != 1 {
			t.Errorf("project %s: purged %d builds, want 1", p.Project, len(p.Builds))
		}
	}
	if n := count(t, "SELECT count(*) FROM context_builds"); n != 6 {
		t.Errorf("builds left = %d, want 6", n)
	}
	// A request context carries a tenant; the sweep refuses it.
	if _, err := h.svc.Purge(h.developer()); err == nil {
		t.Error("Purge ran in a tenant's request context")
	}
}

func TestDeletingAProjectOrApplicationErasesItsContext(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web", "api"}, "checkout.pay")
	web := h.ingest(t, f, "plugin", "web", "abcdef1", "main", use{"checkout.pay", "a.vue", 1}).Build
	h.ingest(t, f, "extract", "api", "abcdef1", "main", use{"checkout.pay", "a.go", 1})
	if _, err := h.svc.IngestCapture(h.ci(), app.IngestCapture{Project: f.project, Build: web.ID, Capture: shot("/", "img")}); err != nil {
		t.Fatal(err)
	}

	ctx := h.developer()
	if err := h.catalog.DeleteApplication(ctx, catalogdomain.ProjectID(f.project), catalogdomain.ApplicationID(f.apps["web"]), nil); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	if n := count(t, "SELECT count(*) FROM context_builds WHERE application_id = $1", f.apps["web"]); n != 0 {
		t.Errorf("builds of the deleted application = %d", n)
	}
	if n := count(t, "SELECT count(*) FROM context_captures"); n != 0 {
		t.Errorf("captures of the deleted application = %d", n)
	}
	if n := count(t, "SELECT count(*) FROM context_builds"); n != 1 {
		t.Errorf("builds left = %d, want the api's", n)
	}

	admin := authztest.Member(context.Background(), h.tenant, []string{"admin"})
	if err := h.catalog.DeleteProject(admin, catalogdomain.ProjectID(f.project), nil); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	for _, table := range []string{"context_builds", "context_usages", "context_captures", "context_regions"} {
		if n := count(t, "SELECT count(*) FROM "+table); n != 0 {
			t.Errorf("%s: %d rows left after the project was deleted", table, n)
		}
	}
}
