package fixture_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tbx"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tmx"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/xliff"
	"go.klarlabs.de/glossa/platform/internal/systemtest/m2/fixture"
)

const testdata = "../testdata"

// TestFixtureIsCurrent: the committed fixture is exactly what the
// generator writes from the default seed (which also means every
// reference and slip passed the generator's checks).
func TestFixtureIsCurrent(t *testing.T) {
	f, err := fixture.Generate(fixture.DefaultSeed)
	if err != nil {
		t.Fatal(err)
	}
	files, err := fixture.Files(f)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(testdata)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, ok := files[e.Name()]; !ok {
			t.Errorf("testdata/%s is not written by the generator; remove it", e.Name())
		}
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(testdata, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("testdata/%s is stale (%v); run go generate ./internal/systemtest/m2/...", name, err)
		}
	}
}

// TestFixtureShape pins what the M2 exit test relies on: about 600
// messages, de source with complete en and partial es/fr/ja, plurals,
// selects, markup, placeholders, max lengths, a sensitive namespace, 20
// concepts, and slips of every kind in every fill locale.
func TestFixtureShape(t *testing.T) {
	f, err := fixture.Load(testdata)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Messages) != fixture.CatalogSize || len(f.Concepts) != 20 || len(f.StyleGuides) == 0 {
		t.Fatalf("%d messages, %d concepts, %d style guides", len(f.Messages), len(f.Concepts), len(f.StyleGuides))
	}
	patterns := map[string]int{}
	for _, m := range f.Messages {
		patterns[m.Pattern]++
	}
	for _, p := range []string{"count", "selected", "permission", "help", "activity", "total", "legal_clause", "common_action"} {
		if patterns[p] == 0 {
			t.Errorf("no %s messages", p)
		}
	}
	coverage := map[string][2]float64{"es": {0.65, 0.75}, "fr": {0.45, 0.55}, "ja": {0, 0}}
	for _, l := range f.FillLocales {
		e := f.Expect[l]
		share := float64(e.Existing) / float64(e.Messages)
		if share < coverage[l][0] || share > coverage[l][1] {
			t.Errorf("%s coverage %.2f", l, share)
		}
		for _, k := range fixture.AllSlips {
			if e.Slips[k] == 0 && !(k == fixture.SlipPluralMissing && l == "ja") {
				t.Errorf("%s has no %s slip", l, k)
			}
		}
		if e.TMReused == 0 || e.AIDrafted == 0 {
			t.Errorf("%s expectation %+v", l, e)
		}
	}
}

// TestInterchangeFilesRead: the files the test imports read back with
// Glossa's own converters.
func TestInterchangeFilesRead(t *testing.T) {
	f, err := fixture.Load(testdata)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"en", "es", "fr"} {
		raw, err := os.ReadFile(filepath.Join(testdata, fixture.XLIFFFile(f.SourceLocale, l)))
		if err != nil {
			t.Fatal(err)
		}
		cat, err := xliff.Read(bytes.NewReader(raw), xliff.ReadOptions{})
		if err != nil || len(cat.Entries) == 0 {
			t.Fatalf("xliff %s: %d entries, %v", l, len(cat.Entries), err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(testdata, fixture.FileTMX))
	if err != nil {
		t.Fatal(err)
	}
	units, err := tmx.ReadAll(bytes.NewReader(raw), tmx.ReadOptions{})
	if err != nil || len(units) != len(f.LegacyTM) {
		t.Fatalf("tmx: %d units, %v", len(units), err)
	}
	raw, err = os.ReadFile(filepath.Join(testdata, fixture.FileTBX))
	if err != nil {
		t.Fatal(err)
	}
	tb, err := tbx.Read(bytes.NewReader(raw), tbx.ReadOptions{})
	if err != nil || len(tb.Concepts) != len(f.Concepts) {
		t.Fatalf("tbx: %d concepts, %v", len(tb.Concepts), err)
	}
	forbidden := 0
	for _, c := range tb.Concepts {
		for _, term := range c.Terms {
			if term.Status == formats.TermForbidden {
				forbidden++
			}
		}
	}
	if forbidden == 0 {
		t.Error("the termbase read back without forbidden terms")
	}
}
