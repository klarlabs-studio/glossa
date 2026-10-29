package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/image/font/sfnt"

	glossa "go.klarlabs.de/glossa/runtimes/go"
)

const docs = "../../testdata/documents"

// renderAll draws both documents in every locale, uncompressed so the
// text can be found in the output. Any runtime error fails the test.
func renderAll(t *testing.T) map[string]*document {
	t.Helper()
	client, err := newClient(docs, func(e glossa.Error) { t.Errorf("glossa: %+v", e) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	tax, export, err := loadData(docs)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*document{}
	for _, locale := range locales {
		l, err := localizer(client, locale)
		if err != nil {
			t.Fatal(err)
		}
		for _, doc := range renderDocuments(l, tax, export, false) {
			out[doc.name+"."+locale] = doc
		}
	}
	return out
}

// expectedText is text each PDF must contain, drawn from table cells and
// styled runs: plurals, exact EUR and JPY amounts, dates, percentages,
// units, bold labels and the long German compound. French groups digits
// and sets units off with U+202F, and puts U+00A0 before €. The de, es
// and fr percentages lack CLDR's no-break space, a known go-intl gap
// (documentGaps in runtimes/go/documents_test.go).
var expectedText = map[string][]string{
	"tax-summary.de": {"Steuerübersicht", "Kraftfahrzeughaftpflichtversicherungsbeiträge", "1 Beleg", "3 Belege", "12.345.678.899.519,06\u00a0€", "2.012.345.678.621.608\u00a0¥", "1. Juni 2026", "Zusammenveranlagung"},
	"tax-summary.en": {"Tax summary", "1 receipt", "3 receipts", "€12,345,678,899,519.06", "¥2,012,345,678,621,608", "June 1, 2026", "Married filing jointly"},
	"tax-summary.es": {"Resumen fiscal", "1 justificante", "3 justificantes", "12.345.678.899.519,06\u00a0€", "2.012.345.678.621.608\u00a0JPY", "1 de junio de 2026", "conjunta"},
	"tax-summary.fr": {"Récapitulatif fiscal", "0 justificatif", "3 justificatifs", "12\u202f345\u202f678\u202f899\u202f519,06\u00a0€", "2\u202f012\u202f345\u202f678\u202f621\u202f608\u00a0JPY", "1 juin 2026", "imposition commune"},
	"tax-summary.ja": {"税務サマリー", "自動車損害賠償責任保険料", "3件", "€12,345,678,899,519.06", "￥2,012,345,678,621,608", "2026年6月1日", "合算申告"},
	"export.de":      {"Trainingsexport", "7 Einheiten", "1 Einheit", "10. Aug. 2026", "1.234", "92,5%", "12,5 km", "12.345,5 kg", "GPS-Schätzungen"},
	"export.en":      {"Training export", "7 sessions", "1 session", "Aug 10, 2026", "1,234", "92.5%", "12.5 km", "12,345.5 kg", "GPS estimates"},
	"export.es":      {"Exportación de entrenamientos", "7 sesiones", "1 sesión", "10 ago 2026", "92,5%", "12,5 km", "12.345,5 kg", "estimaciones GPS"},
	"export.fr":      {"Export des entraînements", "7 séances", "1 séance", "10 août 2026", "1\u202f234", "92,5%", "12,5\u202fkm", "12\u202f345,5\u202fkg", "estimations GPS"},
	"export.ja":      {"トレーニングのエクスポート", "7回のセッション", "1回", "2026年8月10日", "1,234", "92.5%", "12.5 km", "12,345.5 kg", "GPSによる推定値"},
}

// TestRenderPDFs is the smoke test of RFC 0004 §12 item 4: every locale's
// documents render to a non-empty PDF that holds the expected text.
func TestRenderPDFs(t *testing.T) {
	dir := t.TempDir()
	docs := renderAll(t)
	if len(docs) != len(expectedText) {
		t.Errorf("rendered %d documents, expected text for %d", len(docs), len(expectedText))
	}
	for key, doc := range docs {
		path := filepath.Join(dir, key+".pdf")
		if err := doc.pdf.OutputFileAndClose(path); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(b, []byte("%PDF-")) || !bytes.HasSuffix(bytes.TrimSpace(b), []byte("%%EOF")) {
			t.Errorf("%s: not a PDF (%d bytes)", key, len(b))
			continue
		}
		for _, text := range expectedText[key] {
			if !bytes.Contains(b, pdfString(text)) {
				t.Errorf("%s: no %q in the PDF", key, text)
			}
		}
	}
}

// pdfString is text as fpdf writes it with a UTF-8 font: UTF-16BE code
// units in a PDF string, with \, ( and ) escaped.
func pdfString(s string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u>>8), byte(u))
	}
	return []byte(strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`, "\r", `\r`).Replace(string(b)))
}

// TestFontsCoverDocuments checks that the subset fonts have a glyph for
// every character the documents draw, in every face of the family. It
// fails when the fixture's Japanese text gains a kanji: rebuild the fonts
// with fonts/build.sh.
func TestFontsCoverDocuments(t *testing.T) {
	faces := map[string]*sfnt.Font{}
	for _, family := range families {
		for _, file := range family {
			b, err := fontFiles.ReadFile("fonts/" + file)
			if err != nil {
				t.Fatal(err)
			}
			if faces[file], err = sfnt.Parse(b); err != nil {
				t.Fatalf("%s: %v", file, err)
			}
		}
	}
	var buf sfnt.Buffer
	for key, doc := range renderAll(t) {
		missing := map[string][]rune{}
		for _, text := range doc.drawn {
			for _, r := range text {
				if r == '\n' {
					continue
				}
				for _, file := range families[doc.family] {
					if i, err := faces[file].GlyphIndex(&buf, r); err != nil || i == 0 {
						missing[file] = append(missing[file], r)
					}
				}
			}
		}
		for file, runes := range missing {
			t.Errorf("%s: %s has no glyph for %q; run fonts/build.sh", key, file, string(runes))
		}
	}
}

// Documents never pass an amount through a float: the data types hold
// Decimals, which the runtime formats digit for digit.
func TestAmountsAreExact(t *testing.T) {
	client, err := newClient(docs, func(e glossa.Error) { t.Errorf("glossa: %+v", e) })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	tax, export, err := loadData(docs)
	if err != nil {
		t.Fatal(err)
	}
	const amount = "98765432109876543.21" // beyond float64's 15–17 digits
	tax.Lines = []taxLine{{Item: "tax.item.salary", Receipts: 1, EUR: glossa.MustParseDecimal(amount), JPY: glossa.MustParseDecimal("0")}}
	l, err := localizer(client, "de")
	if err != nil {
		t.Fatal(err)
	}
	doc := renderDocuments(l, tax, export, false)[0]
	if want := "98.765.432.109.876.543,21\u00a0€"; !slices.Contains(doc.drawn, want) {
		t.Errorf("the tax summary lacks %q", want)
	}
}
