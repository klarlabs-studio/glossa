//go:build integration

package app_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	knowledgeapp "go.klarlabs.de/glossa/platform/internal/knowledge/app"
	localizationdomain "go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// xliffDoc is an XLIFF 2.1 document of plain units: key, source,
// target, state.
func xliffDoc(target string, units ...[4]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en" trgLang="%s">
  <file id="f1">
`, target)
	for _, u := range units {
		fmt.Fprintf(&b, `    <unit id="%s" name="%s"><segment state="%s"><source>%s</source><target>%s</target></segment></unit>
`, u[0], u[0], u[3], u[1], u[2])
	}
	b.WriteString("  </file>\n</xliff>\n")
	return b.String()
}

var shop = map[string]string{
	"home.title": "Welcome",
	"cart.empty": "Your cart is empty",
	"cart.full":  "Your cart is full",
}

// An XLIFF file's review states are kept as far as its importer may
// decide them, and merge never replaces an approved translation.
func TestXLIFFImportMapsStatesAndKeepsApprovedText(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", true, []string{"de", "fr"}, shop)
	h.translate(t, p, "home.title", "de", "Willkommen", "approved")
	file := xliffDoc("de",
		[4]string{"home.title", "Welcome", "Hallo", "final"},
		[4]string{"cart.empty", "Your cart is empty", "Dein Warenkorb ist leer", "final"},
		[4]string{"cart.full", "Your cart is full", "Dein Warenkorb ist voll", "translated"},
		[4]string{"brand.new", "Brand new", "Ganz neu", "translated"},
	)
	reviewer := h.as([]string{"reviewer"}, "de")
	j := h.importFile(t, reviewer, app.ImportRequest{ProjectID: &p, Format: "xliff", FileName: "shop.de.xlf"}, file)
	if j.State != domain.StateSucceeded || j.Mode != domain.ModeMerge || j.ProcessedItems != 4 || j.TotalItems != 4 {
		t.Fatalf("job = %+v", j)
	}
	got := byKey(h.results(t, reviewer, j.ID))
	for key, want := range map[string]struct {
		status domain.ItemStatus
		code   string
	}{
		"message home.title ":       {domain.ItemUnchanged, ""},
		"translation home.title de": {domain.ItemConflict, domain.CodeApprovedConflict},
		"translation cart.empty de": {domain.ItemCreated, ""},
		"translation cart.full de":  {domain.ItemCreated, ""},
		"message brand.new ":        {domain.ItemInvalid, domain.CodeForbidden},
		"translation brand.new de":  {domain.ItemInvalid, domain.CodeMessageNotFound},
	} {
		if it := got[key]; it.Status != want.status || it.Code != want.code {
			t.Errorf("%s = %+v, want %s %s", key, it, want.status, want.code)
		}
	}
	if j.Summary.Created != 2 || j.Summary.Conflict != 1 || j.Summary.Invalid != 2 || j.Summary.ByKind[domain.ItemTranslation].Total() != 4 {
		t.Errorf("summary = %+v", j.Summary)
	}
	ctx := h.owner()
	state := func(key string) localizationdomain.ReviewState {
		tr, err := h.localization.GetTranslation(ctx, p, key, "de")
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return tr.State
	}
	if tr, _ := h.localization.GetTranslation(ctx, p, "home.title", "de"); tr.Content.Text != "Willkommen" || tr.State != localizationdomain.StateApproved {
		t.Errorf("approved translation changed: %+v", tr.Translation)
	}
	if state("cart.empty") != localizationdomain.StateApproved || state("cart.full") != localizationdomain.StateNeedsReview {
		t.Errorf("states: cart.empty %s, cart.full %s", state("cart.empty"), state("cart.full"))
	}
	var origin, job, file2 string
	if err := env.Super.QueryRow(context.Background(), `SELECT origin, origin_detail->>'job', origin_detail->>'file'
		FROM localization_translation_revisions WHERE origin = 'import' LIMIT 1`).Scan(&origin, &job, &file2); err != nil ||
		job != j.ID.String() || file2 != "shop.de.xlf" {
		t.Errorf("provenance = %s %s %s %v", origin, job, file2, err)
	}

	// A translator may not approve under review_required: final is
	// capped to needs_review. Overwrite replaces approved text, for a
	// manager only.
	translator := h.as([]string{"translator"}, "de")
	j2 := h.importFile(t, translator, app.ImportRequest{ProjectID: &p, Format: "xliff", Mode: "merge"},
		xliffDoc("de", [4]string{"cart.full", "Your cart is full", "Warenkorb voll", "final"}))
	if it := byKey(h.results(t, translator, j2.ID))["translation cart.full de"]; it.Status != domain.ItemUpdated {
		t.Errorf("translator import = %+v", it)
	}
	if state("cart.full") != localizationdomain.StateNeedsReview {
		t.Errorf("a translator's final became %s", state("cart.full"))
	}
	if _, _, err := h.svc.CreateImport(translator, app.ImportRequest{ProjectID: &p, Format: "xliff", Mode: "overwrite"}, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator overwrite: %v", err)
	}
	over := h.importFile(t, h.developer(), app.ImportRequest{ProjectID: &p, Format: "xliff", Mode: "overwrite"},
		xliffDoc("de", [4]string{"home.title", "Welcome", "Hallo", "final"}))
	if it := byKey(h.results(t, ctx, over.ID))["translation home.title de"]; it.Status != domain.ItemUpdated {
		t.Errorf("overwrite = %+v", it)
	}
	if tr, _ := h.localization.GetTranslation(ctx, p, "home.title", "de"); tr.Content.Text != "Hallo" || tr.Origin != localizationdomain.OriginImport {
		t.Errorf("overwritten translation = %+v", tr.Translation)
	}

	// A translator limited to fr imports nothing into de.
	fr := h.as([]string{"translator"}, "fr")
	j3 := h.importFile(t, fr, app.ImportRequest{ProjectID: &p, Format: "xliff"},
		xliffDoc("de", [4]string{"cart.empty", "Your cart is empty", "Leer", "translated"}))
	if it := byKey(h.results(t, fr, j3.ID))["translation cart.empty de"]; it.Status != domain.ItemInvalid || it.Code != domain.CodeForbidden {
		t.Errorf("out-of-scope import = %+v", it)
	}
	if _, _, err := h.svc.CreateImport(fr, app.ImportRequest{Format: "tmx"}, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator TMX import: %v", err)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'integration.import.completed'"); n != 4 {
		t.Errorf("completion events = %d", n)
	}
}

// A nested JSON catalog in the source locale creates messages; the same
// file again reuses the first job's result.
func TestJSONNestedImportCreatesMessagesOnce(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "web", false, []string{"de"}, nil)
	ctx := h.developer()
	file := `{"checkout": {"title": "Checkout", "pay": "Pay {amount, number}"}, "home": {"title": "Welcome"}}`
	req := app.ImportRequest{ProjectID: &p, Format: "json", Options: domain.Options{Namespace: "web"}}
	j := h.importFile(t, ctx, req, file)
	if j.State != domain.StateSucceeded || j.Summary.Created != 3 {
		t.Fatalf("job = %+v", j)
	}
	m, err := h.catalog.GetMessage(ctx, catalogdomain.ProjectID(p), "checkout.pay")
	if err != nil || m.Namespace != "web" || m.Source.Text != "Pay {amount, number}" || m.Source.Syntax != "mf1" {
		t.Fatalf("message = %+v %v", m, err)
	}
	again := h.importFile(t, ctx, req, file)
	if again.ReusedJobID == nil || *again.ReusedJobID != j.ID || again.State != domain.StateSucceeded || again.Summary.Created != 3 {
		t.Errorf("same file again = %+v", again)
	}
	if items := h.results(t, ctx, again.ID); len(items) != 3 {
		t.Errorf("reused results = %d", len(items))
	}
	// Translations into de from a translator: needs_review by default.
	de := h.importFile(t, h.as([]string{"translator"}, "de"), app.ImportRequest{ProjectID: &p, Format: "json", Options: domain.Options{Locale: "de"}},
		`{"checkout.title": "Kasse", "home.title": "Willkommen", "nope": "x"}`)
	got := byKey(h.results(t, ctx, de.ID))
	if got["translation checkout.title de"].Status != domain.ItemCreated || got["translation nope de"].Code != domain.CodeMessageNotFound {
		t.Errorf("json translations = %+v", got)
	}
	if tr, _ := h.localization.GetTranslation(ctx, p, "checkout.title", "de"); tr.State != localizationdomain.StateNeedsReview {
		t.Errorf("state = %s", tr.State)
	}
	// A translator can't import a source catalog.
	if _, _, err := h.svc.CreateImport(h.as([]string{"translator"}, "de"), req, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator source import: %v", err)
	}
}

// Gettext plurals become MF2 selects with the target's CLDR categories;
// keys derive from msgid.
func TestPOImportWithPlurals(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "app", false, []string{"pl"}, nil)
	ctx := h.developer()
	file := `msgid ""
msgstr ""
"Language: pl\n"
"Content-Type: text/plain; charset=UTF-8\n"
"Plural-Forms: nplurals=3; plural=(n==1 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);\n"

msgid "Add to cart"
msgstr "Dodaj do koszyka"

msgid "One file"
msgid_plural "Many files"
msgstr[0] "Jeden plik"
msgstr[1] "Kilka plików"
msgstr[2] "Wiele plików"
`
	j := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "po"}, file)
	if j.State != domain.StateSucceeded || j.Summary.Created != 4 {
		t.Fatalf("job = %+v %+v", j, h.results(t, ctx, j.ID))
	}
	key := domain.POMessageKey("", "One file")
	m, err := h.catalog.GetMessage(ctx, catalogdomain.ProjectID(p), key)
	if err != nil || !m.Source.Model.IsSelect() {
		t.Fatalf("source %s = %+v %v", key, m, err)
	}
	tr, err := h.localization.GetTranslation(ctx, p, key, "pl")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, v := range tr.Content.Model.Variants {
		k := v.Keys[0].Value
		if v.Keys[0].Catchall {
			k = "*"
		}
		keys = append(keys, k)
	}
	if strings.Join(keys, ",") != "one,few,many,*" || tr.State != localizationdomain.StateApproved {
		t.Errorf("pl variants = %v, state %s", keys, tr.State)
	}
	if _, err := h.catalog.GetMessage(ctx, catalogdomain.ProjectID(p), domain.POMessageKey("", "Add to cart")); err != nil {
		t.Errorf("simple message: %v", err)
	}
}

// TMX units become translation memory that LookupTM finds; TBX concepts
// become a termbase that recognition uses.
func TestTMXAndTBXImportsFeedKnowledge(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "docs", false, []string{"de"}, nil)
	ctx := h.developer()
	tmxFile := `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4"><header creationtool="test" creationtoolversion="1" segtype="sentence" o-tmf="x" adminlang="en" srclang="en" datatype="plaintext"/>
<body>
<tu tuid="t1"><tuv xml:lang="en"><seg>Save changes</seg></tuv><tuv xml:lang="de"><seg>Änderungen speichern</seg></tuv></tu>
<tu tuid="t2"><tuv xml:lang="en"><seg>Save changes</seg></tuv><tuv xml:lang="de"><seg>Änderungen speichern</seg></tuv></tu>
</body></tmx>`
	j := h.importFile(t, ctx, app.ImportRequest{Format: "tmx", FileName: "memory.tmx"}, tmxFile)
	if j.State != domain.StateSucceeded || j.Summary.Created != 1 || j.Summary.Unchanged != 1 || j.ProjectID != nil {
		t.Fatalf("tmx job = %+v", j)
	}
	src, _ := mf.ParseMF2("Save changes")
	ms, err := h.knowledge.LookupTM(ctx, knowledgeapp.TMQuery{ProjectID: &p, SourceLocale: bcp47.MustParse("en"), TargetLocale: bcp47.MustParse("de"), Source: src})
	if err != nil || len(ms) != 1 || ms[0].Score != 100 || ms[0].TargetMF2 != "Änderungen speichern" {
		t.Errorf("lookup = %+v %v", ms, err)
	}

	tbxFile := `<?xml version="1.0" encoding="UTF-8"?>
<tbx xmlns="urn:iso:std:iso:30042:ed-2" type="TBX-Basic" style="dca" xml:lang="en">
<tbxHeader><fileDesc><sourceDesc><p>test</p></sourceDesc></fileDesc></tbxHeader>
<text><body>
<conceptEntry id="invoice">
  <descrip type="definition">A bill</descrip>
  <langSec xml:lang="en"><termSec><term>invoice</term><termNote type="administrativeStatus">preferredTerm-admn-sts</termNote></termSec></langSec>
  <langSec xml:lang="de"><termSec><term>Rechnung</term><termNote type="administrativeStatus">preferredTerm-admn-sts</termNote></termSec></langSec>
</conceptEntry>
</body></text></tbx>`
	tj := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "tbx"}, tbxFile)
	if tj.State != domain.StateSucceeded || tj.Summary.Created != 1 {
		t.Fatalf("tbx job = %+v %+v", tj, h.results(t, ctx, tj.ID))
	}
	hits, err := h.knowledge.RecognizeTerms(ctx, knowledgeapp.TermQuery{ProjectID: &p, Text: "Pay your invoices", Locale: bcp47.MustParse("en"),
		TargetLocale: ptr(bcp47.MustParse("de"))})
	if err != nil || len(hits) != 1 || len(hits[0].Targets) != 1 || hits[0].Targets[0].Text != "Rechnung" {
		t.Errorf("recognition = %+v %v", hits, err)
	}
	// The same termbase again is unchanged (the concept ID is derived
	// from the file's), a changed one a conflict in merge mode.
	again := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "tbx", Mode: "dry_run"}, tbxFile)
	if again.Summary.Unchanged != 1 {
		t.Errorf("re-import = %+v", again.Summary)
	}
	changed := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "tbx"}, strings.Replace(tbxFile, "A bill", "A request for payment", 1))
	if changed.Summary.Conflict != 1 {
		t.Errorf("changed concept = %+v", changed.Summary)
	}
}

// A dry run reports and writes nothing to the catalog, translations or
// knowledge.
func TestDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, shop)
	h.translate(t, p, "home.title", "de", "Willkommen", "approved")
	h.drain(t)
	before := map[string]int{}
	tables := []string{"catalog_messages", "catalog_source_revisions", "localization_translations",
		"localization_translation_revisions", "knowledge_tm_units", "knowledge_concepts"}
	for _, tbl := range tables {
		before[tbl] = count(t, "SELECT count(*) FROM "+tbl)
	}
	events := count(t, "SELECT count(*) FROM outbox_events WHERE event_type NOT LIKE 'integration.%'")
	j := h.importFile(t, h.developer(), app.ImportRequest{ProjectID: &p, Format: "xliff", Mode: "dry_run"},
		xliffDoc("de",
			[4]string{"home.title", "Welcome", "Hallo", "final"},
			[4]string{"cart.empty", "Your cart is empty", "Leer", "final"},
			[4]string{"brand.new", "Brand new", "Neu", "final"},
			[4]string{"Bad Key", "x", "y", "final"},
		))
	got := byKey(h.results(t, h.owner(), j.ID))
	if j.State != domain.StateSucceeded || got["translation home.title de"].Status != domain.ItemConflict ||
		got["translation cart.empty de"].Status != domain.ItemCreated || got["message brand.new "].Status != domain.ItemCreated ||
		got["translation brand.new de"].Status != domain.ItemCreated || got["message Bad Key "].Code != "invalid_message_key" {
		t.Errorf("dry run results = %+v", got)
	}
	for _, tbl := range tables {
		if n := count(t, "SELECT count(*) FROM "+tbl); n != before[tbl] {
			t.Errorf("%s: %d rows, was %d", tbl, n, before[tbl])
		}
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type NOT LIKE 'integration.%'"); n != events {
		t.Errorf("a dry run published %d events", n-events)
	}
	// A dry run is never reused: run it again and it runs again.
	again := h.importFile(t, h.developer(), app.ImportRequest{ProjectID: &p, Format: "xliff", Mode: "dry_run"},
		xliffDoc("de", [4]string{"home.title", "Welcome", "Hallo", "final"}))
	if again.ReusedJobID != nil {
		t.Error("a dry run reused a result")
	}
}

// An XLIFF export imports back unchanged; a JSON export has the shape
// `glossa pull` writes.
func TestExportsRoundTrip(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de", "fr"}, map[string]string{
		"checkout.pay": "Pay {amount, number}",
		"cart.items":   "{count, plural, one {# item} other {# items}}",
		"home.title":   "Welcome <b>friend</b>",
	})
	h.translate(t, p, "checkout.pay", "de", "{amount, number} bezahlen", "approved")
	h.translate(t, p, "cart.items", "de", "{count, plural, one {# Artikel} other {# Artikel}}", "needs_review")
	h.translate(t, p, "home.title", "de", "Willkommen <b>Freund</b>", "approved")
	h.translate(t, p, "home.title", "fr", "Bienvenue", "approved")
	ctx := h.developer()

	j, xlf := h.export(t, ctx, app.ExportRequest{ProjectID: &p, Format: "xliff", Options: domain.Options{
		Locales: []string{"de"}, States: []string{"approved", "needs_review"},
	}})
	if j.FileName != "shop.de.xlf" || j.Summary.Written != 3 || j.File.ContentType != "application/xliff+xml" {
		t.Errorf("export job = %+v", j)
	}
	back := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "xliff", FileName: j.FileName}, string(xlf))
	if back.Summary.Unchanged != back.Summary.Total() || back.Summary.Total() != 6 {
		t.Errorf("round trip = %+v\n%s\n%+v", back.Summary, xlf, h.results(t, ctx, back.ID))
	}

	_, js := h.export(t, ctx, app.ExportRequest{ProjectID: &p, Format: "json", Options: domain.Options{Locales: []string{"de"}}})
	want, err := cliPull(map[string]string{"checkout.pay": "{amount, number} bezahlen", "home.title": "Willkommen <b>Freund</b>"})
	if err != nil || string(js) != string(want) {
		t.Errorf("json export:\n%s\nwant (glossa pull):\n%s", js, want)
	}

	zj, zipped := h.export(t, ctx, app.ExportRequest{ProjectID: &p, Format: "json", Options: domain.Options{Locales: []string{"de", "fr"}, Layout: "nested"}})
	if zj.File.ContentType != domain.ContentTypeZip || zj.FileName != "shop.json.zip" || len(zipped) < 4 || string(zipped[:2]) != "PK" {
		t.Errorf("zip export = %+v", zj)
	}
	if _, _, err := h.svc.CreateExport(ctx, app.ExportRequest{ProjectID: &p, Format: "xliff", Options: domain.Options{Locales: []string{"it"}}}, ""); !errors.Is(err, app.ErrLocaleNotFound) {
		t.Errorf("unknown locale: %v", err)
	}
	if _, _, err := h.svc.CreateExport(ctx, app.ExportRequest{ProjectID: &p, Format: "po"}, ""); !errors.Is(err, domain.ErrNotExportable) {
		t.Errorf("po export: %v", err)
	}
}

// TMX and TBX exports come back through their imports unchanged.
func TestKnowledgeExportsRoundTrip(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, map[string]string{"save": "Save changes"})
	h.translate(t, p, "save", "de", "Änderungen speichern", "approved")
	h.drain(t) // derives the TM unit
	ctx := h.developer()
	if _, _, err := h.knowledge.CreateConcept(ctx, &p, knowledgeTerm("invoice", "Rechnung"), ""); err != nil {
		t.Fatal(err)
	}
	tj, tmxFile := h.export(t, ctx, app.ExportRequest{ProjectID: &p, Format: "tmx"})
	if tj.Summary.Written != 1 || !strings.Contains(string(tmxFile), "x-glossa-message-key") {
		t.Fatalf("tmx export = %+v\n%s", tj, tmxFile)
	}
	back := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "tmx"}, string(tmxFile))
	if back.Summary.Unchanged != 1 {
		t.Errorf("tmx round trip = %+v", back.Summary)
	}
	bj, tbxFile := h.export(t, ctx, app.ExportRequest{ProjectID: &p, Format: "tbx"})
	if bj.Summary.Written != 1 {
		t.Fatalf("tbx export = %+v", bj)
	}
	tb := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "tbx"}, string(tbxFile))
	if tb.Summary.Unchanged != 1 {
		t.Errorf("tbx round trip = %+v %+v", tb.Summary, h.results(t, ctx, tb.ID))
	}
}

// Jobs are cancelled, their files expire, and a malformed file fails its
// job with the problem's position as a result.
func TestLifecycleRetentionAndBadFiles(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, shop)
	ctx := h.developer()

	waiting, _, err := h.svc.CreateImport(ctx, app.ImportRequest{ProjectID: &p, Format: "xliff"}, "k1")
	if err != nil {
		t.Fatal(err)
	}
	replay, replayed, err := h.svc.CreateImport(ctx, app.ImportRequest{ProjectID: &p, Format: "xliff"}, "k1")
	if err != nil || !replayed || replay.ID != waiting.ID {
		t.Errorf("idempotent create = %v %v", replayed, err)
	}
	if _, err := h.svc.UploadImport(h.developer(), waiting.ID, strings.NewReader("x")); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("someone else's upload: %v", err)
	}
	if _, err := h.svc.UploadImport(ctx, waiting.ID, strings.NewReader("")); !errors.Is(err, app.ErrEmptyUpload) {
		t.Errorf("empty upload: %v", err)
	}
	if _, err := h.svc.UploadImport(ctx, waiting.ID, strings.NewReader(strings.Repeat("x", 1<<20+1))); !errors.Is(err, app.ErrUploadTooLarge) {
		t.Errorf("oversized upload: %v", err)
	}
	cancelled, err := h.svc.Cancel(ctx, waiting.ID, domain.Import)
	if err != nil || cancelled.State != domain.StateCancelled {
		t.Fatalf("cancel = %+v %v", cancelled, err)
	}
	if _, err := h.svc.UploadImport(ctx, waiting.ID, strings.NewReader("<x/>")); !errors.Is(err, domain.ErrUploadNotExpected) {
		t.Errorf("upload after cancel: %v", err)
	}

	bad := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "json", Options: domain.Options{Locale: "de"}},
		"{\n  \"home.title\": \"Hallo\",\n  \"home.title\": \"Doppelt\"\n}")
	items := h.results(t, ctx, bad.ID)
	if bad.State != domain.StateFailed || bad.FailureCode != domain.FailureInvalidFile || len(items) != 1 ||
		items[0].Line != 3 || items[0].Column == 0 || items[0].Status != domain.ItemInvalid {
		t.Errorf("bad file = %+v %+v", bad, items)
	}
	wrong := h.importFile(t, ctx, app.ImportRequest{ProjectID: &p, Format: "xliff"},
		strings.Replace(xliffDoc("de", [4]string{"home.title", "Welcome", "Hallo", "final"}), `srcLang="en"`, `srcLang="fr"`, 1))
	if wrong.FailureCode != domain.FailureSourceLocale {
		t.Errorf("source locale mismatch = %+v", wrong)
	}

	queued, _, err := h.svc.CreateExport(ctx, app.ExportRequest{ProjectID: &p, Format: "xliff"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if c, err := h.svc.Cancel(h.as([]string{"admin"}), queued.ID, domain.Export); err != nil || c.State != domain.StateCancelled {
		t.Errorf("a manager cancels a queued export: %+v %v", c, err)
	}
	if _, err := h.svc.Cancel(h.as([]string{"translator"}, "de"), queued.ID, domain.Export); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("someone else cancels: %v", err)
	}
	ej, _ := h.export(t, ctx, app.ExportRequest{ProjectID: &p, Format: "json"})
	if _, err := h.svc.Cancel(ctx, ej.ID, domain.Export); !errors.Is(err, domain.ErrNotCancellable) {
		t.Errorf("cancel a finished export: %v", err)
	}
	if _, err := env.Super.Exec(context.Background(), "UPDATE integration_jobs SET expires_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if err := h.worker.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.OpenExport(ctx, ej.ID); !errors.Is(err, domain.ErrFileExpired) {
		t.Errorf("download after retention: %v", err)
	}
	if ok, _ := objects.Exists(context.Background(), ej.File.Key); ok {
		t.Error("retention left the export in storage")
	}
	if n := count(t, "SELECT count(*) FROM integration_jobs WHERE files_deleted_at IS NULL"); n != 0 {
		t.Errorf("%d jobs still hold files", n)
	}

	// Deleting the project erases its jobs.
	proj, err := h.catalog.GetProject(ctx, catalogdomain.ProjectID(p))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.catalog.DeleteProject(h.owner(), catalogdomain.ProjectID(p), &proj.Version); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	if n := count(t, "SELECT count(*) FROM integration_jobs WHERE project_id = $1", p); n != 0 {
		t.Errorf("%d jobs outlived their project", n)
	}
}

// Another tenant sees none of the jobs.
func TestJobsAreTenantScoped(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, shop)
	j, _, err := h.svc.CreateImport(h.developer(), app.ImportRequest{ProjectID: &p, Format: "xliff"}, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := env.SeedTenant(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	intruder := authzMember(other, "owner")
	if _, err := h.svc.GetImport(intruder, j.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("other tenant reads the job: %v", err)
	}
	if _, err := h.svc.Cancel(intruder, j.ID, domain.Import); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("other tenant cancels the job: %v", err)
	}
	if n := count(t, "SELECT count(*) FROM integration_jobs WHERE id = $1", j.ID); n != 1 {
		t.Fatal("job missing")
	}
	_ = uuid.Nil
}
