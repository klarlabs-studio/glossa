package messageformat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	enginetests "github.com/kaptinlin/messageformat-go/tests"
)

// The official Unicode MessageFormat WG conformance suite, run through
// Glossa's public API (ParseMF2 / Format). See testdata/unicode/README.md.

const unicodeTestsDir = "testdata/unicode/tests"

// unicodeSkips lists upstream cases the engine does not pass yet, keyed by
// "<file>: <description or src>". Every entry names the reason; each one is
// a candidate upstream issue for kaptinlin/messageformat-go. Keep this list
// short and explicit — never widen a check to make a case pass.
var unicodeSkips = map[string]string{}

type unicodeFile struct {
	Scenario              string        `json:"scenario"`
	DefaultTestProperties unicodeTest   `json:"defaultTestProperties"`
	Tests                 []unicodeTest `json:"tests"`
}

type unicodeTest struct {
	Description   string          `json:"description"`
	Locale        *string         `json:"locale"`
	Src           *string         `json:"src"`
	BidiIsolation *string         `json:"bidiIsolation"`
	Params        *[]unicodeParam `json:"params"`
	Exp           *string         `json:"exp"`
	ExpParts      *[]any          `json:"expParts"`
	ExpErrors     *[]struct {
		Type string `json:"type"`
	} `json:"expErrors"`
}

type unicodeParam struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// merged applies the file defaults to t.
func (t unicodeTest) merged(d unicodeTest) unicodeTest {
	if t.Locale == nil {
		t.Locale = d.Locale
	}
	if t.Src == nil {
		t.Src = d.Src
	}
	if t.BidiIsolation == nil {
		t.BidiIsolation = d.BidiIsolation
	}
	if t.Params == nil {
		t.Params = d.Params
	}
	if t.Exp == nil {
		t.Exp = d.Exp
	}
	if t.ExpParts == nil {
		t.ExpParts = d.ExpParts
	}
	if t.ExpErrors == nil {
		t.ExpErrors = d.ExpErrors
	}
	return t
}

func (t unicodeTest) name() string {
	if t.Description != "" {
		return t.Description
	}
	return t.srcOrEmpty()
}

func (t unicodeTest) srcOrEmpty() string {
	if t.Src == nil {
		return ""
	}
	return *t.Src
}

func (t unicodeTest) expErrorCodes() []ErrorCode {
	if t.ExpErrors == nil {
		return nil
	}
	codes := make([]ErrorCode, len(*t.ExpErrors))
	for i, e := range *t.ExpErrors {
		codes[i] = ErrorCode(e.Type)
	}
	return codes
}

func (t unicodeTest) values() (map[string]any, error) {
	if t.Params == nil {
		return nil, nil
	}
	out := make(map[string]any, len(*t.Params))
	for _, p := range *t.Params {
		if p.Type != "datetime" {
			out[p.Name] = p.Value
			continue
		}
		s, ok := p.Value.(string)
		if !ok {
			return nil, fmt.Errorf("datetime param %q is not a string", p.Name)
		}
		v, err := parseDateTimeParam(s)
		if err != nil {
			return nil, err
		}
		out[p.Name] = v
	}
	return out, nil
}

func parseDateTimeParam(s string) (time.Time, error) {
	if v, err := time.Parse(time.RFC3339, s); err == nil {
		return v, nil
	}
	return time.ParseInLocation("2006-01-02T15:04:05", s, time.UTC)
}

type conformanceStats struct {
	pass, skip int
}

func TestUnicodeConformance(t *testing.T) {
	files := unicodeTestFiles(t)
	stats := map[string]*conformanceStats{}
	usedSkips := map[string]bool{}
	for _, path := range files {
		rel, _ := filepath.Rel(unicodeTestsDir, path)
		rel = filepath.ToSlash(rel)
		st := &conformanceStats{}
		stats[rel] = st
		file := loadUnicodeFile(t, path)
		t.Run(rel, func(t *testing.T) {
			for i, raw := range file.Tests {
				tc := raw.merged(file.DefaultTestProperties)
				key := rel + ": " + tc.name()
				t.Run(fmt.Sprintf("%03d", i), func(t *testing.T) {
					if reason, ok := unicodeSkips[key]; ok {
						usedSkips[key] = true
						st.skip++
						t.Skipf("%s — %s", key, reason)
					}
					runUnicodeCase(t, tc)
					if !t.Failed() {
						st.pass++
					}
				})
			}
		})
	}
	for key := range unicodeSkips {
		if !usedSkips[key] {
			t.Errorf("stale skip-list entry (no such test): %q", key)
		}
	}
	logConformanceStats(t, stats)
}

// runUnicodeCase runs one case through Format and FormatToParts.
func runUnicodeCase(t *testing.T, tc unicodeTest) {
	t.Helper()
	want := tc.expErrorCodes()
	msg, err := ParseMF2(tc.srcOrEmpty())
	if slices.Contains(want, CodeSyntaxError) || slices.ContainsFunc(want, ErrorCode.IsDataModelError) {
		assertParseError(t, tc, err, want)
		return
	}
	if err != nil {
		t.Fatalf("%s: ParseMF2(%q): %v", tc.name(), tc.srcOrEmpty(), err)
	}
	values, err := tc.values()
	if err != nil {
		t.Fatalf("params: %v", err)
	}
	locale := "en-US"
	if tc.Locale != nil {
		locale = *tc.Locale
	}
	bidi := tc.BidiIsolation == nil || *tc.BidiIsolation == "default"
	got, err := Format(msg, locale, values, WithBidiIsolation(bidi), withEngineFunctions(enginetests.TestFunctions()))
	if tc.Exp != nil && got != *tc.Exp {
		t.Errorf("%s: Format(%q) = %q, want %q", tc.name(), tc.srcOrEmpty(), got, *tc.Exp)
	}
	assertFormatErrors(t, tc, err, want)
	parts, _ := FormatToParts(msg, locale, values, WithBidiIsolation(bidi), withEngineFunctions(enginetests.TestFunctions()))
	if joined := PartsText(parts); joined != got {
		t.Errorf("%s: FormatToParts(%q) joins to %q, Format = %q", tc.name(), tc.srcOrEmpty(), joined, got)
	}
	if tc.ExpParts != nil {
		assertParts(t, tc.name(), parts, *tc.ExpParts)
	}
}

func assertParseError(t *testing.T, tc unicodeTest, err error, want []ErrorCode) {
	t.Helper()
	var mfErr *Error
	if !errors.As(err, &mfErr) {
		t.Fatalf("%s: ParseMF2(%q) = %v, want error %v", tc.name(), tc.srcOrEmpty(), err, want)
	}
	if !slices.Contains(want, mfErr.Code) {
		t.Errorf("%s: ParseMF2(%q) code %s, want one of %v (%v)", tc.name(), tc.srcOrEmpty(), mfErr.Code, want, err)
	}
}

func assertFormatErrors(t *testing.T, tc unicodeTest, err error, want []ErrorCode) {
	t.Helper()
	var got []ErrorCode
	var fe *FormatError
	switch {
	case err == nil:
	case errors.As(err, &fe):
		got = fe.Codes()
	default:
		t.Fatalf("%s: Format(%q): %v", tc.name(), tc.srcOrEmpty(), err)
	}
	if !sameCodes(got, want) {
		t.Errorf("%s: Format(%q) errors %v, want %v", tc.name(), tc.srcOrEmpty(), got, want)
	}
}

// sameCodes compares error codes as multisets: the spec does not fix the
// order in which an implementation reports errors.
func sameCodes(a, b []ErrorCode) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := slices.Clone(a), slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}

func unicodeTestFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(unicodeTestsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", unicodeTestsDir, err)
	}
	sort.Strings(files)
	return files
}

func loadUnicodeFile(t *testing.T, path string) unicodeFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var f unicodeFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return f
}

func logConformanceStats(t *testing.T, stats map[string]*conformanceStats) {
	t.Helper()
	names := make([]string, 0, len(stats))
	for name := range stats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := stats[name]
		t.Logf("%-28s pass %3d  skip %2d", name, s.pass, s.skip)
	}
}
