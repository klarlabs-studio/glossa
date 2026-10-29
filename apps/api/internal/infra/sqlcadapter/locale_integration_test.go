//go:build integration

package sqlcadapter_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/locale"
	"go.klarlabs.de/glossa/apps/api/internal/infra/sqlcadapter"
)

// Migration 0006 widened locales.code to 35 characters. Tags longer
// than the old VARCHAR(8) must round-trip through the repository.
func TestLocaleRepo_StoresLongBCP47Tags(t *testing.T) {
	f := setupPg(t)
	defer f.cleanup()

	repo := sqlcadapter.NewLocaleRepo(db.New(f.pool))
	tags := []string{"zh-Hant-TW", "ca-ES-valencia", "sr-Latn-RS"}

	var got []locale.Locale
	if err := runAsTenant(t, f.pool, f.tenantA, func(ctx context.Context, _ *db.Queries) error {
		for _, raw := range tags {
			code, err := locale.NewCode(raw)
			if err != nil {
				return err
			}
			if err := repo.Save(ctx, locale.Locale{
				ID: uuid.New(), ProjectID: f.projectA.ID, Code: code, Label: locale.Label(raw), Enabled: true,
			}); err != nil {
				return err
			}
		}
		var err error
		got, err = repo.ListForProject(ctx, f.projectA.ID)
		return err
	}); err != nil {
		t.Fatalf("tx: %v", err)
	}

	stored := map[string]bool{}
	for _, l := range got {
		stored[l.Code.String()] = true
	}
	for _, tag := range tags {
		if !stored[tag] {
			t.Errorf("locale %q not stored; have %v", tag, stored)
		}
	}
}
