package messageformat

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// formatMF2 parses src and formats it without bidi isolation.
func formatMF2(t *testing.T, src, locale string, values map[string]any) (string, error) {
	t.Helper()
	msg, err := ParseMF2(src)
	if err != nil {
		t.Fatalf("ParseMF2(%q): %v", src, err)
	}
	return Format(msg, locale, values, WithBidiIsolation(false))
}

func TestFormatDecimalIsExact(t *testing.T) {
	d := MustParseDecimal
	tests := []struct {
		name, src, locale string
		values            map[string]any
		want              string
	}{
		{"large tax amount, de", "{$v :currency}", "de", map[string]any{"v": Money{d("12345678901234.56"), "EUR"}}, "12.345.678.901.234,56\u00a0€"},
		{"large tax amount, en", "{$v :currency}", "en", map[string]any{"v": Money{d("12345678901234.56"), "EUR"}}, "€12,345,678,901,234.56"},
		{"beyond float64 integers", "{$v :number}", "en", map[string]any{"v": d("9007199254740993")}, "9,007,199,254,740,993"},
		{"beyond float64 fractions", "{$v :number maximumFractionDigits=2}", "en", map[string]any{"v": d("1234567890123456789.01")}, "1,234,567,890,123,456,789.01"},
		{"half-even trap of binary floats", "{$v :number maximumFractionDigits=2}", "en", map[string]any{"v": d("1.005")}, "1.01"},
		{"currency digits: JPY has none", "{$v :currency}", "en", map[string]any{"v": Money{d("1234567.5"), "JPY"}}, "¥1,234,568"},
		{"currency digits: EUR has two", "{$v :currency}", "en", map[string]any{"v": Money{d("5"), "EUR"}}, "€5.00"},
		{"placeholder overrides digits", "{$v :currency fractionDigits=0}", "en", map[string]any{"v": Money{d("5.49"), "EUR"}}, "€5"},
		{"decimal with a currency option", "{$v :currency currency=EUR}", "de", map[string]any{"v": d("0.1")}, "0,10\u00a0€"},
		{"integer", "{$v :integer}", "en", map[string]any{"v": d("12345678901234567.5")}, "12,345,678,901,234,568"},
		{"percent", "{$v :percent}", "en", map[string]any{"v": d("0.256")}, "26%"},
		{"unit", "{$v :unit unit=kilometer}", "en", map[string]any{"v": d("1234.5")}, "1,234.5 km"},
		{"offset", ".input {$v :number}\n.local $w = {$v :offset subtract=1}\n{{{$w}}}", "en", map[string]any{"v": d("10000000000000000.5")}, "9,999,999,999,999,999.5"},
		{"unannotated decimal formats as :number", "Summe {$v}", "de", map[string]any{"v": d("1234.5")}, "Summe 1.234,5"},
		{"unannotated money formats as :currency", "Summe {$v}", "de", map[string]any{"v": Money{d("1234.5"), "EUR"}}, "Summe 1.234,50\u00a0€"},
		{"unannotated input declaration", ".input {$v}\n{{{$v}}}", "en", map[string]any{"v": d("1234.5")}, "1,234.5"},
		{"money under :number formats its amount", "{$v :number}", "en", map[string]any{"v": Money{d("1234.5"), "EUR"}}, "1,234.5"},
		{"options carry through a local", ".local $x = {$v :number minimumFractionDigits=2}\n{{{$x :currency currency=EUR}}}", "en", map[string]any{"v": d("5")}, "€5.00"},
		{"declared currency is not reformatted", ".input {$v :currency currency=EUR}\n{{{$v}}}", "en", map[string]any{"v": d("5")}, "€5.00"},
		{"string", "{$v :string}", "en", map[string]any{"v": d("1234.50")}, "1234.50"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatMF2(t, tt.src, tt.locale, tt.values)
			if err != nil {
				t.Fatalf("Format: %v (output %q)", err, got)
			}
			if got != tt.want {
				t.Errorf("Format = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectDecimal(t *testing.T) {
	d := MustParseDecimal
	plural := ".input {$n :number}\n.match $n\n0 {{zero}}\none {{one}}\n* {{other}}"
	tests := []struct {
		name, src, locale string
		n                 any
		want              string
	}{
		{"one", plural, "en", d("1"), "one"},
		{"exact key", plural, "en", d("0.00"), "zero"},
		{"fraction", plural, "en", d("1.5"), "other"},
		{"trailing zeros follow the digits shown", plural, "en", d("1.0"), "one"},
		{"visible fraction digit", ".input {$n :number minimumFractionDigits=1}\n.match $n\none {{one}}\n* {{other}}", "en", d("1"), "other"},
		{"beyond float64", plural, "en", d("9007199254740993"), "other"},
		{"french many", ".input {$n :number}\n.match $n\none {{one}}\nmany {{many}}\n* {{other}}", "fr", d("1000000"), "many"},
		{"integer rounds before selecting", ".input {$n :integer}\n.match $n\none {{one}}\n* {{other}}", "en", d("1.4"), "one"},
		{"percent selects on the percentage", ".input {$n :percent}\n.match $n\none {{one}}\n* {{other}}", "en", d("0.01"), "one"},
		{"offset", ".input {$n :number}\n.local $m = {$n :offset subtract=1}\n.match $m\none {{one}}\n* {{other}}", "en", d("2"), "one"},
		{"ordinal", ".input {$n :number select=ordinal}\n.match $n\none {{st}}\ntwo {{nd}}\nfew {{rd}}\n* {{th}}", "en", d("22"), "nd"},
		{"exact selection", ".input {$n :number select=exact}\n.match $n\n1 {{one}}\n* {{other}}", "en", d("1.00"), "one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatMF2(t, tt.src, tt.locale, map[string]any{"n": tt.n})
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			if got != tt.want {
				t.Errorf("Format = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDecimalMatchesEngine proves the exact path formats and selects like
// the engine wherever the engine is exact: integer operands, across the
// numeric functions, options and locales.
func TestDecimalMatchesEngine(t *testing.T) {
	exprs := []string{
		"{$v :number}",
		"{$v :number minimumFractionDigits=2}",
		"{$v :number maximumSignificantDigits=2}",
		"{$v :number signDisplay=always useGrouping=never}",
		"{$v :number minimumIntegerDigits=6}",
		"{$v :integer}",
		"{$v :percent}",
		"{$v :percent maximumFractionDigits=1 signDisplay=exceptZero}",
		"{$v :currency currency=EUR}",
		"{$v :currency currency=JPY currencyDisplay=name}",
		"{$v :currency currency=USD currencyDisplay=code currencySign=accounting}",
		"{$v :currency currency=CHF fractionDigits=0 trailingZeroDisplay=stripIfInteger}",
		"{$v :unit unit=kilometer-per-hour unitDisplay=long}",
		"{$v :unit unit=liter unitDisplay=narrow}",
		".local $w = {$v :offset add=2}\n{{{$w}}}",
		".local $w = {$v :number minimumFractionDigits=1}\n{{{$w :currency currency=EUR}}}",
		".input {$v :number}\n.match $v\n0 {{=0}}\nzero {{zero}}\none {{one}}\ntwo {{two}}\nfew {{few}}\nmany {{many}}\n* {{other}}",
		".input {$v :number select=ordinal}\n.match $v\none {{one}}\ntwo {{two}}\nfew {{few}}\nmany {{many}}\n* {{other}}",
		".input {$v :integer}\n.match $v\n1 {{=1}}\none {{one}}\nfew {{few}}\n* {{other}}",
	}
	locales := []string{"en", "de", "fr", "es", "ja", "ar", "he", "ru", "pl"}
	values := []int64{-1234, -1, 0, 1, 2, 3, 5, 11, 21, 101, 1000000, 1234567}
	for _, src := range exprs {
		msg, err := ParseMF2(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		for _, locale := range locales {
			for _, v := range values {
				want, wantErr := Format(msg, locale, map[string]any{"v": v})
				got, gotErr := Format(msg, locale, map[string]any{"v": NewDecimal(v, 0)})
				if got != want || !reflect.DeepEqual(errCodes(gotErr), errCodes(wantErr)) {
					t.Errorf("%q %s v=%d:\n decimal %q %v\n  engine %q %v", src, locale, v, got, gotErr, want, wantErr)
				}
			}
		}
	}
}

func errCodes(err error) []ErrorCode {
	if fe, ok := err.(*FormatError); ok {
		return fe.Codes()
	}
	if err != nil {
		return []ErrorCode{ErrorCode("<" + fmt.Sprint(err) + ">")}
	}
	return nil
}

func TestDecimalErrorsMatchEngine(t *testing.T) {
	for _, src := range []string{
		"{$v :number minimumFractionDigits=foo}",
		"{$v :currency}",
		"{$v :currency currency=EURO}",
		"{$v :percent useGrouping=|| minimumFractionDigits=x}",
		"{$v :offset}",
		"{$v :unit}",
		".local $s = {$v :number select=$v}\n.match $s\n* {{x}}",
	} {
		msg, err := ParseMF2(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		want, wantErr := Format(msg, "en", map[string]any{"v": 5})
		got, gotErr := Format(msg, "en", map[string]any{"v": NewDecimal(5, 0)})
		if got != want || !reflect.DeepEqual(errCodes(gotErr), errCodes(wantErr)) {
			t.Errorf("%q:\n decimal %q %v\n  engine %q %v", src, got, gotErr, want, wantErr)
		}
	}
}

func TestMoneyWithoutCurrency(t *testing.T) {
	got, err := formatMF2(t, "{$v :currency}", "en", map[string]any{"v": Money{Amount: NewDecimal(5, 0)}})
	if got != "{$v}" || !slices.Equal(errCodes(err), []ErrorCode{CodeBadOperand}) {
		t.Errorf("Format = %q, %v; want the fallback and a bad-operand error", got, err)
	}
}

func TestFormatToPartsDecimal(t *testing.T) {
	msg, err := ParseMF2("{$v :currency u:id=total}")
	if err != nil {
		t.Fatal(err)
	}
	parts, err := FormatToParts(msg, "de", map[string]any{"v": Money{MustParseDecimal("1234.5"), "EUR"}}, WithBidiIsolation(false))
	if err != nil {
		t.Fatal(err)
	}
	want := []Part{{
		Type: PartNumber, Value: "1.234,50\u00a0€", Source: "$v", Locale: "de", Dir: "ltr", ID: "total",
		Parts: []SubPart{
			{Type: "integer", Value: "1"}, {Type: "group", Value: "."}, {Type: "integer", Value: "234"},
			{Type: "decimal", Value: ","}, {Type: "fraction", Value: "50"},
			{Type: "literal", Value: "\u00a0"}, {Type: "currency", Value: "€"},
		},
	}}
	if !reflect.DeepEqual(parts, want) {
		t.Errorf("parts\n got %+v\nwant %+v", parts, want)
	}
	engine, err := FormatToParts(msg, "de", map[string]any{"v": map[string]any{"valueOf": 1234, "options": map[string]any{"currency": "EUR"}}}, WithBidiIsolation(false))
	if err != nil {
		t.Fatal(err)
	}
	engine[0].Value, engine[0].Parts = want[0].Value, want[0].Parts
	if !reflect.DeepEqual(parts, engine) {
		t.Errorf("part metadata differs from the engine's\n got %+v\nwant %+v", parts, engine)
	}
}
