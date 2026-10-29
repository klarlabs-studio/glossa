package domain_test

import (
	"errors"
	"slices"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

func TestPluralCategories(t *testing.T) {
	tests := []struct {
		locale  string
		ordinal bool
		want    []string
	}{
		{"de", false, []string{"one", "other"}},
		{"pl", false, []string{"one", "few", "many", "other"}},
		{"ru", false, []string{"one", "few", "many", "other"}},
		{"ja", false, []string{"other"}},
		{"fr", false, []string{"one", "many", "other"}},
		{"en", true, []string{"one", "two", "few", "other"}},
	}
	for _, tc := range tests {
		got, err := domain.PluralCategories(tc.locale, tc.ordinal)
		if err != nil {
			t.Fatalf("%s: %v", tc.locale, err)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s ordinal=%v: %v, want %v", tc.locale, tc.ordinal, got, tc.want)
		}
	}
	if _, err := domain.PluralCategories("not a tag!", false); err == nil {
		t.Error("an invalid tag must fail")
	}
}

// The prompt lists PluralCategories; validation must accept exactly
// those. A Polish plural that has every listed category is compatible.
func TestPluralCategoriesAgreeWithCheckCompat(t *testing.T) {
	src := mustParse(t, ".input {$n :number}\n.match $n\none {{{$n} Datei}}\n* {{{$n} Dateien}}")
	cats, err := domain.PluralCategories("pl", false)
	if err != nil {
		t.Fatal(err)
	}
	var body string
	for _, c := range cats {
		key := c
		if c == "other" {
			key = "*"
		}
		body += key + " {{{$n} plik}}\n"
	}
	tr := mustParse(t, ".input {$n :number}\n.match $n\n"+body)
	if f := mf.CheckCompat(src, tr, "pl"); len(f) != 0 {
		t.Errorf("findings = %+v", f)
	}
}

func TestPlainTextAndLength(t *testing.T) {
	msg := mustParse(t, ".input {$n :number}\n.match $n\none {{Hello {#b}you{/b}, {$n} file}}\n* {{Hello {#b}you{/b}, {$n} files}}")
	if got, want := domain.PlainText(msg), "Hello you,  file\nHello you,  files"; got != want {
		t.Errorf("PlainText = %q, want %q", got, want)
	}
	if got := domain.TextLength(msg); got != 17 {
		t.Errorf("TextLength = %d", got)
	}
	if got := domain.MarkupCount(msg); got != 4 {
		t.Errorf("MarkupCount = %d", got)
	}
}

func TestParseMessage(t *testing.T) {
	_, canonical, err := domain.ParseMessage("Hello {$name}!")
	if err != nil || canonical != "Hello {$name}!" {
		t.Fatalf("canonical = %q, %v", canonical, err)
	}
	_, _, err = domain.ParseMessage("Hello {$name!")
	if !errors.Is(err, domain.ErrUnparsable) {
		t.Fatalf("err = %v", err)
	}
	var mfErr *mf.Error
	if !errors.As(err, &mfErr) || mfErr.Code == "" {
		t.Errorf("want the kernel's error code, got %v", err)
	}
}

func mustParse(t *testing.T, src string) mf.Message {
	t.Helper()
	msg, err := mf.ParseMF2(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return msg
}
