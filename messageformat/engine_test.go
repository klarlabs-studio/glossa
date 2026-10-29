package messageformat

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestParseMF2(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want Message
	}{
		{
			name: "simple text",
			src:  "Hallo Welt",
			want: Message{Type: PatternMessageType, Pattern: Pattern{Text("Hallo Welt")}},
		},
		{
			name: "empty",
			src:  "",
			want: Message{Type: PatternMessageType, Pattern: Pattern{}},
		},
		{
			name: "placeholder with function, options and attributes",
			src:  "Summe: {$total :currency currency=EUR @translate=no @x}",
			want: Message{Type: PatternMessageType, Pattern: Pattern{
				Text("Summe: "),
				Expression{
					Arg:        VariableRef{Name: "total"},
					Function:   &FunctionRef{Name: "currency", Options: Options{"currency": Literal{Value: "EUR"}}},
					Attributes: Attributes{"translate": &Literal{Value: "no"}, "x": nil},
				},
			}},
		},
		{
			name: "markup",
			src:  "{#b}fett{/b}{#br/}",
			want: Message{Type: PatternMessageType, Pattern: Pattern{
				Markup{Kind: MarkupOpen, Name: "b"},
				Text("fett"),
				Markup{Kind: MarkupClose, Name: "b"},
				Markup{Kind: MarkupStandalone, Name: "br"},
			}},
		},
		{
			name: "select with local",
			src:  ".input {$n :number}\n.local $m = {$n :offset subtract=1}\n.match $n $m\n0 * {{keine}}\n* one {{{$m} Datei}}\n* * {{{$m} Dateien}}",
			want: Message{
				Type: SelectMessageType,
				Declarations: []Declaration{
					{Type: InputDeclaration, Name: "n", Value: Expression{Arg: VariableRef{Name: "n"}, Function: &FunctionRef{Name: "number"}}},
					{Type: LocalDeclaration, Name: "m", Value: Expression{Arg: VariableRef{Name: "n"}, Function: &FunctionRef{Name: "offset", Options: Options{"subtract": Literal{Value: "1"}}}}},
				},
				Selectors: []VariableRef{{Name: "n"}, {Name: "m"}},
				Variants: []Variant{
					{Keys: []VariantKey{{Value: "0"}, {Catchall: true}}, Value: Pattern{Text("keine")}},
					{Keys: []VariantKey{{Catchall: true}, {Value: "one"}}, Value: Pattern{Expression{Arg: VariableRef{Name: "m"}}, Text(" Datei")}},
					{Keys: []VariantKey{{Catchall: true}, {Catchall: true}}, Value: Pattern{Expression{Arg: VariableRef{Name: "m"}}, Text(" Dateien")}},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMF2(tt.src)
			if err != nil {
				t.Fatalf("ParseMF2: %v", err)
			}
			if !reflect.DeepEqual(normalize(got), normalize(tt.want)) {
				t.Errorf("model mismatch\n got: %#v\nwant: %#v", got, tt.want)
			}
			assertMessageMatchesSchema(t, got)
		})
	}
}

func TestParseMF2Errors(t *testing.T) {
	tests := []struct {
		src  string
		want ErrorCode
	}{
		{"{", CodeSyntaxError},
		{"{$x :}", CodeSyntaxError},
		{".input {$foo :x} .match $foo * * {{foo}}", CodeVariantKeyMismatch},
		{".input {$foo :x} .match $foo one {{_}}", CodeMissingFallbackVariant},
		{".input {$foo} .match $foo one {{one}} * {{other}}", CodeMissingSelectorAnnotation},
		{".input {$foo} .input {$foo} {{_}}", CodeDuplicateDeclaration},
		{"{:number a=1 a=2}", CodeDuplicateOptionName},
		{".input {$x :string} .match $x * {{a}} * {{b}}", CodeDuplicateVariant},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			_, err := ParseMF2(tt.src)
			var mfErr *Error
			if !errors.As(err, &mfErr) {
				t.Fatalf("want *Error with code %s, got %v", tt.want, err)
			}
			if mfErr.Code != tt.want {
				t.Errorf("code = %s, want %s (%v)", mfErr.Code, tt.want, err)
			}
		})
	}
}

func TestStringifyRoundTrip(t *testing.T) {
	srcs := []string{
		"Hallo {$name}!",
		"{|literal with space|} and \\{escaped\\}",
		".input {$n :number}\n.match $n\none {{eine Datei}}\n* {{{$n} Dateien}}",
		"{#link href=|https://example.com|}Klick{/link}",
		".local $x = {1 :number minimumFractionDigits=2} {{{$x}}}",
		"  leading space and .dot",
		"{{.dot at start}}",
	}
	for _, src := range srcs {
		t.Run(src, func(t *testing.T) {
			msg, err := ParseMF2(src)
			if err != nil {
				t.Fatalf("ParseMF2: %v", err)
			}
			out, err := Stringify(msg)
			if err != nil {
				t.Fatalf("Stringify: %v", err)
			}
			again, err := ParseMF2(out)
			if err != nil {
				t.Fatalf("re-parse %q: %v", out, err)
			}
			if !reflect.DeepEqual(normalize(msg), normalize(again)) {
				t.Errorf("round trip changed the model\nsrc: %q\nout: %q", src, out)
			}
		})
	}
}

func TestStringifyRejectsInvalidModel(t *testing.T) {
	_, err := Stringify(Message{Type: PatternMessageType, Pattern: Pattern{Expression{}}})
	if !errors.Is(err, &Error{Code: CodeInvalidMessage}) {
		t.Fatalf("want invalid-message, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	valid, err := ParseMF2(".input {$n :number} .match $n one {{a}} * {{b}}")
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(valid); err != nil {
		t.Errorf("Validate(valid) = %v", err)
	}
	invalid := valid
	invalid.Variants = invalid.Variants[:1]
	if err := Validate(invalid); !errors.Is(err, &Error{Code: CodeMissingFallbackVariant}) {
		t.Errorf("Validate(no fallback) = %v, want missing-fallback-variant", err)
	}
	if err := Validate(Message{Type: "bogus"}); !errors.Is(err, &Error{Code: CodeInvalidMessage}) {
		t.Errorf("Validate(bogus) = %v, want invalid-message", err)
	}
}

func TestFormat(t *testing.T) {
	date := time.Date(2026, 9, 19, 14, 5, 9, 0, time.UTC)
	tests := []struct {
		name   string
		src    string
		locale string
		values map[string]any
		want   string
	}{
		{"text", "Hallo Welt", "de", nil, "Hallo Welt"},
		{"string var", "Hallo {$name}!", "de", map[string]any{"name": "Anna"}, "Hallo Anna!"},
		{"number de", "{$n :number}", "de", map[string]any{"n": 1234.5}, "1.234,5"},
		{"plural de one", ".input {$n :number} .match $n one {{eine Datei}} * {{{$n} Dateien}}", "de", map[string]any{"n": 1}, "eine Datei"},
		{"plural de other", ".input {$n :number} .match $n one {{eine Datei}} * {{{$n} Dateien}}", "de", map[string]any{"n": 1000}, "1.000 Dateien"},
		{"currency (draft)", "{$p :currency currency=EUR}", "de", map[string]any{"p": 9.5}, "9,50\u00a0€"},
		{"date (draft)", "{$d :date length=long}", "de", map[string]any{"d": date}, "19. September 2026"},
		{"markup formats to nothing", "{#b}fett{/b}", "de", nil, "fett"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := ParseMF2(tt.src)
			if err != nil {
				t.Fatalf("ParseMF2: %v", err)
			}
			got, err := Format(msg, tt.locale, tt.values, WithBidiIsolation(false))
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			if got != tt.want {
				t.Errorf("Format = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatReportsRecoverableErrors(t *testing.T) {
	msg, err := ParseMF2("Hallo {$name}!")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Format(msg, "de", nil, WithBidiIsolation(false))
	if got != "Hallo {$name}!" {
		t.Errorf("fallback output = %q", got)
	}
	var fe *FormatError
	if !errors.As(err, &fe) {
		t.Fatalf("want *FormatError, got %v", err)
	}
	if codes := fe.Codes(); len(codes) != 1 || codes[0] != CodeUnresolvedVariable {
		t.Errorf("codes = %v", codes)
	}
	if !errors.Is(err, &Error{Code: CodeUnresolvedVariable}) {
		t.Error("errors.Is does not see the unresolved-variable error")
	}
}

func TestFormatBidiIsolationDefaultsToSpec(t *testing.T) {
	msg, err := ParseMF2("Hallo {$name}!")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Format(msg, "de", map[string]any{"name": "Anna"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hallo ⁨Anna⁩!" {
		t.Errorf("Format = %q, want isolated placeholder", got)
	}
}

func TestFormatInvalidLocale(t *testing.T) {
	msg, _ := ParseMF2("x")
	if _, err := Format(msg, "not a locale!", nil); !errors.Is(err, &Error{Code: CodeInvalidLocale}) {
		t.Errorf("want invalid-locale, got %v", err)
	}
}

// normalize maps empty and nil collections to one representation so
// models built by hand compare equal to parsed ones.
func normalize(m Message) Message {
	m.Declarations = nilIfEmpty(m.Declarations)
	m.Selectors = nilIfEmpty(m.Selectors)
	m.Pattern = normalizePattern(m.Pattern)
	for i := range m.Declarations {
		m.Declarations[i].Value = normalizeExpression(m.Declarations[i].Value)
	}
	if m.Variants != nil {
		vs := make([]Variant, len(m.Variants))
		for i, v := range m.Variants {
			vs[i] = Variant{Keys: v.Keys, Value: normalizePattern(v.Value)}
		}
		m.Variants = vs
	}
	return m
}

func normalizePattern(p Pattern) Pattern {
	if len(p) == 0 {
		return nil
	}
	out := make(Pattern, len(p))
	for i, el := range p {
		switch el := el.(type) {
		case Expression:
			out[i] = normalizeExpression(el)
		case Markup:
			el.Options = nilIfEmptyMap(el.Options)
			el.Attributes = nilIfEmptyMap(el.Attributes)
			out[i] = el
		default:
			out[i] = el
		}
	}
	return out
}

func normalizeExpression(e Expression) Expression {
	e.Attributes = nilIfEmptyMap(e.Attributes)
	if e.Function != nil {
		f := *e.Function
		f.Options = nilIfEmptyMap(f.Options)
		e.Function = &f
	}
	return e
}

func nilIfEmpty[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	return s
}

func nilIfEmptyMap[M ~map[K]V, K comparable, V any](m M) M {
	if len(m) == 0 {
		return nil
	}
	return m
}
