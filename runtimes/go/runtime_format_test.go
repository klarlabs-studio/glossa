package glossa

// messageformat/testdata/glossa/runtime-format.json through the runtime's
// rendering path (runtimes/SPEC.md §5): each precompiled message is
// shipped in a release artifact, resolved and formatted by Localizer.T,
// and by Localizer.Parts where the case has expParts.

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

const runtimeFormatFixture = "../../messageformat/testdata/glossa/runtime-format.json"

// runtimeFormatSkips lists cases whose expected output the Go formatter
// (messageformat.Format) doesn't reproduce because of gaps in its CLDR
// layer, keyed by "<description> <params>", with the reason. These are
// engine gaps, not CLDR-version drift: go-intl bundles CLDR 48, and every
// output in the fixture is the same in CLDR 47 and 48. They get fixed
// upstream, not worked around here. Keep the list short: an entry whose
// case is gone, or whose case passes now, fails the test.
var runtimeFormatSkips = map[string]string{
	// The same gap messageformat's glossaFormatSkips records for its MF1
	// fixture: go-intl v0.2.17 (and still v0.4.10) appends the percent
	// sign instead of applying the locale's percent pattern, so the
	// no-break space of "#,##0\u00a0%" (de, es, fr) is lost.
	"de percent [p=0.256]": "go-intl: percent ignores the CLDR pattern #,##0\u00a0% (26% vs 26\u00a0%)",
	"es percent [p=0.256]": "go-intl: percent ignores the CLDR pattern #,##0\u00a0% (26% vs 26\u00a0%)",
	"fr percent [p=0.256]": "go-intl: percent ignores the CLDR pattern #,##0\u00a0% (26% vs 26\u00a0%)",
	// go-intl v0.2.17 (and still v0.4.10) ignores the locale's
	// minimumGroupingDigits under useGrouping "auto": es sets it to 2, so a
	// four-digit integer part stays ungrouped (1234, but 12.345).
	"es grouping and negative numbers [n=1234]":    "go-intl: ignores CLDR minimumGroupingDigits=2 for es (1.234 vs 1234)",
	"es grouping and negative numbers [n=-1234.5]": "go-intl: ignores CLDR minimumGroupingDigits=2 for es (-1.234,5 vs -1234,5)",
	"es EUR total [total=1234.5]":                  "go-intl: ignores CLDR minimumGroupingDigits=2 for es (1.234,50 vs 1234,50)",
}

type runtimeFormatCase struct {
	Description   string          `json:"description"`
	Locale        string          `json:"locale"`
	Message       json.RawMessage `json:"message"`
	Params        []formatParam   `json:"params"`
	BidiIsolation string          `json:"bidiIsolation"`
	Exp           string          `json:"exp"`
	ExpParts      []any           `json:"expParts"`
	ExpErrors     []struct {
		Type string `json:"type"`
	} `json:"expErrors"`
}

type formatParam struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value any    `json:"value"`
}

func (tc runtimeFormatCase) key() string {
	parts := make([]string, len(tc.Params))
	for i, p := range tc.Params {
		parts[i] = fmt.Sprintf("%s=%v", p.Name, p.Value)
	}
	return tc.Description + " [" + strings.Join(parts, " ") + "]"
}

func (tc runtimeFormatCase) args(t *testing.T) Args {
	args := Args{}
	for _, p := range tc.Params {
		args[p.Name] = p.Value
		if p.Type == "datetime" {
			ts, err := time.Parse(time.RFC3339, p.Value.(string))
			if err != nil {
				t.Fatal(err)
			}
			args[p.Name] = ts
		}
	}
	return args
}

func TestRuntimeFormatFixture(t *testing.T) {
	b, err := os.ReadFile(runtimeFormatFixture)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Tests []runtimeFormatCase `json:"tests"`
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	used, passed := map[string]bool{}, 0
	for _, tc := range fixture.Tests {
		if reason, ok := runtimeFormatSkips[tc.key()]; ok {
			used[tc.key()] = true
			if renderRuntimeFormat(t, tc) == tc.Exp {
				t.Errorf("runtimeFormatSkips entry %q passes now: remove it", tc.key())
				continue
			}
			t.Logf("skip %s: %s", tc.key(), reason)
			continue
		}
		if checkRuntimeFormat(t, tc) {
			passed++
		}
	}
	for key := range runtimeFormatSkips {
		if !used[key] {
			t.Errorf("stale runtimeFormatSkips entry %q", key)
		}
	}
	t.Logf("runtime-format.json: %d passed, %d skipped", passed, len(runtimeFormatSkips))
}

// renderRuntimeFormat renders a skipped case through Localizer.T, so a
// skip whose upstream gap is fixed shows up.
func renderRuntimeFormat(t *testing.T, tc runtimeFormatCase) string {
	t.Helper()
	rel := buildReleaseModels(t, "rel_fmt", 1, map[string]map[string]any{tc.Locale: {"m": tc.Message}})
	c := newTestClient(t, Config{Bundled: rel.fs(), OnError: (&errorLog{}).handle})
	return c.For(tc.Locale).T("m", tc.args(t), BidiIsolation(tc.BidiIsolation != "none"))
}

func checkRuntimeFormat(t *testing.T, tc runtimeFormatCase) bool {
	t.Helper()
	rel := buildReleaseModels(t, "rel_fmt", 1, map[string]map[string]any{tc.Locale: {"m": tc.Message}})
	log := &errorLog{}
	c := newTestClient(t, Config{Bundled: rel.fs(), OnError: log.handle})
	got := c.For(tc.Locale).T("m", tc.args(t), BidiIsolation(tc.BidiIsolation != "none"))
	ok := true
	if got != tc.Exp {
		t.Errorf("%s: got %q, want %q", tc.key(), got, tc.Exp)
		ok = false
	}
	if tc.ExpParts != nil {
		parts := c.For(tc.Locale).Parts("m", tc.args(t), BidiIsolation(tc.BidiIsolation != "none"))
		ok = checkExpParts(t, tc.key(), parts, tc.ExpParts) && ok
	}
	errs := log.all()
	if len(errs) != min(len(tc.ExpErrors), 1) {
		t.Errorf("%s: reported %v, want the format errors %v", tc.key(), errs, tc.ExpErrors)
		return false
	}
	for _, want := range tc.ExpErrors {
		if errs[0].Type != ErrorFormat || !strings.Contains(errs[0].Detail, want.Type) {
			t.Errorf("%s: reported %+v, want a format error with %s", tc.key(), errs[0], want.Type)
			ok = false
		}
	}
	return ok
}

// checkExpParts compares parts with the fixture's expParts the way the
// MF2 suites do: every expected field must be present and equal, and
// implementations may add fields.
func checkExpParts(t *testing.T, name string, parts []Part, exp []any) bool {
	t.Helper()
	raw, err := json.Marshal(parts)
	if err != nil {
		t.Fatal(err)
	}
	var got []any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !containsJSON(got, exp) {
		want, _ := json.Marshal(exp)
		t.Errorf("%s: Parts\n got %s\nwant %s", name, raw, want)
		return false
	}
	return true
}

func containsJSON(got, want any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for k, wv := range w {
			if gv, ok := g[k]; !ok || !containsJSON(gv, wv) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !containsJSON(g[i], w[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(got, want)
	}
}
