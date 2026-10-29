package po_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/formatstest"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/po"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

func readFile(t *testing.T, path string, opts po.ReadOptions) formats.Catalog {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := po.Read(bytes.NewReader(data), opts)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func plain(t *testing.T, s string) formats.Target {
	t.Helper()
	c, err := formats.PlainText(s)
	if err != nil {
		t.Fatal(err)
	}
	return formats.Target{Locale: bcp47.MustParse("de-DE"), Content: c, State: formats.StateApproved}
}

func mf2(t *testing.T, s string) formats.Target {
	t.Helper()
	c, err := formats.ParseContent("mf2", s, bcp47.Tag{})
	if err != nil {
		t.Fatal(err)
	}
	return formats.Target{Locale: bcp47.MustParse("de-DE"), Content: c, State: formats.StateApproved}
}

func TestReadSample(t *testing.T) {
	cat := readFile(t, "testdata/de.po", po.ReadOptions{})
	fuzzy := plain(t, `Hallo %s, Sie haben {Klammern} und "Anführungszeichen"`)
	fuzzy.State = formats.StateNeedsReview
	want := formats.Catalog{SourceLocale: bcp47.MustParse("en"), Entries: []formats.Entry{
		{ID: "Checkout", Description: "Page title of the cart.\nKeep it short.", References: []string{"src/cart.ts:12", "src/header.ts:3"},
			Source: plain(t, "Checkout").Content, Targets: []formats.Target{plain(t, "Kasse")}},
		{ID: `Hello %s, you have {braces} and "quotes"`, Notes: []string{"Reviewed by Anna."},
			Source: plain(t, `Hello %s, you have {braces} and "quotes"`).Content, Targets: []formats.Target{fuzzy}},
		{ID: "Open", Namespace: "verb", Source: plain(t, "Open").Content, Targets: []formats.Target{plain(t, "Öffnen")}},
		{ID: "Open", Namespace: "adjective", Source: plain(t, "Open").Content, Targets: []formats.Target{plain(t, "Geöffnet")}},
		{ID: "One item", References: []string{"src/cart.ts:40"},
			Source:  mf2(t, ".input {$count :number} .match $count one {{One item}} * {{%d items}}").Content,
			Targets: []formats.Target{mf2(t, ".input {$count :number} .match $count one {{Ein Artikel}} * {{%d Artikel}}")}},
		{ID: "Untranslated", Source: plain(t, "Untranslated").Content},
		{ID: "Line one\nLine two\ttabbed \\ backslash", Source: plain(t, "Line one\nLine two\ttabbed \\ backslash").Content,
			Targets: []formats.Target{plain(t, "Zeile eins\nZeile zwei\ttab \\ Backslash")}},
	}}
	formatstest.SameCatalog(t, want, cat)
}

func TestReadOptionsOverrideHeader(t *testing.T) {
	cat := readFile(t, "testdata/de.po", po.ReadOptions{
		Locale: bcp47.MustParse("de-AT"), SourceLocale: bcp47.MustParse("en-GB"),
		PluralVariable: "n", TranslatedState: formats.StateDraft,
	})
	if cat.SourceLocale.String() != "en-GB" {
		t.Errorf("source locale = %s", cat.SourceLocale)
	}
	e := cat.Entries[0]
	if e.Targets[0].Locale.String() != "de-AT" || e.Targets[0].State != formats.StateDraft {
		t.Errorf("target = %+v", e.Targets[0])
	}
	if got := cat.Entries[4].Source.Text; !strings.HasPrefix(got, ".input {$n :number}") {
		t.Errorf("plural variable not applied: %s", got)
	}
}

// pluralCases pin the gettext form → CLDR category mapping for common
// locales, with their standard Plural-Forms.
var pluralCases = []struct {
	locale, header string
	want           string
}{
	{"ja", "nplurals=1; plural=0;", "* {{f0}}"},
	{"de", "nplurals=2; plural=(n != 1);", "one {{f0}} * {{f1}}"},
	{"fr", "nplurals=2; plural=(n > 1);", "one {{f0}} many {{f1}} * {{f1}}"},
	{"pl", "nplurals=3; plural=(n==1 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);", "one {{f0}} few {{f1}} many {{f2}} * {{f2}}"},
	{"ru", "nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);", "one {{f0}} few {{f1}} many {{f2}} * {{f2}}"},
	{"cs", "nplurals=3; plural=(n==1) ? 0 : (n>=2 && n<=4) ? 1 : 2;", "one {{f0}} few {{f1}} * {{f2}}"},
	{"ar", "nplurals=6; plural=n==0 ? 0 : n==1 ? 1 : n==2 ? 2 : n%100>=3 && n%100<=10 ? 3 : n%100>=11 ? 4 : 5;", "zero {{f0}} one {{f1}} two {{f2}} few {{f3}} many {{f4}} * {{f5}}"},
	{"lv", "nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : n != 0 ? 1 : 2);", "zero {{f2}} one {{f0}} * {{f1}}"},
	{"pl", "", "one {{f0}} few {{f1}} many {{f2}} * {{f2}}"},
	{"de", "", "one {{f0}} * {{f1}}"},
	{"ar", "", "zero {{f0}} one {{f1}} two {{f2}} few {{f3}} many {{f4}} * {{f5}}"},
}

func pluralPO(header string, forms int) string {
	var b strings.Builder
	b.WriteString("msgid \"\"\nmsgstr \"\"\n")
	if header != "" {
		fmt.Fprintf(&b, "\"Plural-Forms: %s\\n\"\n", header)
	}
	b.WriteString("\nmsgid \"a file\"\nmsgid_plural \"files\"\n")
	for i := range forms {
		fmt.Fprintf(&b, "msgstr[%d] \"f%d\"\n", i, i)
	}
	return b.String()
}

func TestPluralFormsMapToCLDRCategories(t *testing.T) {
	for _, tt := range pluralCases {
		t.Run(tt.locale+"/"+tt.header, func(t *testing.T) {
			forms := formsIn(tt.want)
			cat, err := po.Read(strings.NewReader(pluralPO(tt.header, forms)), po.ReadOptions{Locale: bcp47.MustParse(tt.locale)})
			if err != nil {
				t.Fatal(err)
			}
			got := cat.Entries[0].Targets[0].Content.Text
			want := ".input {$count :number}\n.match $count\n" + strings.ReplaceAll(tt.want, "}} ", "}}\n")
			if got != want {
				t.Errorf("got\n%s\nwant\n%s", got, want)
			}
			// Categories only decimals select (Czech "many") may fall back
			// to *: a warning, never an error.
			for _, f := range mf.CheckCompat(cat.Entries[0].Source.Model, cat.Entries[0].Targets[0].Content.Model, tt.locale) {
				if f.Severity != mf.SeverityWarning {
					t.Errorf("translation not compatible with its source in %s: %+v", tt.locale, f)
				}
			}
		})
	}
}

// formsIn counts the distinct forms f0…f5 a mapping uses.
func formsIn(s string) int {
	n := 0
	for i := range 6 {
		if strings.Contains(s, fmt.Sprintf("{{f%d}}", i)) {
			n = i + 1
		}
	}
	return n
}

func TestReadErrors(t *testing.T) {
	tests := map[string]struct {
		doc  string
		want error
		line int
	}{
		"no locale":         {"msgid \"a\"\nmsgstr \"b\"\n", formats.ErrInvalid, 0},
		"bad language":      {"msgid \"\"\nmsgstr \"Language: ??\\n\"\n", formats.ErrInvalid, 1},
		"garbage":           {"msgid \"\"\nmsgstr \"Language: de\\n\"\n\nhello\n", formats.ErrInvalid, 4},
		"unterminated":      {"msgid \"\"\nmsgstr \"Language: de\\n\"\n\nmsgid \"a\nmsgstr \"b\"\n", formats.ErrInvalid, 4},
		"bad escape":        {"msgid \"\"\nmsgstr \"Language: de\\n\"\n\nmsgid \"\\q\"\nmsgstr \"b\"\n", formats.ErrInvalid, 4},
		"orphan string":     {"\"x\"\n", formats.ErrInvalid, 1},
		"msgstr no msgid":   {"msgstr \"x\"\n", formats.ErrInvalid, 1},
		"missing msgstr":    {"msgid \"\"\nmsgstr \"Language: de\\n\"\n\nmsgid \"a\"\n\nmsgid \"b\"\nmsgstr \"c\"\n", formats.ErrInvalid, 4},
		"plural mismatch":   {"msgid \"\"\nmsgstr \"Language: de\\n\"\n\nmsgid \"a\"\nmsgstr[0] \"b\"\n", formats.ErrInvalid, 5},
		"duplicate":         {"msgid \"\"\nmsgstr \"Language: de\\n\"\n\nmsgid \"a\"\nmsgstr \"b\"\n\nmsgid \"a\"\nmsgstr \"c\"\n", formats.ErrInvalid, 7},
		"bad plural forms":  {pluralPO("nplurals=2; plural=n/0;", 2), formats.ErrInvalid, 0},
		"bad expression":    {pluralPO("nplurals=2; plural=(n > ;", 2), formats.ErrInvalid, 0},
		"form out of range": {pluralPO("nplurals=2; plural=n;", 2), formats.ErrInvalid, 0},
		"nplurals mismatch": {pluralPO("nplurals=3; plural=n!=1;", 2), formats.ErrInvalid, 0},
		"no mapping":        {pluralPO("", 4), formats.ErrInvalid, 0},
		"invalid utf-8":     {"msgid \"\"\nmsgstr \"Language: de\\n\"\n\nmsgid \"\xff\"\nmsgstr \"b\"\n", formats.ErrInvalid, 4},
		"too many":          {"msgid \"\"\nmsgstr \"Language: de\\n\"\n\nmsgid \"a\"\nmsgstr \"b\"\n\nmsgid \"c\"\nmsgstr \"d\"\n", formats.ErrTooLarge, 7},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			opts := po.ReadOptions{}
			if name == "too many" {
				opts.Limits.MaxItems = 2
			}
			if strings.Contains(tt.doc, "a file") {
				opts.Locale = bcp47.MustParse("de")
			}
			_, err := po.Read(strings.NewReader(tt.doc), opts)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			var fe *formats.Error
			if !errors.As(err, &fe) || fe.Format != "po" {
				t.Fatalf("err = %#v", err)
			}
			if tt.line > 0 && fe.Line != tt.line {
				t.Errorf("line = %d, want %d (%v)", fe.Line, tt.line, err)
			}
		})
	}
}

func TestReadLatin1(t *testing.T) {
	doc := "msgid \"\"\nmsgstr \"\"\n\"Language: de\\n\"\n\"Content-Type: text/plain; charset=ISO-8859-1\\n\"\n\nmsgid \"Size\"\nmsgstr \"Gr\xf6\xdfe\"\n"
	cat, err := po.Read(strings.NewReader(doc), po.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := cat.Entries[0].Targets[0].Content.Model.Pattern[0]; got != mf.Text("Größe") {
		t.Errorf("target = %#v", got)
	}
}

// TestReadProperty renders random entries as PO text and reads them
// back: escapes, contexts, comments and flags survive.
func TestReadProperty(t *testing.T) {
	for seed := range uint64(300) {
		g := formatstest.New(seed)
		g.Control = true
		want := formats.Catalog{SourceLocale: bcp47.MustParse("en")}
		var b strings.Builder
		b.WriteString("msgid \"\"\nmsgstr \"Language: de\\n\"\n")
		for i := range g.R.IntN(6) {
			id, str := fmt.Sprint(i, g.Text()), g.Text()
			e := formats.Entry{ID: id, Namespace: formatstest.Pick(g, "", "ctx"), Source: plain(t, id).Content}
			fmt.Fprintf(&b, "\n#. note %d\n", i)
			e.Description = fmt.Sprint("note ", i)
			fuzzy := g.R.IntN(2) == 0
			if fuzzy {
				b.WriteString("#, fuzzy\n")
			}
			if e.Namespace != "" {
				fmt.Fprintf(&b, "msgctxt %s\n", quote(e.Namespace))
			}
			fmt.Fprintf(&b, "msgid %s\nmsgstr %s\n", quote(id), quote(str))
			if str != "" {
				tgt := plain(t, str)
				tgt.Locale = bcp47.MustParse("de")
				if fuzzy {
					tgt.State = formats.StateNeedsReview
				}
				e.Targets = []formats.Target{tgt}
			}
			want.Entries = append(want.Entries, e)
		}
		got, err := po.Read(strings.NewReader(b.String()), po.ReadOptions{})
		if err != nil {
			t.Fatalf("seed %d: %v\n%s", seed, err, b.String())
		}
		formatstest.SameCatalog(t, want, got)
		if t.Failed() {
			t.Fatalf("seed %d:\n%s", seed, b.String())
		}
	}
}

// quote writes s as a C string, split over lines at newlines as msgmerge
// does.
func quote(s string) string {
	var b strings.Builder
	b.WriteString(`"`)
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteString(`\` + string(r))
		case r == '\n':
			b.WriteString("\\n\"\n\"")
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20:
			fmt.Fprintf(&b, `\%03o`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(`"`)
	return b.String()
}
