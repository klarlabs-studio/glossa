//go:build integration

package app_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// An XLIFF file's translations are imported as the locale the importer
// chooses; a file in a locale the project lacks fails with the choice
// to make, not with one invalid result per translation.
func TestXLIFFTargetLocaleOption(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de-AT", "fr"}, shop)
	ctx := h.developer()
	file := xliffDoc("de", [4]string{"home.title", "Welcome", "Servus", "final"})

	failed := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "xliff"}, file)
	if failed.State != domain.StateFailed || failed.FailureCode != domain.FailureTargetLocale ||
		!strings.Contains(failed.FailureMessage, "de,") && !strings.Contains(failed.FailureMessage, "in de") ||
		!strings.Contains(failed.FailureMessage, "de-AT") {
		t.Fatalf("mismatched import = %s %s %q", failed.State, failed.FailureCode, failed.FailureMessage)
	}

	as := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "xliff", Options: domain.Options{Locale: "de_at"}}, file)
	if as.State != domain.StateSucceeded || as.Options.Locale != "de-AT" {
		t.Fatalf("import as de-AT = %s %s %s", as.State, as.FailureCode, as.FailureMessage)
	}
	if tr, err := h.localization.GetTranslation(ctx, p, "home.title", "de-AT"); err != nil || tr.Content.Text != "Servus" {
		t.Errorf("de-AT translation = %+v %v", tr, err)
	}

	noTrg := strings.Replace(xliffDoc("fr", [4]string{"cart.empty", "Your cart is empty", "Panier vide", "translated"}), ` trgLang="fr"`, "", 1)
	named := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "xliff", Options: domain.Options{Locale: "fr"}}, noTrg)
	if named.State != domain.StateSucceeded || named.Summary.ByKind[domain.ItemTranslation].Created != 1 {
		t.Fatalf("file without trgLang = %s %s %+v", named.State, named.FailureMessage, named.Summary)
	}

	for locale, want := range map[string]error{"ja": domain.ErrLocaleNotInProject, "en": domain.ErrInvalidOptions} {
		_, _, err := h.svc.CreateImport(ctx, app.ImportRequest{ProjectID: &p, Format: "xliff", Options: domain.Options{Locale: locale}}, "")
		if !errors.Is(err, want) {
			t.Errorf("locale %s: err = %v, want %v", locale, err, want)
		}
	}
	if _, _, err := h.svc.CreateImport(ctx, app.ImportRequest{ProjectID: &p, Format: "json", Options: domain.Options{Locale: "it"}}, ""); !errors.Is(err, domain.ErrLocaleNotInProject) {
		t.Errorf("json locale it: err = %v", err)
	}
}

// Every result of a file says where its item is: line, column and the
// format's own reference — conflicts and invalid items included.
func TestImportResultsCarryPositions(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, shop)
	h.translate(t, p, "home.title", "de", "Willkommen", "approved")
	ctx := h.developer()

	x := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "xliff"}, xliffDoc("de",
		[4]string{"home.title", "Welcome", "Hallo", "final"},
		[4]string{"cart.empty", "Your cart is empty", "Leer", "final"},
	))
	got := byKey(h.results(t, ctx, x.ID))
	for key, want := range map[string]struct {
		line int
		ref  string
	}{
		"message home.title ":       {4, "#/f=f1/u=home.title"},
		"translation home.title de": {4, "#/f=f1/u=home.title"},
		"translation cart.empty de": {5, "#/f=f1/u=cart.empty"},
	} {
		it := got[key]
		if it.Line != want.line || it.Column < 1 || it.Ref != want.ref {
			t.Errorf("%s at %d:%d %q, want line %d %q", key, it.Line, it.Column, it.Ref, want.line, want.ref)
		}
	}
	if c := got["translation home.title de"]; c.Status != domain.ItemConflict {
		t.Errorf("the approved translation = %+v, want a conflict", c)
	}

	j := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "json", Options: domain.Options{Locale: "de"}},
		"{\n  \"cart\": {\n    \"empty\": \"Leer\"\n  },\n  \"nope\": \"x\"\n}\n")
	jr := byKey(h.results(t, ctx, j.ID))
	if it := jr["translation cart.empty de"]; it.Line != 3 || it.Column != 5 || it.Ref != "/cart/empty" {
		t.Errorf("json item = %+v", it)
	}
	if it := jr["translation nope de"]; it.Status != domain.ItemInvalid || it.Line != 5 || it.Ref != "/nope" {
		t.Errorf("json invalid item = %+v", it)
	}

	po := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "po", Options: domain.Options{Locale: "de"}},
		"msgid \"\"\nmsgstr \"Language: de\\n\"\n\nmsgctxt \"menu\"\nmsgid \"Open\"\nmsgstr \"Öffnen\"\n")
	for _, it := range h.results(t, ctx, po.ID) {
		wantLine := map[domain.ItemKind]int{domain.ItemMessage: 4, domain.ItemTranslation: 6}[it.Kind]
		if it.Line != wantLine || it.Column != 1 || it.Ref != `msgctxt "menu" msgid "Open"` {
			t.Errorf("po %s = %+v, want line %d", it.Kind, it, wantLine)
		}
	}
}

// The workspace's translation memory and termbase have routes of their
// own: tenant-wide, for managers, listed apart from projects' jobs.
func TestWorkspaceKnowledgeFiles(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "docs", false, []string{"de"}, nil)
	ctx := h.developer()
	tmxFile := `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4"><header creationtool="test" creationtoolversion="1" segtype="sentence" o-tmf="x" adminlang="en" srclang="en" datatype="plaintext"/>
<body>
<tu tuid="t1"><tuv xml:lang="en"><seg>Save changes</seg></tuv><tuv xml:lang="de"><seg>Änderungen speichern</seg></tuv></tu>
</body></tmx>`

	if _, _, err := h.svc.CreateKnowledgeImport(h.as([]string{"translator"}), domain.KindTM, "", "memory.tmx", ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator: err = %v, want forbidden", err)
	}
	j, _, err := h.svc.CreateKnowledgeImport(ctx, domain.KindTM, "merge", "memory.tmx", "")
	if err != nil || j.ProjectID != nil || j.Format != domain.FormatTMX {
		t.Fatalf("create = %+v %v", j, err)
	}
	if _, err := h.svc.UploadImport(ctx, j.ID, strings.NewReader(tmxFile)); err != nil {
		t.Fatal(err)
	}
	h.work(t)
	if j, _ = h.svc.GetImport(ctx, j.ID); j.State != domain.StateSucceeded || j.Summary.Created != 1 {
		t.Fatalf("tenant TMX import = %+v", j)
	}
	if it := h.results(t, ctx, j.ID)[0]; it.Line != 4 || it.Ref != "tu[1]" {
		t.Errorf("tmx item = %+v", it)
	}
	// A project's own TMX import isn't the workspace's.
	h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "tmx"}, tmxFile)

	kind := domain.KindTM
	jobs, _, err := h.svc.ListJobs(ctx, app.JobFilter{Direction: domain.Import, TenantWide: true, Kind: &kind}, pagination.Page{Size: pagination.MaxPageSize})
	if err != nil || len(jobs) != 1 || jobs[0].ID != j.ID {
		t.Errorf("workspace TM imports = %d %v", len(jobs), err)
	}
	termbase := domain.KindTermbase
	if jobs, _, _ := h.svc.ListJobs(ctx, app.JobFilter{Direction: domain.Import, TenantWide: true, Kind: &termbase}, pagination.Page{Size: pagination.MaxPageSize}); len(jobs) != 0 {
		t.Errorf("workspace termbase imports = %d", len(jobs))
	}

	ex, _, err := h.svc.CreateKnowledgeExport(ctx, domain.KindTermbase, domain.Options{}, "")
	if err != nil || ex.ProjectID != nil || ex.Format != domain.FormatTBX {
		t.Fatalf("termbase export = %+v %v", ex, err)
	}
	if _, _, err := h.svc.CreateKnowledgeExport(ctx, domain.KindTermbase, domain.Options{Locales: []string{"de"}}, ""); !errors.Is(err, domain.ErrInvalidOptions) {
		t.Errorf("TBX with locales: err = %v", err)
	}
	tmxOut, data := h.exportKnowledge(t, ctx, domain.KindTM)
	if tmxOut.Summary.Written != 2 || !strings.Contains(string(data), "Änderungen speichern") {
		t.Errorf("workspace TMX export wrote %d units", tmxOut.Summary.Written)
	}
}

// exportKnowledge exports the workspace's memory or termbase and
// downloads the file.
func (h *harness) exportKnowledge(t *testing.T, ctx context.Context, k domain.Kind) (domain.Job, []byte) {
	t.Helper()
	j, _, err := h.svc.CreateKnowledgeExport(ctx, k, domain.Options{}, "")
	if err != nil {
		t.Fatalf("create export: %v", err)
	}
	h.work(t)
	rc, j, err := h.svc.OpenExport(ctx, j.ID)
	if err != nil {
		t.Fatalf("download export (%s %s %s): %v", j.State, j.FailureCode, j.FailureMessage, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return j, b
}
