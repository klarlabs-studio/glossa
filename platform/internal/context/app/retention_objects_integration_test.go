//go:build integration

package app_test

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/context/adapters/imaging"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store/s3test"
)

// minioStore boots a disposable MinIO and returns a store on a fresh
// bucket: what the deployment actually writes capture images to.
func minioStore(t *testing.T) *s3store.Store {
	t.Helper()
	ctx := context.Background()
	env, err := s3test.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
	s, err := env.Store(ctx, "glossa-context-retention")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// normalizedDigest is the digest the server stores img under, after
// re-encoding it (RFC 0004 §3.3): nothing is stored as it came.
func normalizedDigest(t *testing.T, img []byte) domain.Digest {
	t.Helper()
	n, err := imaging.New(t.TempDir(), 0).Normalize(context.Background(), bytes.NewReader(img), domain.Digest(hexDigest(img)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = n.Close() }()
	return n.Image().Digest
}

func exists(t *testing.T, objects objectstore.Store, key string) bool {
	t.Helper()
	ok, err := objects.Exists(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// TestPurgeDeletesUnreferencedImagesFromObjectStorage runs retention
// against a real MinIO: the image only a purged build's capture showed
// is gone from the bucket, and the one a surviving build still shows is
// untouched (RFC 0004 §3.3).
func TestPurgeDeletesUnreferencedImagesFromObjectStorage(t *testing.T) {
	objects := minioStore(t)
	h := withObjects(t, objects)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay", "checkout.help")
	lonely := pngOf(t, 40, 30, 3, png.BestSpeed) // only the oldest build shows it
	shared := pngOf(t, 24, 18, 4, png.BestSpeed) // an old and a recent build share it

	// Five capture builds: retention keeps the latest three.
	for i := range 5 {
		var c capture
		switch i {
		case 0:
			c = capture{"/checkout", "de", lonely, []string{"checkout.pay"}}
		case 1, 3:
			c = capture{"/checkout", "de", shared, []string{"checkout.pay"}}
		default:
			c = capture{"/checkout", "de", pngOf(t, 8, 8, uint8(i), png.BestSpeed), []string{"checkout.pay"}} //nolint:gosec // a test pattern
		}
		h.uploadCaptures(t, f, manifestOf(t, fmt.Sprintf("%07x", 0xabc0000+i), "main", c), partsOf(c.image))
	}
	lonelyKey := h.imageKey(f, normalizedDigest(t, lonely).String())
	sharedKey := h.imageKey(f, normalizedDigest(t, shared).String())
	if !exists(t, objects, lonelyKey) || !exists(t, objects, sharedKey) {
		t.Fatal("the uploads did not reach object storage")
	}

	got, err := h.svc.PurgeProject(h.developer(), f.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Builds) != 2 || got.Captures != 2 {
		t.Errorf("purged = %d builds, %d captures; want the two oldest", len(got.Builds), got.Captures)
	}
	if len(got.OrphanedImages) != 1 || got.OrphanedImages[0] != normalizedDigest(t, lonely) {
		t.Errorf("orphaned images = %v, want only the oldest build's own", got.OrphanedImages)
	}
	if got.ImagesDeleted != 1 {
		t.Errorf("images deleted = %d, want 1", got.ImagesDeleted)
	}
	if exists(t, objects, lonelyKey) {
		t.Error("the unreferenced image is still in the bucket")
	}
	if !exists(t, objects, sharedKey) {
		t.Error("an image a surviving capture still references was deleted")
	}

	// Re-running changes nothing and deletes nothing more: the deletes
	// are idempotent, and a missing object is not an error.
	again, err := h.svc.PurgeProject(h.developer(), f.project)
	if err != nil || len(again.Builds) != 0 || again.ImagesDeleted != 0 {
		t.Errorf("second purge = %+v, %v", again, err)
	}
	if !exists(t, objects, sharedKey) {
		t.Error("the second purge deleted a referenced image")
	}
	// Deleting an object out from under the purge is survivable too.
	if err := objects.Delete(context.Background(), sharedKey); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.PurgeProject(h.developer(), f.project); err != nil {
		t.Errorf("purge with an object already gone: %v", err)
	}
}

// TestPurgeDeletesAClosedBranchsBuildsAfterTheGracePeriod drives the
// boundary through the real Catalog branch overlay (RFC 0004 §2.3,
// §4.1): until the adapter reported closed branches, a branch's builds
// were kept forever.
func TestPurgeDeletesAClosedBranchsBuildsAfterTheGracePeriod(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	onMain := h.ingest(t, f, "plugin", "web", "1111111", "main", use{"checkout.pay", "a.vue", 1}).Build
	branch := h.ingest(t, f, "plugin", "web", "2222222", "feat/tip", use{"checkout.pay", "b.vue", 2}).Build
	if _, err := h.catalog.PushBranch(h.developer(), catalogdomain.ProjectID(f.project), catalogapp.BranchPush{
		Branch: "feat/tip", Items: []catalogapp.UpsertItem{{Key: "checkout.tip", Text: "Add a tip"}},
	}); err != nil {
		t.Fatal(err)
	}
	h.drain(t)

	// An open branch's builds are kept however old they are.
	h.clock.advance(60 * 24 * time.Hour)
	if got, err := h.svc.PurgeProject(h.developer(), f.project); err != nil || len(got.Builds) != 0 {
		t.Fatalf("open branch: purged %+v, %v", got.Builds, err)
	}

	closedAt := h.clock.now()
	if _, err := h.catalog.CloseBranch(h.developer(), catalogdomain.ProjectID(f.project), "feat/tip"); err != nil {
		t.Fatal(err)
	}
	h.drain(t)

	h.clock.set(closedAt.Add(domain.DefaultRetention.ClosedBranchGrace - time.Second))
	if got, err := h.svc.PurgeProject(h.developer(), f.project); err != nil || len(got.Builds) != 0 {
		t.Fatalf("a second before the grace ends: purged %+v, %v", got.Builds, err)
	}

	h.clock.set(closedAt.Add(domain.DefaultRetention.ClosedBranchGrace))
	got, err := h.svc.PurgeProject(h.developer(), f.project)
	if err != nil {
		t.Fatal(err)
	}
	if !sameUUIDs(got.Builds, []uuid.UUID{branch.ID}) {
		t.Errorf("purged %v, want the closed branch's build %v", got.Builds, branch.ID)
	}
	if n := count(t, "SELECT count(*) FROM context_builds WHERE id = $1", onMain.ID); n != 1 {
		t.Error("the default branch's build went with the closed branch")
	}
	// Reopening the branch stops the rest of its builds expiring.
	if _, err := h.catalog.ReopenBranch(h.developer(), catalogdomain.ProjectID(f.project), "feat/tip"); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	reopened := h.ingest(t, f, "plugin", "web", "3333333", "feat/tip", use{"checkout.pay", "c.vue", 3}).Build
	h.clock.advance(60 * 24 * time.Hour)
	if got, err := h.svc.PurgeProject(h.developer(), f.project); err != nil || len(got.Builds) != 0 {
		t.Errorf("after reopening: purged %+v, %v", got.Builds, err)
	}
	if n := count(t, "SELECT count(*) FROM context_builds WHERE id = $1", reopened.ID); n != 1 {
		t.Error("the reopened branch's build was purged")
	}
}
