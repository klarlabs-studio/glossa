package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// TestRequireInEnvironment pins approvals.decide on a release request
// (RFC 0006 §4.2, §5.1): environment-scoped, not locale-scoped. A
// reviewer limited to de decides a production release request — a
// release ships every locale, and the locale scope speaks about text —
// unless their environment scope leaves production out. A token never
// holds the permission, a project outside the scope does not exist, and
// a permission that is not environment-scoped is not checked this way.
func TestRequireInEnvironment(t *testing.T) {
	tenant := tenancy.NewID()
	bg := context.Background()
	project, other := uuid.New(), uuid.New()

	for _, c := range []struct {
		name string
		ctx  context.Context
		env  string
		want error
	}{
		{"owner", authztest.Member(bg, tenant, []string{"owner"}), "production", nil},
		{"admin", authztest.Member(bg, tenant, []string{"admin"}), "production", nil},
		{"reviewer of every locale", authztest.Member(bg, tenant, []string{"reviewer"}), "production", nil},
		{"reviewer limited to de", authztest.Member(bg, tenant, []string{"reviewer"}, "de"), "production", nil},
		{"reviewer limited to staging, in staging",
			authztest.InEnvironments(authztest.Member(bg, tenant, []string{"reviewer"}), "staging"), "staging", nil},
		{"reviewer limited to staging, in production",
			authztest.InEnvironments(authztest.Member(bg, tenant, []string{"reviewer"}), "staging"), "production", authz.ErrForbidden},
		{"developer", authztest.Member(bg, tenant, []string{"developer"}), "production", authz.ErrForbidden},
		{"translator", authztest.Member(bg, tenant, []string{"translator"}), "production", authz.ErrForbidden},
		{"token with every scope", authztest.Token(bg, tenant, "read", "write", "publish", "admin"), "production", authz.ErrForbidden},
		{"reviewer scoped to another project",
			authztest.ScopedMember(bg, tenant, []uuid.UUID{other}, []string{"reviewer"}), "production", authz.ErrNotVisible},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := authz.RequireInEnvironment(c.ctx, authz.ApprovalsDecide, project, c.env)
			if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("RequireInEnvironment = %v, want %v", err, c.want)
			}
		})
	}

	owner := authztest.Member(bg, tenant, []string{"owner"})
	if err := authz.RequireInEnvironment(owner, authz.ReleasesPublish, project, "production"); err == nil {
		t.Error("releases.publish is not environment-scoped and must not pass an environment check")
	}
}
