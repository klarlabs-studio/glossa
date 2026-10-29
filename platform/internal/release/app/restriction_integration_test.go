//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/release/app"
)

func outcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, authz.ErrNotVisible), errors.Is(err, app.ErrNotFound), errors.Is(err, app.ErrReleaseNotInProject):
		return "not found"
	case errors.Is(err, authz.ErrForbidden):
		return "denied"
	}
	return err.Error()
}

// TestReleaseRestrictions: a project outside a principal's scope has no
// releases, environments or keys to read or act on, and an assigned
// member can't read, publish, promote or roll back anything (RFC 0006
// §3.3, §4.1).
func TestReleaseRestrictions(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, []string{"de"}, map[string]string{"a": "Apple"})
	page := pagination.Page{Size: 10}
	rel, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production", Note: "first"}, "")
	if err != nil {
		t.Fatal(err)
	}
	scopedOut := authztest.ScopedMember(context.Background(), h.tenant, []uuid.UUID{uuid.New()}, []string{"developer"})
	scopedIn := authztest.ScopedMember(context.Background(), h.tenant, []uuid.UUID{p}, []string{"developer"})
	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(context.Background(), h.tenant, cov, "de")
	cov.Assign(member, p, uuid.New(), "de")

	calls := map[string]func(context.Context) error{
		"list releases": func(ctx context.Context) error { _, _, err := h.svc.ListReleases(ctx, p, page); return err },
		"get release":   func(ctx context.Context) error { _, err := h.svc.GetRelease(ctx, p, rel.ID); return err },
		"manifest": func(ctx context.Context) error {
			_, err := h.svc.ReleaseManifest(ctx, p, rel.ID, "production")
			return err
		},
		"environments": func(ctx context.Context) error { _, _, err := h.svc.ListEnvironments(ctx, p, page); return err },
		"environment":  func(ctx context.Context) error { _, err := h.svc.GetEnvironment(ctx, p, "production"); return err },
		"deployments": func(ctx context.Context) error {
			_, _, err := h.svc.ListDeployments(ctx, p, "production", page)
			return err
		},
		"keys":         func(ctx context.Context) error { _, _, err := h.svc.ListDeliveryKeys(ctx, p, page); return err },
		"signing keys": func(ctx context.Context) error { _, err := h.svc.SigningKeys(ctx, p); return err },
		"preview":      func(ctx context.Context) error { _, err := h.svc.PreviewPublish(ctx, p, "production"); return err },
		"publish": func(ctx context.Context) error {
			_, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, "")
			return err
		},
		"rollback": func(ctx context.Context) error { _, err := h.svc.Rollback(ctx, p, "production", nil); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if got := outcome(call(scopedOut)); got != "not found" {
				t.Errorf("out of scope: got %s, want not found", got)
			}
			if got := outcome(call(vendor)); got != "denied" {
				t.Errorf("assigned: got %s, want denied", got)
			}
		})
	}
	t.Run("in scope", func(t *testing.T) {
		if _, err := h.svc.GetRelease(scopedIn, p, rel.ID); err != nil {
			t.Errorf("a developer limited to the project reads its release: %v", err)
		}
	})
}
