package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// publishJSON runs `release publish` and returns its document.
func publishJSON(t *testing.T, w *workspace, args ...string) releasePublishJSON {
	t.Helper()
	var out releasePublishJSON
	w.json(&out, append([]string{"release", "publish"}, args...)...).want(t, ExitOK)
	return out
}

func wantError(t *testing.T, w *workspace, exit ExitCode, code string, args ...string) errorDoc {
	t.Helper()
	var doc errorDoc
	w.json(&doc, args...).want(t, exit)
	if doc.Schema != "glossa.cli.error/v1" || doc.Error.Code != code {
		t.Errorf("glossa %s: error = %+v, want code %s", strings.Join(args, " "), doc.Error, code)
	}
	return doc
}

func TestReleasePublishSendsAnIdempotencyKeyAndReportsWhatShipped(t *testing.T) {
	srv, w := seeded(t)
	out := publishJSON(t, w, "--environment", "preview", "--note", "first cut")
	r := out.Release
	if out.Schema != "glossa.cli.release.publish/v1" || out.Replayed || out.IdempotencyKey == "" ||
		r.Version != 1 || r.Environment != "preview" || r.Note != "first cut" || r.Counts.Messages != 3 ||
		r.Counts.Locales["de"].Messages != 3 || r.Counts.Locales["ja"].Messages != 2 {
		t.Fatalf("publish = %+v", out)
	}
	if srv.countRequests("POST /v1/tenants/ten_1/projects/prj_1/releases "+out.IdempotencyKey) != 1 {
		t.Errorf("the generated Idempotency-Key wasn't sent: %v", srv.requests)
	}

	// A new invocation is a new publish; a repeated key replays.
	again := publishJSON(t, w, "--environment", "preview", "--idempotency-key", "ci-run-7")
	replay := publishJSON(t, w, "--environment", "preview", "--idempotency-key", "ci-run-7")
	if again.Release.Version != 2 || again.IdempotencyKey != "ci-run-7" || !replay.Replayed || replay.Release.ID != again.Release.ID {
		t.Errorf("again = %+v, replay = %+v", again, replay)
	}

	r3 := w.run("release", "publish")
	r3.want(t, ExitOK)
	for _, want := range []string{"Published v3 to development", "3 messages", "de 3", "ja 2", "3 artifacts"} {
		if !strings.Contains(r3.stdout, want) {
			t.Errorf("human output lacks %q:\n%s", want, r3.stdout)
		}
	}
	replayed := w.run("release", "publish", "--environment", "preview", "--idempotency-key", "ci-run-7")
	if !strings.Contains(replayed.stdout, "already published") {
		t.Errorf("replay output:\n%s", replayed.stdout)
	}
}

func TestReleaseListAndShowSayWhichEnvironmentsServeWhat(t *testing.T) {
	_, w := seeded(t)
	first := publishJSON(t, w, "--environment", "preview").Release
	publishJSON(t, w, "--environment", "production", "--note", "launch")

	var list releaseListJSON
	w.json(&list, "release", "list").want(t, ExitOK)
	if list.Schema != "glossa.cli.release.list/v1" || len(list.Releases) != 2 || list.Releases[0].Version != 2 ||
		strings.Join(list.Releases[0].Serving, ",") != "production" || strings.Join(list.Releases[1].Serving, ",") != "preview" {
		t.Fatalf("list = %+v", list)
	}
	w.json(&list, "release", "list", "--limit", "1").want(t, ExitOK)
	if len(list.Releases) != 1 {
		t.Errorf("list --limit 1 = %d releases", len(list.Releases))
	}
	human := w.run("release", "list")
	human.want(t, ExitOK)
	if !strings.Contains(human.stdout, "VERSION") || !strings.Contains(human.stdout, "launch") || !strings.Contains(human.stdout, "production") {
		t.Errorf("list output:\n%s", human.stdout)
	}

	var show releaseShowJSON
	w.json(&show, "release", "show", "v1").want(t, ExitOK)
	if show.Schema != "glossa.cli.release.show/v1" || show.Release.ID != first.ID || strings.Join(show.Serving, ",") != "preview" {
		t.Errorf("show v1 = %+v", show)
	}
	w.json(&show, "release", "show", first.ID).want(t, ExitOK)
	if show.Release.Version != 1 {
		t.Errorf("show by ID = %+v", show)
	}
	h := w.run("release", "show", "v2")
	h.want(t, ExitOK)
	for _, want := range []string{"Release v2", "production", "launch", "de", "approved"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("show output lacks %q:\n%s", want, h.stdout)
		}
	}
	wantError(t, w, ExitNetwork, "release_not_found", "release", "show", "v9")
	wantError(t, w, ExitNetwork, "release_not_found", "release", "show", "rel_404")
}

func TestReleaseEnvironmentsShowTheReleaseVersionEachServes(t *testing.T) {
	_, w := seeded(t)
	publishJSON(t, w, "--environment", "preview")
	publishJSON(t, w, "--environment", "production")

	var out releaseEnvironmentsJSON
	w.json(&out, "release", "environments").want(t, ExitOK)
	got := map[string]int{}
	for _, e := range out.Environments {
		if e.Release != nil {
			got[e.Name] = e.Release.Version
		}
	}
	if out.Schema != "glossa.cli.release.environments/v1" || len(out.Environments) != 4 || got["preview"] != 1 || got["production"] != 2 || len(got) != 2 {
		t.Fatalf("environments = %+v", out)
	}
	h := w.run("release", "environments")
	h.want(t, ExitOK)
	for _, want := range []string{"production", "v2", "staging", "approved"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("environments output lacks %q:\n%s", want, h.stdout)
		}
	}
}

func TestReleaseDiffListsChangesPerLocale(t *testing.T) {
	srv, w := seeded(t)
	v1 := publishJSON(t, w, "--environment", "production").Release // de: nothing approved yet
	srv.translations["de"]["cart.checkout"].state = "approved"
	v2 := publishJSON(t, w, "--environment", "production").Release

	var out releaseDiffJSON
	w.json(&out, "release", "diff", "v1", "v2").want(t, ExitOK)
	de := localeDiff(out, "de")
	if out.Schema != "glossa.cli.release.diff/v1" || out.Release.ID != v2.ID || out.Base == nil || out.Base.Version != 1 ||
		out.Identical || de == nil || strings.Join(de.Added, ",") != "cart.checkout" || len(localeDiff(out, "en").Added) != 0 {
		t.Fatalf("diff = %+v", out)
	}
	// One release: compared with its parent.
	var parent releaseDiffJSON
	w.json(&parent, "release", "diff", v2.ID).want(t, ExitOK)
	if parent.Base == nil || parent.Base.ID != v1.ID {
		t.Errorf("diff against the parent = %+v", parent)
	}
	w.json(&parent, "release", "diff", "v2", "v2").want(t, ExitOK)
	if !parent.Identical {
		t.Errorf("a release against itself = %+v", parent)
	}
	h := w.run("release", "diff", "v1", "v2")
	h.want(t, ExitOK)
	for _, want := range []string{"v1 → v2", "de", "+ cart.checkout", "1 added"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("diff output lacks %q:\n%s", want, h.stdout)
		}
	}
}

func localeDiff(d releaseDiffJSON, locale string) *localeDiffJSON {
	for i := range d.Locales {
		if d.Locales[i].Locale == locale {
			return &d.Locales[i]
		}
	}
	return nil
}

func TestReleasePromoteAndRollbackMovePointers(t *testing.T) {
	srv, w := seeded(t)
	preview := publishJSON(t, w, "--environment", "preview").Release
	prod1 := publishJSON(t, w, "--environment", "production").Release
	srv.translations["de"]["cart.checkout"].state = "approved"
	prod2 := publishJSON(t, w, "--environment", "production").Release

	var moved releaseMoveJSON
	w.json(&moved, "release", "promote", prod1.ID, "--to", "staging").want(t, ExitOK)
	if moved.Schema != "glossa.cli.release.promote/v1" || moved.Environment.Name != "staging" || moved.Environment.Release == nil ||
		moved.Environment.Release.Version != prod1.Version || moved.Previous != nil {
		t.Fatalf("promote = %+v", moved)
	}
	h := w.run("release", "promote", "v3", "--to", "staging")
	h.want(t, ExitOK)
	if !strings.Contains(h.stdout, "staging now serves v3") || !strings.Contains(h.stdout, "was v2") {
		t.Errorf("promote output:\n%s", h.stdout)
	}
	doc := wantError(t, w, ExitNetwork, "release_ineligible", "release", "promote", preview.ID, "--to", "production")
	if !strings.Contains(doc.Error.Fix, "publish") {
		t.Errorf("fix = %q", doc.Error.Fix)
	}

	w.json(&moved, "release", "rollback", "--environment", "production").want(t, ExitOK)
	if moved.Schema != "glossa.cli.release.rollback/v1" || moved.Environment.Release.ID != prod1.ID ||
		moved.Previous == nil || moved.Previous.ID != prod2.ID {
		t.Fatalf("rollback = %+v", moved)
	}
	w.json(&moved, "release", "rollback", "--environment", "production", "--to", "v3").want(t, ExitOK)
	if moved.Environment.Release.Version != 3 {
		t.Errorf("rollback --to v3 = %+v", moved)
	}
	wantError(t, w, ExitNetwork, "not_in_history", "release", "rollback", "--environment", "production", "--to", preview.ID)
	wantError(t, w, ExitNetwork, "no_rollback_target", "release", "rollback", "--environment", "preview")
}

func TestReleaseKeysCreateListRevoke(t *testing.T) {
	_, w := seeded(t)
	var created releaseKeyJSON
	w.json(&created, "release", "keys", "create", "web").want(t, ExitOK)
	if created.Schema != "glossa.cli.release.key/v1" || created.Action != "created" || created.Key.Name != "web" || !strings.HasPrefix(created.Key.Key, "glossa_pk_") {
		t.Fatalf("create = %+v", created)
	}
	h := w.run("release", "keys", "create", "go-emails")
	h.want(t, ExitOK)
	if !strings.Contains(h.stdout, "glossa_pk_") || !strings.Contains(h.stdout, "go-emails") {
		t.Errorf("create output:\n%s", h.stdout)
	}

	var list releaseKeysJSON
	w.json(&list, "release", "keys").want(t, ExitOK)
	if list.Schema != "glossa.cli.release.keys/v1" || len(list.Keys) != 2 || list.Keys[0].Key != created.Key.Key {
		t.Fatalf("keys = %+v", list)
	}

	var revoked releaseKeyJSON
	w.json(&revoked, "release", "keys", "revoke", "web").want(t, ExitOK)
	if revoked.Action != "revoked" || revoked.Key.ID != created.Key.ID || revoked.Key.RevokedAt == nil {
		t.Fatalf("revoke = %+v", revoked)
	}
	lh := w.run("release", "keys", "list")
	if !strings.Contains(lh.stdout, "revoked") || !strings.Contains(lh.stdout, "active") {
		t.Errorf("keys list output:\n%s", lh.stdout)
	}
	wantError(t, w, ExitNetwork, "key_revoked", "release", "keys", "revoke", created.Key.ID)
	wantError(t, w, ExitUsage, "key_not_found", "release", "keys", "revoke", "nope")
}

// A key's scope (RFC 0004 §4.3): production by default, a preview key
// on request, and changeable without changing the key.
func TestReleaseKeyScopes(t *testing.T) {
	_, w := seeded(t)
	var created releaseKeyJSON
	w.json(&created, "release", "keys", "create", "web").want(t, ExitOK)
	if len(created.Key.Scope.Environments) != 1 || created.Key.Scope.Environments[0] != "production" || created.Key.Scope.Branches {
		t.Fatalf("a new key reads %+v", created.Key.Scope)
	}
	var preview releaseKeyJSON
	w.json(&preview, "release", "keys", "create", "previews", "--environments", "preview", "--preview").want(t, ExitOK)
	if !preview.Key.Scope.Branches || len(preview.Key.Scope.Environments) != 1 {
		t.Fatalf("preview key reads %+v", preview.Key.Scope)
	}

	var scoped releaseKeyJSON
	w.json(&scoped, "release", "keys", "scope", "web", "--environments", "production,staging").want(t, ExitOK)
	if scoped.Action != "scoped" || scoped.Key.Key != created.Key.Key || len(scoped.Key.Scope.Environments) != 2 {
		t.Fatalf("scope = %+v", scoped)
	}
	h := w.run("release", "keys", "list")
	if !strings.Contains(h.stdout, "production, staging") || !strings.Contains(h.stdout, "branch previews") {
		t.Errorf("keys list output:\n%s", h.stdout)
	}
	wantError(t, w, ExitUsage, "invalid_usage", "release", "keys", "scope", "web")
	wantError(t, w, ExitUsage, "key_not_found", "release", "keys", "scope", "nope", "--preview")
}

func TestReleaseUsageAndServerRefusals(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN})
	for _, args := range [][]string{
		{"release"},
		{"release", "ship"},
		{"release", "promote", "rel_1"},
		{"release", "promote", "--to", "staging"},
		{"release", "rollback"},
		{"release", "show"},
		{"release", "diff"},
		{"release", "diff", "a", "b", "c"},
		{"release", "environments", "extra"},
		{"release", "list", "--limit", "-1"},
		{"release", "keys", "create"},
		{"release", "keys", "revoke"},
		{"release", "keys", "rotate"},
		{"release", "publish", "--idempotency-key", "has space"},
		{"pull", "--release", "latest"},
		{"pull", "--environment", "production"},
	} {
		wantError(t, w, ExitUsage, "invalid_usage", args...)
	}
	if n := len(srv.requests); n != 0 {
		t.Errorf("usage errors sent %d requests: %v", n, srv.requests)
	}
	wantError(t, w, ExitNetwork, "not_releasable", "release", "publish")
	wantError(t, w, ExitUsage, "invalid_environment", "release", "publish", "--environment", "qa")
}

func TestPullReleaseWritesTheBundleForAnEnvironment(t *testing.T) {
	srv, w := seeded(t)
	wantError(t, w, ExitNetwork, "no_release", "pull", "--release", "latest", "--environment", "production")
	srv.translations["de"]["cart.checkout"].state = "approved"
	prod := publishJSON(t, w, "--environment", "production").Release

	var out pullJSON
	w.json(&out, "pull", "--release", "latest", "--environment", "production", "--out", "public/glossa").want(t, ExitOK)
	b := out.Release
	if b == nil || b.ReleaseID != prod.ID || b.Version != 1 || b.Environment != "production" || b.Artifacts != 3 ||
		strings.Join(b.Locales, ",") != "de,en,ja" || b.Dir != "public/glossa" {
		t.Fatalf("pull --release = %+v", out)
	}
	var m struct {
		Environment string `json:"environment"`
		Artifacts   map[string]map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(w.read("public/glossa/manifest.json")), &m); err != nil || m.Environment != "production" {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "public", "glossa", "a", m.Artifacts["de"]["default"].SHA256+".json")); err != nil {
		t.Error(err)
	}

	// By ID the release's own environment is the default; another one can be named.
	w.json(&out, "pull", "--release", prod.ID, "--out", "b1").want(t, ExitOK)
	if out.Release.Environment != "production" {
		t.Errorf("default environment = %+v", out.Release)
	}
	w.json(&out, "pull", "--release", "v1", "--environment", "staging", "--out", "b2").want(t, ExitOK)
	if out.Release.Environment != "staging" {
		t.Errorf("--environment staging = %+v", out.Release)
	}
	h := w.run("pull", "--release", "latest", "--environment", "production", "--out", "public/glossa")
	h.want(t, ExitOK)
	if !strings.Contains(h.stdout, "v1 (production)") || !strings.Contains(h.stdout, "public/glossa") {
		t.Errorf("pull output:\n%s", h.stdout)
	}

	srv.rel.tamper = true
	doc := wantError(t, w, ExitNetwork, "bundle_failed", "pull", "--release", "latest", "--environment", "production", "--out", "b3")
	if !strings.Contains(doc.Error.Why, "integrity") {
		t.Errorf("why = %q", doc.Error.Why)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "b3", "manifest.json")); !os.IsNotExist(err) {
		t.Errorf("manifest written for a tampered bundle: %v", err)
	}
}
