package glossa

import (
	"encoding/json"
	"errors"
	"flag"
	htmltemplate "html/template"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata/")

// formatCatalogs hold sentences with the same expressions the standalone
// formatters build, so the two can be compared.
var formatCatalogs = map[string]map[string]string{
	"de": {
		"cell.number":   "{$value :number}",
		"cell.digits":   "{$value :number minimumFractionDigits=2}",
		"cell.percent":  "{$value :percent}",
		"cell.currency": "{$value :currency}",
		"cell.unit":     "{$value :unit unit=kilometer}",
		"cell.date":     "{$value :date length=long}",
		"cell.time":     "{$value :time}",
		"cell.datetime": "{$value :datetime}",
		"invoice.tax":   "Umsatzsteuer: {$tax}",
		"invoice.due":   "Fällig am {$due :date length=long} um {$due :time}",
		"invoice.utc":   "{$due :time timeZone=UTC} UTC",
		"invoice.lines": ".input {$n :number}\n.match $n\none {{{$n} Position}}\n* {{{$n} Positionen}}",
	},
	"en": {"invoice.tax": "VAT: {$tax}"},
	"es": {},
	"fr": {},
	"ja": {},
}

func formatClient(t *testing.T, log *errorLog) *Client {
	t.Helper()
	cfg := Config{Bundled: buildRelease(t, "rel_format", 1, formatCatalogs, "de", "en", "es", "fr", "ja").fs()}
	if log != nil {
		cfg.OnError = log.handle
	}
	return newTestClient(t, cfg)
}

var (
	taxAmount = Money{Amount: MustParseDecimal("12345678901234.56"), Currency: "EUR"}
	orderedAt = time.Date(2026, 9, 19, 14, 5, 0, 0, time.UTC)
)

func TestLargeTaxAmountSurvivesExactly(t *testing.T) {
	c := formatClient(t, nil)
	de := c.For("de")
	const want = "12.345.678.901.234,56 €"
	if got := de.Currency(taxAmount); got != want {
		t.Errorf("Currency = %q, want %q", got, want)
	}
	if got := de.T("invoice.tax", Args{"tax": taxAmount}, BidiIsolation(false)); got != "Umsatzsteuer: "+want {
		t.Errorf("T = %q", got)
	}
	if got := de.Number(taxAmount.Amount, Opt("minimumFractionDigits", "2")); got != "12.345.678.901.234,56" {
		t.Errorf("Number = %q", got)
	}
	parts := de.Parts("invoice.tax", Args{"tax": taxAmount}, BidiIsolation(false))
	if len(parts) != 2 || parts[1].Type != PartNumber || parts[1].Value != want {
		t.Fatalf("Parts = %+v", parts)
	}
	var fraction string
	for _, sub := range parts[1].Parts {
		if sub.Type == "fraction" {
			fraction = sub.Value
		}
	}
	if fraction != "56" {
		t.Errorf("fraction sub-part = %q, want 56", fraction)
	}
	// Beyond 15 significant digits a float64 loses the cents; a Decimal
	// doesn't.
	const huge = "1.234.567.890.123.456,78"
	if got := de.Number(MustParseDecimal("1234567890123456.78")); got != huge {
		t.Errorf("Number(Decimal) = %q, want %q", got, huge)
	}
	if got := de.Number(1234567890123456.78); got == huge {
		t.Errorf("float64 formatted exactly (%q); the test no longer shows the difference", got)
	}
}

func TestCellAndSentenceAgree(t *testing.T) {
	c := formatClient(t, nil)
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"de"} {
		l := c.For(locale).WithTimeZone(berlin)
		cells := []struct {
			id   string
			v    any
			cell string
		}{
			{"cell.number", MustParseDecimal("-1234567.891"), l.Number(MustParseDecimal("-1234567.891"))},
			{"cell.number", 42, l.Number(42)},
			{"cell.digits", MustParseDecimal("0.5"), l.Number(MustParseDecimal("0.5"), Opt("minimumFractionDigits", "2"))},
			{"cell.percent", MustParseDecimal("0.256"), l.Percent(MustParseDecimal("0.256"))},
			{"cell.currency", taxAmount, l.Currency(taxAmount)},
			{"cell.currency", money("1234567.5", "JPY"), l.Currency(money("1234567.5", "JPY"))},
			{"cell.unit", MustParseDecimal("12.5"), l.Unit(MustParseDecimal("12.5"), "kilometer")},
			{"cell.date", orderedAt, l.Date(orderedAt, Opt("length", "long"))},
			{"cell.time", orderedAt, l.Time(orderedAt)},
			{"cell.datetime", orderedAt, l.DateTime(orderedAt)},
		}
		for _, cell := range cells {
			sentence := l.T(cell.id, Args{"value": cell.v}, BidiIsolation(false))
			if sentence != cell.cell {
				t.Errorf("%s %s(%v): sentence %q, cell %q", locale, cell.id, cell.v, sentence, cell.cell)
			}
		}
	}
}

// TestFormatterGolden records the formatters' output in the five M3
// locales. Known go-intl v0.2.17 divergences from CLDR/ICU are in the
// golden file as they are (messageformat/README.md, "Engine gaps"): the
// de/es/fr percent lacks its no-break space ("26%" for "26 %"), and an
// alphabetic currency symbol or code isn't spaced from the digits
// ("USD19.99" for "USD 19.99" in en and ja).
func TestFormatterGolden(t *testing.T) {
	c := formatClient(t, nil)
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	d := MustParseDecimal
	calls := []struct {
		name string
		call func(l *Localizer) string
	}{
		{"number decimal", func(l *Localizer) string { return l.Number(d("-1234567.891")) }},
		{"number int", func(l *Localizer) string { return l.Number(1000000) }},
		{"number digits", func(l *Localizer) string { return l.Number(d("0.5"), Opt("minimumFractionDigits", "2")) }},
		{"number no grouping", func(l *Localizer) string { return l.Number(d("12345.6"), Opt("useGrouping", "never")) }},
		{"percent", func(l *Localizer) string { return l.Percent(d("0.256")) }},
		{"percent one digit", func(l *Localizer) string { return l.Percent(d("0.1234"), Opt("maximumFractionDigits", "1")) }},
		{"currency EUR tax", func(l *Localizer) string { return l.Currency(taxAmount) }},
		{"currency EUR negative", func(l *Localizer) string { return l.Currency(money("-0.5", "EUR")) }},
		{"currency JPY", func(l *Localizer) string { return l.Currency(money("1234567.5", "JPY")) }},
		{"currency code", func(l *Localizer) string {
			return l.Currency(money("19.99", "USD"), Opt("currencyDisplay", "code"))
		}},
		{"currency option", func(l *Localizer) string { return l.Currency(d("19.99"), Opt("currency", "CHF")) }},
		{"unit", func(l *Localizer) string { return l.Unit(d("12.5"), "kilometer") }},
		{"unit long", func(l *Localizer) string { return l.Unit(1, "liter", Opt("unitDisplay", "long")) }},
		{"date", func(l *Localizer) string { return l.Date(orderedAt) }},
		{"date long", func(l *Localizer) string { return l.Date(orderedAt, Opt("length", "long")) }},
		{"date short", func(l *Localizer) string { return l.Date(orderedAt, Opt("length", "short")) }},
		{"date weekday", func(l *Localizer) string {
			return l.Date(orderedAt, Opt("fields", "year-month-day-weekday"), Opt("length", "long"))
		}},
		{"time", func(l *Localizer) string { return l.Time(orderedAt) }},
		{"time tokyo", func(l *Localizer) string { return l.Time(orderedAt, TimeZone(tokyo)) }},
		{"datetime", func(l *Localizer) string { return l.DateTime(orderedAt) }},
		{"datetime long tokyo", func(l *Localizer) string {
			return l.DateTime(orderedAt, Opt("dateLength", "long"), TimeZone(tokyo))
		}},
	}
	got := map[string]map[string]string{}
	for _, locale := range []string{"de", "en", "es", "fr", "ja"} {
		got[locale] = map[string]string{}
		for _, call := range calls {
			got[locale][call.name] = call.call(c.For(locale))
		}
	}
	checkGolden(t, "testdata/formatters.golden.json", got)
}

// checkGolden compares got with the JSON golden file, or rewrites the file
// with -update.
func checkGolden(t *testing.T, path string, got any) {
	t.Helper()
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if *updateGolden {
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if string(want) != string(b) {
		t.Errorf("%s differs; run go test -run %s -update and review the diff.\n got:\n%s", path, t.Name(), b)
	}
}

func TestTimeZones(t *testing.T) {
	c := formatClient(t, nil)
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	args := Args{"due": orderedAt}
	de := c.For("de")
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"default is UTC", de.T("invoice.due", args, BidiIsolation(false)), "Fällig am 19. September 2026 um 14:05"},
		{"default is UTC whatever the value's location", de.T("invoice.due", Args{"due": orderedAt.In(tokyo)}, BidiIsolation(false)), "Fällig am 19. September 2026 um 14:05"},
		{"call option", de.T("invoice.due", args, BidiIsolation(false), TimeZone(tokyo)), "Fällig am 19. September 2026 um 23:05"},
		{"localizer default", de.WithTimeZone(berlin).T("invoice.due", args, BidiIsolation(false)), "Fällig am 19. September 2026 um 16:05"},
		{"call option beats the localizer default", de.WithTimeZone(berlin).T("invoice.due", args, BidiIsolation(false), TimeZone(tokyo)), "Fällig am 19. September 2026 um 23:05"},
		{"placeholder zone wins", de.WithTimeZone(berlin).T("invoice.utc", args, BidiIsolation(false)), "14:05 UTC"},
		{"parts", PartsText(de.Parts("invoice.due", args, BidiIsolation(false), TimeZone(tokyo))), "Fällig am 19. September 2026 um 23:05"},
		{"standalone default", de.Time(orderedAt.In(tokyo)), "14:05"},
		{"standalone localizer default", de.WithTimeZone(berlin).Time(orderedAt), "16:05"},
		{"standalone explicit zone", de.WithTimeZone(berlin).Time(orderedAt, Opt("timeZone", "Asia/Tokyo")), "23:05"},
		{"nil keeps the default", de.T("invoice.due", args, BidiIsolation(false), TimeZone(nil)), "Fällig am 19. September 2026 um 14:05"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, tt.got, tt.want)
		}
	}
	if de.WithTimeZone(berlin) == de {
		t.Error("WithTimeZone must return a new Localizer")
	}
}

func TestDecimalArgsSelect(t *testing.T) {
	c := formatClient(t, nil)
	de := c.For("de")
	for n, want := range map[string]string{"1": "1 Position", "2": "2 Positionen", "1.5": "1,5 Positionen"} {
		if got := de.T("invoice.lines", Args{"n": MustParseDecimal(n)}, BidiIsolation(false)); got != want {
			t.Errorf("n=%s: %q, want %q", n, got, want)
		}
	}
}

func TestFormatterErrors(t *testing.T) {
	log := &errorLog{}
	c := formatClient(t, log)
	de := c.For("de")
	if got := de.Currency(MustParseDecimal("5")); got != "{$value}" {
		t.Errorf("currency without a code = %q, want the MF2 fallback", got)
	}
	if got := de.Number("zwölf"); got != "{$value}" {
		t.Errorf("non-numeric = %q, want the MF2 fallback", got)
	}
	errs := log.all()
	if len(errs) != 2 {
		t.Fatalf("errors = %v, want two", errs)
	}
	for _, e := range errs {
		if e.Type != ErrorFormat || e.Locale != "de" || e.ReleaseID != "rel_format" || !strings.Contains(e.Detail, ":") {
			t.Errorf("error %+v", e)
		}
	}
}

func TestParseDecimalExported(t *testing.T) {
	if _, err := ParseDecimal("1,50"); !errors.Is(err, ErrInvalidDecimal) {
		t.Errorf("ParseDecimal(1,50) = %v, want ErrInvalidDecimal", err)
	}
	if got := NewDecimal(1999, 2).String(); got != "19.99" {
		t.Errorf("NewDecimal = %q", got)
	}
}

func TestTemplateFormatters(t *testing.T) {
	c := formatClient(t, nil)
	base := htmltemplate.Must(htmltemplate.New("invoice").Funcs(TemplateFuncs()).Parse(
		`<td>{{num .N}}</td><td>{{num .N "minimumFractionDigits" 2}}</td><td>{{percent .P}}</td>` +
			`<td>{{money .Tax}}</td><td>{{money .Net "EUR"}}</td><td>{{money .Net "JPY" "currencyDisplay" "code"}}</td>` +
			`<td>{{unit .N "kilometer"}}</td><td>{{date .At "length" "long"}}</td><td>{{time .At}}</td><td>{{datetime .At}}</td>`))
	data := map[string]any{
		"N": MustParseDecimal("1234.5"), "P": 0.256, "Tax": taxAmount, "Net": MustParseDecimal("99.5"), "At": orderedAt,
	}
	placeholder := new(strings.Builder)
	if err := htmltemplate.Must(base.Clone()).Execute(placeholder, data); err != nil {
		t.Fatalf("placeholders: %v", err)
	}
	var b strings.Builder
	if err := htmltemplate.Must(base.Clone()).Funcs(c.For("de").FuncMap()).Execute(&b, data); err != nil {
		t.Fatal(err)
	}
	// The German percent is compared with Percent itself: go-intl lacks the
	// CLDR no-break space there (runtime_format_test.go's skip).
	want := "<td>1.234,5</td><td>1.234,50</td><td>" + c.For("de").Percent(0.256) + "</td>" +
		"<td>12.345.678.901.234,56 €</td><td>99,50 €</td><td>100 JPY</td>" +
		"<td>1.234,5 km</td><td>19. September 2026</td><td>14:05</td><td>19. Sept. 2026, 14:05</td>"
	if b.String() != want {
		t.Errorf("template\n got %q\nwant %q", b.String(), want)
	}
}

func TestTemplateFormatterMisuse(t *testing.T) {
	log := &errorLog{}
	c := formatClient(t, log)
	tmpl := htmltemplate.Must(htmltemplate.New("x").Funcs(c.For("de").FuncMap()).Parse(`{{num 5 7 "x"}}|{{money 5}}`))
	var b strings.Builder
	if err := tmpl.Execute(&b, nil); err != nil {
		t.Fatal(err)
	}
	if b.String() != "5|{$value}" {
		t.Errorf("output %q", b.String())
	}
	if types := log.types(); len(types) != 2 || types[0] != ErrorFormat || types[1] != ErrorFormat {
		t.Errorf("errors %v, want a misuse report and a format error", log.all())
	}
}

// A template parsed with the placeholders must execute with FuncMap, so
// both have the same functions with the same signatures.
func TestTemplateFuncsMatchFuncMap(t *testing.T) {
	c := formatClient(t, nil)
	bound, placeholders := c.For("de").FuncMap(), TemplateFuncs()
	if len(bound) != len(placeholders) {
		t.Errorf("FuncMap has %d functions, TemplateFuncs %d", len(bound), len(placeholders))
	}
	for name, fn := range bound {
		if got, want := reflect.TypeOf(placeholders[name]), reflect.TypeOf(fn); got != want {
			t.Errorf("%s: placeholder %v, FuncMap %v", name, got, want)
		}
	}
}

func money(amount, currency string) Money {
	return Money{Amount: MustParseDecimal(amount), Currency: currency}
}
