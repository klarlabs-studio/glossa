//go:build integration

package v0_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/v0"
	"go.klarlabs.de/glossa/platform/internal/cli/v0/v0test"
)

// One v0.3 server (apps/api's migrations, the seed) for every case; each
// case restores its own database from the same backup.
func TestReadRestoreAgainstV03Schema(t *testing.T) {
	ctx := context.Background()
	srv, err := v0test.Start(ctx, v0test.Seed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	backup, err := srv.Backup(ctx, "glossa-20261001")
	if err != nil {
		t.Fatal(err)
	}
	restore := func(t *testing.T, db string) string {
		t.Helper()
		dsn, err := srv.Restore(ctx, backup, db)
		if err != nil {
			t.Fatal(err)
		}
		return dsn
	}
	refused := func(t *testing.T, dsn, why string) {
		t.Helper()
		_, err := v0.ReadRestore(ctx, dsn, "klarlabs", "brotwerk")
		var nr *v0.NotARestoreError
		if !errors.As(err, &nr) || !strings.Contains(err.Error(), why) {
			t.Fatalf("err = %v, want a refusal saying %q", err, why)
		}
	}

	t.Run("v0.3's own database is refused", func(t *testing.T) {
		refused(t, srv.DSN(v0test.LiveDB), "no glossa_v0_restore.marker table")
	})

	t.Run("the restore script refuses an existing database", func(t *testing.T) {
		if _, err := srv.Restore(ctx, backup, v0test.LiveDB); err == nil {
			t.Fatal("v0-restore.sh marked v0.3's own database")
		}
		refused(t, srv.DSN(v0test.LiveDB), "no glossa_v0_restore.marker table")
	})

	t.Run("a marked restore is read in full", func(t *testing.T) {
		dsn := restore(t, "v0_restore")
		_, err := v0.ReadRestore(ctx, dsn, "", "brotwerk")
		var amb *v0.AmbiguousProjectError
		if !errors.As(err, &amb) || strings.Join(amb.Tenants, ",") != "klarlabs,other" {
			t.Fatalf("err = %v; two tenants have brotwerk", err)
		}
		s, err := v0.ReadRestore(ctx, dsn, "klarlabs", "brotwerk")
		if err != nil {
			t.Fatal(err)
		}
		checkSnapshot(t, s)
	})

	t.Run("a copied marker is refused", func(t *testing.T) {
		restore(t, "v0_marked")
		// Dump the marked restore (marker included) and load it by hand
		// into a new, read-only database: the marker came along, but it
		// names the database it was written in.
		if _, err := srv.Shell(ctx, "pg_dump -d v0_marked > /tmp/marked.sql && createdb v0_copy && psql -X -q -v ON_ERROR_STOP=1 -d v0_copy -f /tmp/marked.sql"); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.SQL(ctx, "v0_copy", "ALTER DATABASE v0_copy SET default_transaction_read_only = on;"); err != nil {
			t.Fatal(err)
		}
		refused(t, srv.DSN("v0_copy"), "was copied")
	})

	t.Run("a writable database is refused even with the marker", func(t *testing.T) {
		restore(t, "v0_writable")
		if _, err := srv.SQL(ctx, "v0_writable", "ALTER DATABASE v0_writable RESET default_transaction_read_only;"); err != nil {
			t.Fatal(err)
		}
		refused(t, srv.DSN("v0_writable"), "read-only")
	})

	t.Run("a role row-level security filters is refused, not given fewer rows", func(t *testing.T) {
		restore(t, "v0_rls")
		if _, err := srv.SQL(ctx, "v0_rls", `CREATE ROLE v0_reader LOGIN PASSWORD 'reader' NOSUPERUSER NOBYPASSRLS;
GRANT USAGE ON SCHEMA public, glossa_v0_restore TO v0_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA public, glossa_v0_restore TO v0_reader;`); err != nil {
			t.Fatal(err)
		}
		_, err := v0.ReadRestore(ctx, srv.DSN("v0_rls", "v0_reader", "reader"), "klarlabs", "brotwerk")
		var rs *v0.RowSecurityError
		if !errors.As(err, &rs) || !v0.IsRefusal(err) {
			t.Fatalf("err = %v, want a row-security refusal", err)
		}
	})

	t.Run("a database without v0.3's schema is refused", func(t *testing.T) {
		restore(t, "v0_old")
		if _, err := srv.SQL(ctx, "v0_old", "ALTER TABLE audit_log DROP COLUMN actor_kind CASCADE;"); err != nil {
			t.Fatal(err)
		}
		_, err := v0.ReadRestore(ctx, srv.DSN("v0_old"), "klarlabs", "brotwerk")
		var se *v0.SchemaError
		if !errors.As(err, &se) || !strings.Contains(err.Error(), "audit_log.actor_kind") {
			t.Fatalf("err = %v", err)
		}
	})
}

func checkSnapshot(t *testing.T, s v0.Snapshot) {
	t.Helper()
	if s.Restore.Database != "v0_restore" || s.Restore.DumpName != "glossa-20261001.sql.gz" ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(s.Restore.DumpSHA256) || s.Restore.RestoredBy != "postgres" {
		t.Errorf("restore = %+v", s.Restore)
	}
	if s.Tenant.Slug != "klarlabs" || s.Project.Name != "Brotwerk" || s.Project.DefaultLocale != "de" {
		t.Errorf("tenant/project = %+v / %+v", s.Tenant, s.Project)
	}
	if len(s.Locales) != 3 || s.Locales[1].Label != "English (UK)" || s.Locales[2].Enabled {
		t.Errorf("locales = %+v", s.Locales)
	}
	desc := map[string]string{}
	for _, k := range s.Keys {
		desc[k.Key] = k.Description
	}
	if desc["cart.items"] != v0test.CartDesc || desc["checkout.pay"] != v0test.CheckoutDesc || desc["home.title"] != "" {
		t.Errorf("descriptions = %v", desc)
	}
	rows := map[string]v0.Row{}
	for _, r := range s.Translations {
		rows[r.Locale+" "+r.Key] = r
	}
	if r := rows["en cart.items"]; r.UpdatedBy != v0test.BobID || r.ID != v0test.TrEnCart || r.UpdatedAt.Format("2006-01-02T15:04:05Z07:00") != "2025-02-02T10:30:00Z" ||
		r.LastActorKind != "user" {
		t.Errorf("en cart.items = %+v", r)
	}
	if r := rows["en home.title"]; r.UpdatedBy != "" || r.LastActorKind != "ai" || r.LastActorLabel != "openai" || r.Status != "ai_translated" {
		t.Errorf("en home.title = %+v", r)
	}
	if len(s.Translations) != 6 {
		t.Errorf("translations = %d, want 6", len(s.Translations))
	}
	if len(s.Users) != 3 || s.Users[1].Email != "bob@example.com" || strings.Join(s.Users[1].Locales, ",") != "en" {
		t.Errorf("users = %+v; the other tenant's must not appear", s.Users)
	}
	if len(s.History) != 4 {
		t.Fatalf("history = %+v; the other tenant's row must not appear", s.History)
	}
	if h := s.History[0]; h.TranslationID != v0test.GoneTrID || h.Key != "" || *h.Before != "Alt" {
		t.Errorf("oldest change = %+v", h)
	}
	if h := s.History[1]; h.Key != "cart.items" || h.Locale != "en" || h.Before != nil || h.ChangedBy != v0test.BobID {
		t.Errorf("first cart change = %+v", h)
	}
	if s.Uncarried != (v0.Uncarried{APIKeys: 1, AIProviders: 1, AnalyticsEvents: 2}) {
		t.Errorf("uncarried = %+v", s.Uncarried)
	}
}
