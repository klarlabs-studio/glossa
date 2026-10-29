package messageformat

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestParseDecimal(t *testing.T) {
	for _, s := range []string{
		"0", "-0", "7", "-7", "0.5", "-0.05", "12345678901234.56",
		"9007199254740993", "1e3", "1E3", "1.5e-2", "2e+2", "100.00",
	} {
		d, err := ParseDecimal(s)
		if err != nil {
			t.Errorf("ParseDecimal(%q): %v", s, err)
			continue
		}
		if d.String() != s {
			t.Errorf("ParseDecimal(%q).String() = %q, want the literal unchanged", s, d.String())
		}
	}
}

func TestParseDecimalRejects(t *testing.T) {
	for _, s := range []string{
		"", " 1", "1 ", "+1", "01", "1.", ".5", "1e", "1e+", "--1", "1,5", "1_000",
		"NaN", "Infinity", "-Infinity", "0x10", "1e1001", "1e-1001",
		strings.Repeat("9", maxDecimalLength+1),
	} {
		if _, err := ParseDecimal(s); err == nil {
			t.Errorf("ParseDecimal(%q) accepted an invalid literal", s)
		} else if !errors.Is(err, ErrInvalidDecimal) {
			t.Errorf("ParseDecimal(%q) = %v, want ErrInvalidDecimal", s, err)
		}
	}
}

func TestDecimalZeroValue(t *testing.T) {
	var d Decimal
	if d.String() != "0" {
		t.Errorf("zero value = %q, want 0", d.String())
	}
}

func TestNewDecimal(t *testing.T) {
	tests := []struct {
		unscaled int64
		scale    int
		want     string
	}{
		{1999, 2, "19.99"},
		{-5, 2, "-0.05"},
		{0, 2, "0.00"},
		{7, 0, "7"},
		{7, -2, "700"},
		{0, -2, "0"},
		{1234567890123456, 2, "12345678901234.56"},
		{math.MinInt64, 0, "-9223372036854775808"},
		{math.MinInt64, 3, "-9223372036854775.808"},
	}
	for _, tt := range tests {
		if got := NewDecimal(tt.unscaled, tt.scale).String(); got != tt.want {
			t.Errorf("NewDecimal(%d, %d) = %q, want %q", tt.unscaled, tt.scale, got, tt.want)
		}
	}
}

func TestDecimalText(t *testing.T) {
	type invoice struct {
		Total Decimal `json:"total"`
	}
	b, err := json.Marshal(invoice{Total: MustParseDecimal("12345678901234.56")})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"total":"12345678901234.56"}` {
		t.Errorf("marshal = %s", b)
	}
	var back invoice
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Total.String() != "12345678901234.56" {
		t.Errorf("round trip = %q", back.Total)
	}
	if err := json.Unmarshal([]byte(`{"total":"1.2.3"}`), &back); err == nil {
		t.Error("unmarshal accepted an invalid decimal")
	}
}

func TestMustParseDecimalPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustParseDecimal did not panic on an invalid literal")
		}
	}()
	MustParseDecimal("1,5")
}

func TestDecimalArithmetic(t *testing.T) {
	d := MustParseDecimal
	tests := []struct {
		name string
		got  Decimal
		want string
	}{
		{"shift", d("0.256").shift(2), "25.6"},
		{"shift exponent", d("1.5e-2").shift(2), "1.5"},
		{"add", d("10000000000000000.5").add(-1), "9999999999999999.5"},
		{"add to integer", d("1e2").add(3), "103"},
		{"round half up", d("2.5").roundInteger(), "3"},
		{"round half away from zero", d("-2.5").roundInteger(), "-3"},
		{"round down", d("2.49").roundInteger(), "2"},
		{"round integer", d("12").roundInteger(), "12"},
		{"round small", d("0.4").roundInteger(), "0"},
		{"normalize", d("1.500").normalized(), "1.5"},
		{"normalize integer", d("100.00").normalized(), "100"},
		{"normalize exponent", d("1e3").normalized(), "1000"},
		{"normalize zero", d("-0.00").normalized(), "0"},
	}
	for _, tt := range tests {
		if tt.got.String() != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
	if !d("1.00").equal(d("1")) || d("1.01").equal(d("1")) || !d("1e2").equal(d("100.0")) {
		t.Error("equal compares numeric values")
	}
}
