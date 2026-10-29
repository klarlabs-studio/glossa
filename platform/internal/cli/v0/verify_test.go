package v0

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"
)

func str(s string) *string { return &s }

// The known-defect category, case by case. v0 and the requoted v0
// outputs are written out by hand from v0.3's parser (packages/format,
// parse.ts): any apostrophe not doubled opens a quoted run to the next
// apostrophe or the end, its content literal and the apostrophes
// dropped. TestRequotedTextRendersTheICUWayInV0sOwnFormatter checks the
// same cases against the real formatter when it is built.
func TestClassifyTheBareApostrophe(t *testing.T) {
	for _, c := range []struct {
		name, text      string
		v0, runtime, rq *string
		v0Err, rtErr    string
		verdict, defect string
	}{
		{
			name: "a bare apostrophe that swallows the argument (§12.6's copy.bare, de)",
			text: "Geht's gut, {name}?", v0: str("Gehts gut, {name}?"), runtime: str("Geht's gut, Ada?"),
			rq: str("Geht's gut, Ada?"), verdict: VerdictKnownDefect, defect: DefectBareApostrophe,
		},
		{
			name: "two bare apostrophes that only drop (§12.6's copy.bare, es)",
			text: "Rock'n'roll con {name}", v0: str("Rocknroll con Ada"), runtime: str("Rock'n'roll con Ada"),
			rq: str("Rock'n'roll con Ada"), verdict: VerdictKnownDefect, defect: DefectBareApostrophe,
		},
		{
			name: "a bare apostrophe inside a plural branch, which v0.3 cannot even parse",
			text: "{count, plural, one {# Datei} other {# Datei's}}", v0Err: "expected '}' but got 'EOF' at offset 48",
			runtime: str("5 Datei's"), rq: str("5 Datei's"), verdict: VerdictKnownDefect, defect: DefectBareApostrophe,
		},
		{
			name: "the same text, rendered the same",
			text: "It''s {name}''s turn", v0: str("It's Ada's turn"), runtime: str("It's Ada's turn"),
			verdict: VerdictMatch,
		},
		// Near misses: an apostrophe is involved, but it is not the whole
		// difference. Every one stays a mismatch.
		{
			name: "a changed word beside the apostrophe",
			text: "Geht's gut, {name}?", v0: str("Gehts gut, {name}?"), runtime: str("Geht's prima, Ada?"),
			rq: str("Geht's gut, Ada?"), verdict: VerdictMismatch,
		},
		{
			name: "only the apostrophe differs, but the text has no bare apostrophe for v0.3 to misread",
			text: "Its fine", v0: str("Its fine"), runtime: str("It's fine"), verdict: VerdictMismatch,
		},
		{
			name: "the runtime fell back to another locale",
			text: "Geht's gut, {name}?", v0: str("Gehts gut, {name}?"), runtime: str("How are you, Ada?"),
			rtErr: "resolved from en, not de", rq: str("Geht's gut, Ada?"), verdict: VerdictMismatch,
		},
		{
			name: "the runtime failed",
			text: "Geht's gut, {name}?", v0: str("Gehts gut, {name}?"), rtErr: "edge unreachable",
			rq: str("Geht's gut, Ada?"), verdict: VerdictMismatch,
		},
		{
			name: "the requoted text fails in v0.3 too",
			text: "Geht's {n, number}", v0Err: "unsupported argType 'number'", runtime: str("Geht's 5"), verdict: VerdictMismatch,
		},
		{
			name: "an apostrophe and a number formatted differently",
			text: "{count, plural, other {# Artikel}} im Korb, gell's", v0: str("1000 Artikel im Korb, gells"),
			runtime: str("1.000 Artikel im Korb, gell's"), rq: str("1000 Artikel im Korb, gell's"), verdict: VerdictMismatch,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			requoted := RequoteForV0(c.text) != c.text
			r := Rendering{V0: c.v0, Runtime: c.runtime, V0Error: c.v0Err, RuntimeError: c.rtErr, V0Requoted: c.rq, Requoted: requoted}
			if c.rq == nil && requoted {
				r.RequotedError = "unsupported argType 'number'"
			}
			verdict, defect := Classify(r)
			if verdict != c.verdict || defect != c.defect {
				t.Errorf("Classify = %s %s, want %s %s", verdict, defect, c.verdict, c.defect)
			}
		})
	}
}

// RequoteForV0 changes the apostrophes and nothing else, following the
// platform's MF1 lexer exactly.
func TestRequoteForV0(t *testing.T) {
	for text, want := range map[string]string{
		"Geht's gut, {name}?":              "Geht''s gut, {name}?",
		"Rock'n'roll con {name}":           "Rock''n''roll con {name}",
		"It''s {name}''s turn":             "It''s {name}''s turn",
		"Use '{'braces'}', {name}":         "Use '{'braces'}', {name}",
		"Nutze '{Klammern}' hier":          "Nutze '{'Klammern'}' hier",
		"'#' im Plural":                    "'#' im Plural",
		"a '{quoted ''x'' run}' b":         "a '{'quoted ''x'' run'}' b",
		"no apostrophe at all {n}":         "no apostrophe at all {n}",
		"an open quote '{name} never ends": "an open quote ''{name} never ends",
		"trailing'":                        "trailing''",
		"'{a}'' is not a quoted run":       "''{a}'' is not a quoted run",
		"l'été est là":                     "l''été est là",
	} {
		if got := RequoteForV0(text); got != want {
			t.Errorf("RequoteForV0(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestGenerateArgsFromTheMessagesOwnArguments(t *testing.T) {
	sets := GenerateArgs(map[string]string{
		"de": "{gender, select, female {{count, plural, one {{name} hat ihre # Datei} other {{name} hat ihre # Dateien}}} other {{count, plural, =0 {Keine} other {{name}: #}}}}",
		"en": "{gender, select, female {{name}: {count, plural, one {# file} other {# files}}} other {{count, plural, other {# files}}}}",
	})
	// gender: female, zz_other; count: 0 1 2 5 21; name: Ada.
	if len(sets) != 10 {
		t.Fatalf("%d sets, want 2 × 5 × 1: %v", len(sets), sets)
	}
	want := map[string]any{"gender": "female", "count": 0, "name": "Ada"}
	if !reflect.DeepEqual(sets[0], want) {
		t.Errorf("first set = %v, want %v", sets[0], want)
	}
	if got := GenerateArgs(map[string]string{"de": "Bestellung ansehen"}); len(got) != 1 || len(got[0]) != 0 {
		t.Errorf("a plain message: %v", got)
	}
	if got := GenerateArgs(map[string]string{"de": "{a, plural, other {x}"}); len(got) != 1 {
		t.Errorf("text that does not parse: %v", got)
	}
	// Past the bound, each selector varies alone.
	many := GenerateArgs(map[string]string{"en": "{a, plural, other {#}}{b, plural, other {#}}{c, plural, other {#}}"})
	if len(many) != 1+3*4 {
		t.Errorf("%d sets, want 13", len(many))
	}
}

// v0FormatModule finds a built @felixgeelhaar/glossa-format: the
// environment's GLOSSA_V0_FORMAT_MODULE, or packages/format of this
// repository when `make system-m5-deps` (or pnpm) built it.
func v0FormatModule(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("GLOSSA_V0_FORMAT_MODULE"); d != "" {
		return d
	}
	d, _ := filepath.Abs(filepath.Join("..", "..", "..", "..", "packages", "format"))
	if _, err := os.Stat(filepath.Join(d, "dist", "index.js")); err != nil {
		t.Skip("@felixgeelhaar/glossa-format is not built (set GLOSSA_V0_FORMAT_MODULE or build packages/format)")
	}
	return d
}

// The classifier's evidence, from v0.3's real formatter: each text's
// v0.3 rendering is as written above, and the requoted text renders the
// ICU way — so the known-defect rows TestClassifyTheBareApostrophe
// assumes are what v0.3 produces.
func TestRequotedTextRendersTheICUWayInV0sOwnFormatter(t *testing.T) {
	dir := v0FormatModule(t)
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	script := `import { pathToFileURL } from "node:url";
const { format } = await import(pathToFileURL(process.argv[2] + "/dist/index.js").href);
const cases = JSON.parse(process.argv[3]);
process.stdout.write(JSON.stringify(cases.map(([t, l, a]) => { try { return format(t, l, a); } catch (e) { return "ERR " + e.message; } })));`
	cases := []struct {
		text, locale string
		args         map[string]any
		v0, icu      string
	}{
		{"Geht's gut, {name}?", "de", map[string]any{"name": "Ada"}, "Gehts gut, {name}?", "Geht's gut, Ada?"},
		{"Don't wait, {name}", "en", map[string]any{"name": "Ada"}, "Dont wait, {name}", "Don't wait, Ada"},
		{"Rock'n'roll con {name}", "es", map[string]any{"name": "Ada"}, "Rocknroll con Ada", "Rock'n'roll con Ada"},
		{"Use '{'braces'}', {name}", "en", map[string]any{"name": "Ada"}, "Use {braces}, Ada", "Use {braces}, Ada"},
		{"It''s {name}''s turn", "en", map[string]any{"name": "Ada"}, "It's Ada's turn", "It's Ada's turn"},
		{"{count, plural, one {# Datei} other {# Datei's}}", "de", map[string]any{"count": 5}, "ERR", "5 Datei's"},
	}
	var in []any
	for _, c := range cases {
		in = append(in, []any{c.text, c.locale, c.args}, []any{RequoteForV0(c.text), c.locale, c.args})
	}
	out := runNode(t, script, dir, in)
	for i, c := range cases {
		got, requoted := out[2*i], out[2*i+1]
		if !strings.HasPrefix(got, c.v0) || (c.v0 != "ERR" && got != c.v0) {
			t.Errorf("v0.3 renders %q as %q, want %q", c.text, got, c.v0)
		}
		if requoted != c.icu {
			t.Errorf("v0.3 renders the requoted %q as %q, want %q", RequoteForV0(c.text), requoted, c.icu)
		}
	}
}

// The other side of the evidence: the platform's MF1 → MF2 converter,
// which every runtime's text comes from, reads the same texts the ICU
// way that the requoted v0.3 rendering reproduces.
func TestThePlatformReadsTheseApostrophesTheICUWay(t *testing.T) {
	for _, c := range []struct {
		text, locale string
		args         map[string]any
		want         string
	}{
		{"Geht's gut, {name}?", "de", map[string]any{"name": "Ada"}, "Geht's gut, Ada?"},
		{"Don't wait, {name}", "en", map[string]any{"name": "Ada"}, "Don't wait, Ada"},
		{"Rock'n'roll con {name}", "es", map[string]any{"name": "Ada"}, "Rock'n'roll con Ada"},
		{"Use '{'braces'}', {name}", "en", map[string]any{"name": "Ada"}, "Use {braces}, Ada"},
		{"{count, plural, one {# Datei} other {# Datei's}}", "de", map[string]any{"count": 5}, "5 Datei's"},
	} {
		msg, err := mf.ParseMF1(c.text, c.locale)
		if err != nil {
			t.Fatalf("%q: %v", c.text, err)
		}
		got, err := mf.Format(msg, c.locale, c.args, mf.WithBidiIsolation(false))
		if err != nil || got != c.want {
			t.Errorf("the platform renders %q as %q (%v), want %q", c.text, got, err, c.want)
		}
	}
}

func runNode(t *testing.T, script, dir string, cases []any) []string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "probe.mjs")
	if err := os.WriteFile(f, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "node", f, dir, string(raw))
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
