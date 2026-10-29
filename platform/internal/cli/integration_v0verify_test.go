//go:build integration

package cli

import (
	"context"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/v0"
	"go.klarlabs.de/glossa/platform/internal/cli/v0/v0test"
)

// verifySeed adds a key with a bare apostrophe to the v0test seed.
const verifySeed = v0test.Seed + `
INSERT INTO keys (id, project_id, key, description, first_seen_at) VALUES
  ('f0000000-0000-0000-0000-0000000000a1', '` + v0test.ProjectID + `', 'copy.bare', NULL, '2025-01-03T00:00:00Z');
INSERT INTO translations (id, key_id, locale_id, value, status, updated_by, updated_at) VALUES
  ('e0000000-0000-0000-0000-0000000000a1', 'f0000000-0000-0000-0000-0000000000a1', 'd0000000-0000-0000-0000-000000000001',
   'Geht''s gut, {name}?', 'approved', NULL, '2025-02-01T09:00:00Z');
`

// `--verify` reading v0.3's text from a marked restore: v0.3's
// apostrophe defect is reported as known, and what v0.3's formatter
// cannot render at all — `{amount, number}`, outside its ICU subset — is
// a mismatch the run fails on, named with its key and locale.
func TestVerifyARestoredV03Database(t *testing.T) {
	format, runtime := jsModules(t)
	ctx := context.Background()
	old, err := v0test.Start(ctx, verifySeed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(old.Close)
	backup, err := old.Backup(ctx, "glossa-20261001")
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := old.Restore(ctx, backup, "v0_restore")
	if err != nil {
		t.Fatal(err)
	}
	edge := newFakeEdge(t, "dk_test", "v0-check", "de", map[string]map[string]string{
		"de": {"cart.items": "{count, plural, one {# Artikel} other {# Artikel}}", "home.title": "Willkommen bei Brotwerk",
			"checkout.pay": "{amount, number} zahlen", "copy.bare": "Geht's gut, {name}?"},
		"en": {"cart.items": "{count, plural, one {# item} other {# items}}", "home.title": "Welcome to Brotwerk"},
		"fr": {"home.title": "Bienvenue chez Brotwerk"},
	})
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	w.env["GLOSSA_DELIVERY_KEY"] = "dk_test"
	args := []string{"import", "--from", "v0", "--v0-db", dsn, "--v0-tenant", "klarlabs", "--v0-project", "brotwerk", "--verify",
		"--edge", edge.srv.URL, "--environment", "v0-check", "--format-module", format, "--runtime-module", runtime}

	var out verifyJSON
	w.json(&out, args...).want(t, ExitCheckFailed)
	if out.Source["restore"] != "glossa-20261001.sql.gz" || out.Summary["keys"] != 4 || out.Summary["locales"] != 3 {
		t.Fatalf("report = %+v", out)
	}
	if len(out.KnownDefects) != 1 || out.KnownDefects[0].Key != "copy.bare" || out.KnownDefects[0].Defect != v0.DefectBareApostrophe {
		t.Errorf("known defects = %+v", out.KnownDefects)
	}
	if len(out.Mismatches) != 1 || out.Mismatches[0].Key != "checkout.pay" || out.Mismatches[0].V0Error == "" {
		t.Errorf("mismatches = %+v", out.Mismatches)
	}
	if srv.countRequests("POST") != 0 {
		t.Error("--verify wrote to the platform")
	}
}
