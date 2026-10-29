package main

import (
	"embed"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"

	glossa "go.klarlabs.de/glossa/runtimes/go"
)

//go:embed fonts/*.ttf
var fontFiles embed.FS

// families are the embedded font families by fpdf style. Noto Sans JP has
// no italic, as CJK type doesn't slant, so its upright faces stand in.
var families = map[string]map[string]string{
	"NotoSans": {
		"": "NotoSans-Regular.ttf", "B": "NotoSans-Bold.ttf",
		"I": "NotoSans-Italic.ttf", "BI": "NotoSans-BoldItalic.ttf",
	},
	"NotoSansJP": {
		"": "NotoSansJP-Regular.ttf", "B": "NotoSansJP-Bold.ttf",
		"I": "NotoSansJP-Regular.ttf", "BI": "NotoSansJP-Bold.ttf",
	},
}

// familyFor picks the font family of a locale.
func familyFor(locale string) string {
	if locale == "ja" || strings.HasPrefix(locale, "ja-") {
		return "NotoSansJP"
	}
	return "NotoSans"
}

const (
	bodySize    = 10.0 // pt
	titleSize   = 16.0
	tableSize   = 9.0
	minCellSize = 6.0
	rowHeight   = 7.0 // mm
)

// lineHeight is the line height in mm for a font size in points.
func lineHeight(size float64) float64 { return size * 0.5 }

// renderDocuments draws both documents in l's locale.
func renderDocuments(l *glossa.Localizer, tax *taxSummary, export *trainingExport, compress bool) []*document {
	return []*document{renderTaxSummary(l, tax, compress), renderExport(l, export, compress)}
}

// document is a PDF being drawn in one locale.
type document struct {
	name   string // tax-summary or export
	pdf    *fpdf.Fpdf
	family string
	drawn  []string // every text drawn, for TestFontsCoverDocuments
}

// newDocument starts an A4 page with the locale's fonts. compress off
// leaves the content streams readable, for tests.
func newDocument(name string, l *glossa.Localizer, title string, created time.Time, compress bool) *document {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(compress)
	pdf.SetCreationDate(created) // reproducible output
	pdf.SetTitle(title, true)
	pdf.SetLang(l.Locale())
	family := familyFor(l.Locale())
	for _, style := range slices.Sorted(maps.Keys(families[family])) { // in order, for reproducible output
		b, err := fontFiles.ReadFile("fonts/" + families[family][style])
		if err != nil {
			pdf.SetError(err)
			break
		}
		pdf.AddUTF8FontFromBytes(family, style, b)
	}
	pdf.SetMargins(15, 15, 15)
	pdf.AddPage()
	return &document{name: name, pdf: pdf, family: family}
}

// paragraph writes styled runs as one flowing paragraph: each run's
// Style() is the fpdf font style, so a translation's {#b}…{/b} is bold.
func (d *document) paragraph(runs []glossa.Run, size float64) {
	for _, r := range runs {
		d.pdf.SetFont(d.family, r.Style(), size)
		d.pdf.Write(lineHeight(size), r.Text)
		d.drawn = append(d.drawn, r.Text)
	}
	d.pdf.Ln(lineHeight(size) * 1.6)
}

// column is a table column: its width in mm and fpdf alignment.
type column struct {
	width float64
	align string
}

// row draws one table row in style ("" or "B"). A text too wide for its
// column is set smaller, down to minCellSize, as long German compounds
// and 16-digit amounts need.
func (d *document) row(columns []column, style string, cells ...string) {
	for i, text := range cells {
		size := tableSize
		d.pdf.SetFont(d.family, style, size)
		room := columns[i].width - 2*d.pdf.GetCellMargin()
		for size > minCellSize && d.pdf.GetStringWidth(text) > room {
			size -= 0.5
			d.pdf.SetFontSize(size)
		}
		d.pdf.CellFormat(columns[i].width, rowHeight, text, "B", 0, columns[i].align, false, 0, "")
		d.drawn = append(d.drawn, text)
	}
	d.pdf.Ln(-1)
}

// plain joins runs into one text and returns their style when they all
// share it, for a table cell drawn in a single font.
func plain(runs []glossa.Run) (text, style string) {
	var b strings.Builder
	for i, r := range runs {
		b.WriteString(r.Text)
		if i == 0 {
			style = r.Style()
		} else if r.Style() != style {
			style = ""
		}
	}
	return b.String(), style
}

var (
	taxColumns = []column{{72, "L"}, {26, "L"}, {42, "R"}, {40, "R"}}
	expColumns = []column{{32, "L"}, {28, "L"}, {30, "R"}, {26, "R"}, {28, "R"}, {36, "R"}}
)

// renderTaxSummary draws the tax summary. Amounts are exact: Money with a
// Decimal amount, formatted with the currency's CLDR digits.
func renderTaxSummary(l *glossa.Localizer, tax *taxSummary, compress bool) *document {
	d := newDocument("tax-summary", l, l.T("tax.doctitle", glossa.Args{"year": tax.Year}), tax.Issued, compress)
	d.paragraph(l.Runs("tax.title", glossa.Args{"year": tax.Year}), titleSize)
	d.paragraph(l.Runs("tax.intro", glossa.Args{"taxpayer": tax.Taxpayer}), bodySize)
	d.paragraph(l.Runs("tax.status", glossa.Args{"status": tax.FilingStatus}), bodySize)
	d.paragraph(l.Runs("tax.period", glossa.Args{"from": tax.PeriodStart, "to": tax.PeriodEnd}), bodySize)

	d.row(taxColumns, "B", l.T("tax.col.item", nil), l.T("tax.col.receipts", nil), l.T("tax.col.eur", nil), l.T("tax.col.jpy", nil))
	for _, line := range tax.Lines {
		d.row(taxColumns, "", l.T(line.Item, nil),
			l.T("tax.receipts", glossa.Args{"count": line.Receipts}),
			l.Currency(glossa.Money{Amount: line.EUR, Currency: "EUR"}),
			l.Currency(glossa.Money{Amount: line.JPY, Currency: "JPY"}))
	}
	label, style := plain(l.Runs("tax.total", nil))
	d.row(taxColumns, style, label,
		l.T("tax.receipts", glossa.Args{"count": tax.TotalReceipts}),
		l.Currency(glossa.Money{Amount: tax.TotalEUR, Currency: "EUR"}),
		l.Currency(glossa.Money{Amount: tax.TotalJPY, Currency: "JPY"}))
	d.pdf.Ln(lineHeight(bodySize))

	d.paragraph(l.Runs("tax.due", glossa.Args{"due": tax.Due}), bodySize)
	d.paragraph(l.Runs("tax.issued", glossa.Args{"issued": tax.Issued}), bodySize)
	return d
}

// renderExport draws the training export: dates, counts, percentages and
// units, each cell formatted through the same engine as the sentences.
func renderExport(l *glossa.Localizer, export *trainingExport, compress bool) *document {
	d := newDocument("export", l, l.T("export.doctitle", glossa.Args{"athlete": export.Athlete}), export.ExportedAt, compress)
	d.paragraph(l.Runs("export.title", glossa.Args{"athlete": export.Athlete}), titleSize)
	d.paragraph(l.Runs("export.summary", glossa.Args{"count": export.TotalSessions, "from": export.From, "to": export.To}), bodySize)

	d.row(expColumns, "B", l.T("export.col.date", nil), l.T("export.col.sessions", nil), l.T("export.col.reps", nil),
		l.T("export.col.completion", nil), l.T("export.col.distance", nil), l.T("export.col.load", nil))
	for _, r := range export.Rows {
		d.row(expColumns, "", l.Date(r.Date),
			l.T("export.sessions", glossa.Args{"count": r.Sessions}),
			l.Number(r.Reps),
			l.Percent(r.Completion, glossa.Opt("maximumFractionDigits", "1")),
			l.Unit(r.Distance, "kilometer"),
			l.Unit(r.Load, "kilogram"))
	}
	d.pdf.Ln(lineHeight(bodySize))

	d.paragraph(l.Runs("export.note", nil), bodySize)
	d.paragraph(l.Runs("export.footer", glossa.Args{"at": export.ExportedAt}), bodySize)
	return d
}
