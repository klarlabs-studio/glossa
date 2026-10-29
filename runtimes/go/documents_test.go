package glossa

// Documents (RFC 0004 §7.2, §12 item 4): a tax summary and a training
// export, rendered in de, en, es, fr and ja through html/template (th,
// num, percent, money, unit, date) and through Runs, against golden files
// in testdata/documents. Regenerate the bundle and the goldens with
//
//	go test -run TestDocument -update
//
// and review the diff. examples/pdf renders the same bundle and data to
// PDF.

import (
	"bytes"
	"encoding/json"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const documentsDir = "testdata/documents"

// documentLocales are the locales of the fixture; de is the source.
var documentLocales = []string{"de", "en", "es", "fr", "ja"}

// taxSummary is a Nexa-like tax summary. Amounts are Decimals: no money
// field may be a float (TestDocumentMoneyIsExact).
type taxSummary struct {
	Year          string
	Taxpayer      string
	FilingStatus  string
	PeriodStart   time.Time
	PeriodEnd     time.Time
	Due           time.Time
	Issued        time.Time
	Lines         []taxLine
	TotalReceipts int
	TotalEUR      Decimal
	TotalJPY      Decimal
}

type taxLine struct {
	Item     string // message ID
	Receipts int
	EUR      Decimal
	JPY      Decimal
}

// trainingExport is a Lexora-like export: dated rows with counts,
// percentages and units.
type trainingExport struct {
	Athlete       string
	From          time.Time
	To            time.Time
	ExportedAt    time.Time
	Rows          []exportRow
	TotalSessions int
}

type exportRow struct {
	Date       time.Time
	Sessions   int
	Reps       int
	Completion Decimal
	Distance   Decimal // kilometers
	Load       Decimal // kilograms
}

// documents are the fixture's documents: templates/<name>.html rendered
// with data/<name>.json.
var documents = []string{"tax-summary", "export"}

func loadDocumentData(t *testing.T, name string) any {
	t.Helper()
	var data any
	switch name {
	case "tax-summary":
		data = &taxSummary{}
	case "export":
		data = &trainingExport{}
	default:
		t.Fatalf("unknown document %q", name)
	}
	b, err := os.ReadFile(filepath.Join(documentsDir, "data", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(data); err != nil {
		t.Fatalf("%s.json: %v", name, err)
	}
	return data
}

func loadDocumentCatalogs(t *testing.T) map[string]map[string]string {
	t.Helper()
	catalogs := map[string]map[string]string{}
	for _, locale := range documentLocales {
		b, err := os.ReadFile(filepath.Join(documentsDir, "messages", locale+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var messages map[string]string
		if err := json.Unmarshal(b, &messages); err != nil {
			t.Fatalf("messages/%s.json: %v", locale, err)
		}
		catalogs[locale] = messages
	}
	return catalogs
}

// TestDocumentBundle keeps testdata/documents/bundle, a release in the
// layout of `glossa pull --release`, in step with messages/*.json, and
// every locale complete: a missing translation would render the German
// source without a report.
func TestDocumentBundle(t *testing.T) {
	catalogs := loadDocumentCatalogs(t)
	for _, locale := range documentLocales[1:] {
		source, target := keysOf(catalogs["de"]), keysOf(catalogs[locale])
		if !slices.Equal(source, target) {
			t.Errorf("messages/%s.json has %v, want the source's %v", locale, target, source)
		}
	}
	rel := buildRelease(t, "rel_documents", 1, catalogs, documentLocales...)
	rel.manifest = mutateJSON(t, rel.manifest, func(m map[string]any) { m["project"] = "prj_documents" })
	files := map[string][]byte{"manifest.json": rel.manifest}
	for digest, body := range rel.artifacts {
		files[filepath.Join("a", digest+".json")] = body
	}
	dir := filepath.Join(documentsDir, "bundle")
	if *updateGolden {
		writeBundle(t, dir, files)
		return
	}
	var onDisk []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			onDisk = append(onDisk, rel)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := keysOf(files); !slices.Equal(onDisk, want) {
		t.Fatalf("%s holds %v, want %v; run go test -run TestDocumentBundle -update", dir, onDisk, want)
	}
	for name, want := range files {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); !bytes.Equal(got, want) {
			t.Fatalf("%s/%s is stale; run go test -run TestDocumentBundle -update", dir, name)
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// documentLocalizer loads the committed bundle the way a service does,
// with bidi isolation off (left-to-right documents, RFC 0004 §7.2) and
// Europe/Berlin as the default time zone. Any reported error fails the
// test, so a missing message or a format error can't hide in a golden.
func documentLocalizer(t *testing.T, locale string) *Localizer {
	t.Helper()
	c := newTestClient(t, Config{Bundled: os.DirFS(filepath.Join(documentsDir, "bundle")), DisableBidiIsolation: true})
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	l := c.For(locale).WithTimeZone(berlin)
	if got := l.Locale(); got != locale {
		t.Fatalf("For(%q) resolved to %q", locale, got)
	}
	return l
}

// documentTemplates are parsed once with the placeholder functions and
// cloned per execution, as TemplateFuncs documents.
var documentTemplates = sync.OnceValues(func() (*htmltemplate.Template, error) {
	return htmltemplate.New("").Funcs(TemplateFuncs()).ParseFS(os.DirFS(documentsDir), "templates/*.html")
})

func renderDocument(t *testing.T, l *Localizer, name string, data any) string {
	t.Helper()
	base, err := documentTemplates()
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := base.Clone()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := tmpl.Funcs(l.FuncMap()).ExecuteTemplate(&b, name+".html", data); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return b.String()
}

func TestDocumentHTMLGolden(t *testing.T) {
	for _, name := range documents {
		data := loadDocumentData(t, name)
		for _, locale := range documentLocales {
			got := renderDocument(t, documentLocalizer(t, locale), name, data)
			checkGoldenFile(t, filepath.Join(documentsDir, "golden", name+"."+locale+".html"), []byte(got))
		}
	}
}

// documentRuns are the styled paragraphs a PDF renderer draws with Runs:
// the headings with bold labels, the paragraphs with bold, italic and
// underlined text, and both arms of the filing-status select.
func documentRuns(l *Localizer, tax *taxSummary, export *trainingExport) map[string][]Run {
	return map[string][]Run{
		"tax.title":          l.Runs("tax.title", Args{"year": tax.Year}),
		"tax.intro":          l.Runs("tax.intro", Args{"taxpayer": tax.Taxpayer}),
		"tax.status single":  l.Runs("tax.status", Args{"status": "single"}),
		"tax.status joint":   l.Runs("tax.status", Args{"status": "joint"}),
		"tax.total":          l.Runs("tax.total", nil),
		"tax.due":            l.Runs("tax.due", Args{"due": tax.Due}),
		"export.title":       l.Runs("export.title", Args{"athlete": export.Athlete}),
		"export.summary":     l.Runs("export.summary", Args{"count": export.TotalSessions, "from": export.From, "to": export.To}),
		"export.summary one": l.Runs("export.summary", Args{"count": 1, "from": export.From, "to": export.From}),
		"export.note":        l.Runs("export.note", nil),
		"export.footer":      l.Runs("export.footer", Args{"at": export.ExportedAt}),
	}
}

func TestDocumentRunsGolden(t *testing.T) {
	tax := loadDocumentData(t, "tax-summary").(*taxSummary)
	export := loadDocumentData(t, "export").(*trainingExport)
	for _, locale := range documentLocales {
		runs := documentRuns(documentLocalizer(t, locale), tax, export)
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(runs); err != nil {
			t.Fatal(err)
		}
		checkGoldenFile(t, filepath.Join(documentsDir, "golden", "runs."+locale+".json"), b.Bytes())
	}
}

// checkGoldenFile compares got with the golden file at path byte for
// byte, or rewrites the file with -update.
func checkGoldenFile(t *testing.T, path string, got []byte) {
	t.Helper()
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -run %s -update to create it)", err, t.Name())
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s differs; run go test -run %s -update and review the diff.\n got:\n%s", path, t.Name(), got)
	}
}

// A document gap is a place where a golden holds go-intl's output and
// CLDR (ICU, and so @klarlabs-studio/glossa) renders something else: the gaps of
// messageformat/README.md ("Engine gaps") that runtime_format_test.go
// skips. The goldens record what the Go runtime renders today; this list
// makes every divergence in them explicit. TestDocumentKnownGaps detects
// each gap kind in the rendered documents and fails on an occurrence that
// isn't listed, and on an entry that no longer occurs, which is how an
// upstream fix shows up (after go test -update).
type documentGap struct {
	document, locale string
	got              string // go-intl, as in the golden
	cldr             string // CLDR 48 (ICU)
	reason           string
}

const (
	gapPercent  = "go-intl: percent ignores the CLDR pattern #,##0\u00a0% (runtimeFormatSkips \"<locale> percent\")"
	gapGrouping = "go-intl: ignores CLDR minimumGroupingDigits=2 for es, so a four-digit integer part is grouped (runtimeFormatSkips \"es grouping …\")"
)

var documentGaps = []documentGap{
	{"export", "de", "100%", "100\u00a0%", gapPercent},
	{"export", "de", "92,5%", "92,5\u00a0%", gapPercent},
	{"export", "de", "80%", "80\u00a0%", gapPercent},
	{"export", "de", "45,6%", "45,6\u00a0%", gapPercent},
	{"export", "es", "100%", "100\u00a0%", gapPercent},
	{"export", "es", "92,5%", "92,5\u00a0%", gapPercent},
	{"export", "es", "80%", "80\u00a0%", gapPercent},
	{"export", "es", "45,6%", "45,6\u00a0%", gapPercent},
	{"export", "fr", "100%", "100\u00a0%", gapPercent},
	{"export", "fr", "92,5%", "92,5\u00a0%", gapPercent},
	{"export", "fr", "80%", "80\u00a0%", gapPercent},
	{"export", "fr", "45,6%", "45,6\u00a0%", gapPercent},
	{"tax-summary", "es", "1.234,50", "1234,50", gapGrouping},
	{"tax-summary", "es", "-3.200,00", "-3200,00", gapGrouping},
	{"export", "es", "1.234", "1234", gapGrouping},
	{"export", "es", "2.150", "2150", gapGrouping},
	{"export", "es", "1.520", "1520", gapGrouping},
}

var (
	// percentNoSpace matches a percentage whose sign touches the digits.
	percentNoSpace = regexp.MustCompile(`-?\d(?:[\d.,]*\d)?%`)
	// fourDigitGroup matches a number whose integer part has four digits,
	// grouped: 1.234 or -3.200,00, but not 12.345 or 1.234.567.
	fourDigitGroup = regexp.MustCompile(`(?:^|[^\d.,])(-?\d\.\d{3}(?:,\d+)?)(?:[^\d.,]|$)`)
)

// detectGaps finds the gap occurrences in a rendered document, with the
// CLDR rendering of each.
func detectGaps(locale, doc string) map[string]string {
	found := map[string]string{}
	switch locale {
	case "de", "es", "fr": // CLDR #,##0 %: a no-break space before the sign
		for _, m := range percentNoSpace.FindAllString(doc, -1) {
			found[m] = strings.TrimSuffix(m, "%") + "\u00a0%"
		}
	}
	if locale == "es" {
		for _, m := range fourDigitGroup.FindAllStringSubmatch(doc, -1) {
			found[m[1]] = strings.Replace(m[1], ".", "", 1)
		}
	}
	return found
}

func TestDocumentKnownGaps(t *testing.T) {
	listed := map[string]documentGap{}
	for _, g := range documentGaps {
		listed[g.document+" "+g.locale+" "+g.got] = g
	}
	seen := map[string]bool{}
	for _, name := range documents {
		data := loadDocumentData(t, name)
		for _, locale := range documentLocales {
			for got, cldr := range detectGaps(locale, renderDocument(t, documentLocalizer(t, locale), name, data)) {
				key := name + " " + locale + " " + got
				seen[key] = true
				g, ok := listed[key]
				switch {
				case !ok:
					t.Errorf("%s (%s) renders %q where CLDR has %q: an unrecorded go-intl gap; add it to documentGaps with its reason", name, locale, got, cldr)
				case g.cldr != cldr:
					t.Errorf("documentGaps entry %q: cldr %q, want %q", key, g.cldr, cldr)
				default:
					t.Logf("known gap in %s (%s): %q for %q: %s", name, locale, got, cldr, g.reason)
				}
			}
		}
	}
	for key := range listed {
		if !seen[key] {
			t.Errorf("documentGaps entry %q no longer occurs: remove it (and its runtimeFormatSkips entry if go-intl fixed the gap)", key)
		}
	}
}

// TestDocumentTimeZones pins what the goldens show: the due date is a
// date in Europe/Berlin (the Localizer default), not in UTC, and the
// issue time is in Asia/Tokyo (the placeholder's timeZone).
func TestDocumentTimeZones(t *testing.T) {
	data := loadDocumentData(t, "tax-summary")
	doc := renderDocument(t, documentLocalizer(t, "de"), "tax-summary", data)
	for _, want := range []string{
		"Zahlbar bis <b>1. Juni 2026</b>",                     // 2026-05-31T22:30Z
		"Erstellt am 15. März 2026 um 01:45 (Ortszeit Tokio)", // 2026-03-14T16:45Z
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("tax summary lacks %q", want)
		}
	}
}

// TestDocumentMoneyIsExact proves that no amount passes through a float:
// the document data has no float fields, the fixture's totals add up in
// exact arithmetic, and an amount beyond float64's precision renders
// digit for digit through the template.
func TestDocumentMoneyIsExact(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[taxSummary](), reflect.TypeFor[trainingExport]()} {
		if path, ok := floatField(typ, typ.Name(), map[reflect.Type]bool{}); ok {
			t.Errorf("%s is a float; use Decimal", path)
		}
	}

	tax := loadDocumentData(t, "tax-summary").(*taxSummary)
	eur, jpy := new(big.Rat), new(big.Rat)
	receipts := 0
	for _, line := range tax.Lines {
		eur.Add(eur, ratOf(t, line.EUR))
		jpy.Add(jpy, ratOf(t, line.JPY))
		receipts += line.Receipts
	}
	if eur.Cmp(ratOf(t, tax.TotalEUR)) != 0 || jpy.Cmp(ratOf(t, tax.TotalJPY)) != 0 || receipts != tax.TotalReceipts {
		t.Errorf("fixture totals %s EUR, %s JPY, %d receipts; the lines add up to %s EUR, %s JPY, %d receipts",
			tax.TotalEUR, tax.TotalJPY, tax.TotalReceipts, eur.FloatString(2), jpy.FloatString(0), receipts)
	}

	const amount = "98765432109876543.21" // float64: 98765432109876544
	if f := 98765432109876543.21; fmt.Sprintf("%.2f", f) == amount {
		t.Fatalf("float64 holds %s exactly; the test no longer shows the difference", amount)
	}
	huge := *tax
	huge.Lines = []taxLine{{Item: "tax.item.salary", Receipts: 1, EUR: MustParseDecimal(amount), JPY: MustParseDecimal("16098765432109876543")}}
	l := documentLocalizer(t, "de")
	doc := renderDocument(t, l, "tax-summary", &huge)
	for _, want := range []string{"98.765.432.109.876.543,21\u00a0€", "16.098.765.432.109.876.543\u00a0¥"} {
		if !strings.Contains(doc, want) {
			t.Errorf("tax summary lacks the exact amount %q", want)
		}
	}
	if float := l.Currency(98765432109876543.21, Opt("currency", "EUR")); strings.Contains(doc, float) {
		t.Errorf("the float64 rendering %q is in the document", float)
	}
}

// floatField reports the path of a float field in typ, if any.
func floatField(typ reflect.Type, path string, seen map[reflect.Type]bool) (string, bool) {
	if seen[typ] {
		return "", false
	}
	seen[typ] = true
	switch typ.Kind() {
	case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return path, true
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return floatField(typ.Elem(), path+"[]", seen)
	case reflect.Struct:
		for i := range typ.NumField() {
			f := typ.Field(i)
			if p, ok := floatField(f.Type, path+"."+f.Name, seen); ok {
				return p, true
			}
		}
	}
	return "", false
}

func ratOf(t *testing.T, d Decimal) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(d.String())
	if !ok {
		t.Fatalf("decimal %s", d)
	}
	return r
}
