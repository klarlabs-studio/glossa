package cli

import (
	"strings"
	"testing"
)

func TestImportV0DBUsage(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	var doc errorDoc
	w.json(&doc, "import", "--from", "v0", "--v0-db", "postgres://x/y", "--v0-url", "http://x").want(t, ExitUsage)
	if !strings.Contains(doc.Error.Message, "--v0-url belongs to an API import") {
		t.Errorf("error = %+v", doc.Error)
	}
	w.json(&doc, "import", "--from", "v0", "--v0-db", "postgres://x/y", "--v0-key-env", "K").want(t, ExitUsage)
	w.json(&doc, "import", "--from", "v0", "--v0-url", "http://x", "--v0-tenant", "acme").want(t, ExitUsage)
	if !strings.Contains(doc.Error.Message, "--v0-tenant belongs to --v0-db") {
		t.Errorf("error = %+v", doc.Error)
	}
	w.json(&doc, "import", "--format", "json", "f.json", "--v0-db", "postgres://x/y").want(t, ExitUsage)
	// --invite sends what --v0-db plans, and only for real.
	w.json(&doc, "import", "--from", "v0", "--v0-url", "http://x", "--invite").want(t, ExitUsage)
	if !strings.Contains(doc.Error.Message, "--invite belongs to --v0-db") {
		t.Errorf("error = %+v", doc.Error)
	}
	w.json(&doc, "import", "--from", "v0", "--v0-db", "postgres://x/y", "--invite", "--dry-run").want(t, ExitUsage)
	if !strings.Contains(doc.Error.Message, "--dry-run sends nothing") {
		t.Errorf("error = %+v", doc.Error)
	}
	w.json(&doc, "import", "--format", "json", "f.json", "--invite").want(t, ExitUsage)
	if srv.members.posts != 0 {
		t.Errorf("a refused command sent %d invitations", srv.members.posts)
	}
	w.json(&doc, "import", "--from", "v0").want(t, ExitUsage)
	if !strings.Contains(doc.Error.Message, "--v0-db") {
		t.Errorf("error = %+v; the usage must name both sources", doc.Error)
	}
}

func TestImportV0DBUnreachableNamesTheServerWithoutCredentials(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	var doc errorDoc
	w.json(&doc, "import", "--from", "v0", "--v0-db", "postgres://u:s3cret@127.0.0.1:1/v0_restore?connect_timeout=2", "--v0-project", "site").
		want(t, ExitNetwork)
	if doc.Error.Code != "v0_db_unreachable" || doc.Error.Where != "postgres://127.0.0.1:1/v0_restore" {
		t.Errorf("error = %+v", doc.Error)
	}
	if strings.Contains(doc.Error.Where+doc.Error.Message+doc.Error.Fix, "s3cret") {
		t.Errorf("error leaks the password: %+v", doc.Error)
	}
}

// An API-mode import writes v0_status too, so both modes' provenance
// reads the same.
func TestImportFromV03APIWritesV0Status(t *testing.T) {
	srv := newFakeServer(t)
	srv.sourceLocale, srv.locales = "de", []string{"de"}
	old := fakeV03(t)
	w := newWorkspace(t).withProject(srv, nil)
	w.write("glossa.yaml", strings.Replace(w.read("glossa.yaml"), "source_locale: en", "source_locale: de", 1))
	w.env["GLOSSA_V0_KEY"] = "glossa_v03key"
	var out importJSON
	w.json(&out, "import", "--from", "v0", "--v0-url", old.URL, "--v0-project", "site").want(t, ExitOK)
	d := srv.translations["en"]["home.title"].originDetail
	if d["v0_status"] != "ai_translated" || d["status"] != "ai_translated" || d["source"] != "glossa-v0.3" || d["project"] != "site" {
		t.Errorf("origin_detail = %v", d)
	}
}
