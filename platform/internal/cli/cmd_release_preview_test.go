package cli

import (
	"strings"
	"testing"
)

func previewDiff(p releasePreviewJSON, locale string) *localeDiffJSON {
	for i := range p.Changes {
		if p.Changes[i].Locale == locale {
			return &p.Changes[i]
		}
	}
	return nil
}

func TestReleasePublishDryRunShowsWhatWouldShip(t *testing.T) {
	srv, w := seeded(t)
	const releases = "POST /v1/tenants/ten_1/projects/prj_1/releases "

	var pv releasePreviewJSON
	w.json(&pv, "release", "publish", "--dry-run", "--environment", "preview").want(t, ExitOK)
	if pv.Schema != "glossa.cli.release.preview/v1" || pv.Environment != "preview" || !pv.Releasable || pv.Base != nil ||
		pv.Release == nil || pv.Release.Counts.Messages != 3 || pv.Release.Counts.Locales["de"].Messages != 3 ||
		pv.Release.Counts.Locales["ja"].Messages != 2 || pv.Release.Counts.NewArtifacts != 3 || len(pv.Problems) != 0 || pv.Identical {
		t.Fatalf("dry run = %+v", pv)
	}
	if de := previewDiff(pv, "de"); de == nil || len(de.Added) != 3 {
		t.Errorf("changes = %+v", pv.Changes)
	}
	if srv.countRequests(releases) != 0 || srv.countRequests("POST /v1/tenants/ten_1/projects/prj_1/environments/preview/release-previews") != 1 {
		t.Fatalf("requests %v", srv.requests)
	}

	// Against what preview serves: only the change.
	v1 := publishJSON(t, w, "--environment", "preview").Release
	srv.translations["de"]["cart.checkout"].content = srv.translations["de"]["checkout.pay"].content
	w.json(&pv, "release", "publish", "--dry-run", "--environment", "preview").want(t, ExitOK)
	de := previewDiff(pv, "de")
	if pv.Base == nil || pv.Base.ID != v1.ID || pv.Base.Version != 1 || de == nil || strings.Join(de.Changed, ",") != "cart.checkout" ||
		len(de.Added)+len(de.Removed) != 0 || pv.Release.Counts.NewArtifacts != 1 {
		t.Errorf("dry run against v1 = %+v", pv)
	}
	h := w.run("release", "publish", "--dry-run", "--environment", "preview")
	h.want(t, ExitOK)
	for _, want := range []string{"Dry run", "preview", "nothing was published", "3 messages", "de 3", "ja 2", "compared with v1",
		"de  0 added, 1 changed, 0 removed", "~ cart.checkout", "en  unchanged"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("human dry run lacks %q:\n%s", want, h.stdout)
		}
	}
	if srv.countRequests(releases) != 1 {
		t.Errorf("a dry run published: %v", srv.requests)
	}

	// Once published, nothing would change.
	publishJSON(t, w, "--environment", "preview")
	w.json(&pv, "release", "publish", "--dry-run", "--environment", "preview").want(t, ExitOK)
	if !pv.Identical {
		t.Errorf("unchanged catalog: %+v", pv)
	}
	if h := w.run("release", "publish", "--dry-run", "--environment", "preview"); !strings.Contains(h.stdout, "nothing would change") {
		t.Errorf("unchanged output:\n%s", h.stdout)
	}
}

func TestReleasePublishDryRunReportsProblems(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN})
	var pv releasePreviewJSON
	w.json(&pv, "release", "publish", "--dry-run").want(t, ExitCheckFailed)
	if pv.Releasable || len(pv.Problems) != 1 || pv.Problems[0].Code != "not_releasable" || pv.Release != nil || pv.Environment != "development" {
		t.Fatalf("dry run = %+v", pv)
	}
	h := w.run("release", "publish", "--dry-run")
	h.want(t, ExitCheckFailed)
	if !strings.Contains(h.stdout, "not releasable") || !strings.Contains(h.stdout, "no active messages") {
		t.Errorf("output:\n%s", h.stdout)
	}
	wantError(t, w, ExitNetwork, "not_found", "release", "publish", "--dry-run", "--environment", "qa")
}
