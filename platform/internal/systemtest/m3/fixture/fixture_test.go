package fixture_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/systemtest/m3/fixture"
)

const dir = "../testdata"

// TestFixtureIsCurrent fails when the committed fixture differs from
// what the generator writes, so the exit test never runs on an
// application nobody can regenerate.
func TestFixtureIsCurrent(t *testing.T) {
	f, err := fixture.Generate(fixture.DefaultSeed)
	if err != nil {
		t.Fatal(err)
	}
	files, err := fixture.Files(f)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("%s: %v — run `go generate ./internal/systemtest/m3/...`", name, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from the generator — run `go generate ./internal/systemtest/m3/...`", name)
		}
	}
	// Nothing generated is left behind: every committed file under the
	// app is either generated or written by the JS build.
	built, err := os.ReadFile(filepath.Join(dir, "app", fixture.FileUsages))
	if err != nil || len(built) == 0 {
		t.Errorf("%s is missing — run `pnpm --filter @klarlabs-studio/glossa-unplugin build:m3`", fixture.FileUsages)
	}
	for _, d := range []string{fixture.DirPreview, fixture.DirProduction} {
		entries, err := os.ReadDir(filepath.Join(dir, "app", filepath.FromSlash(d)))
		if err != nil || len(entries) == 0 {
			t.Errorf("app/%s is missing — run `pnpm --filter @klarlabs-studio/glossa-unplugin build:m3`", d)
		}
	}
}

// TestFixtureShape checks the application the RFC asks for: about 150
// messages over eight routes, five locales, a React island, and a server
// side that reuses some of the same copy.
func TestFixtureShape(t *testing.T) {
	f, err := fixture.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Messages) != 150 || len(f.Pages) != 8 {
		t.Errorf("%d messages over %d routes, want about 150 over 8", len(f.Messages), len(f.Pages))
	}
	if f.SourceLocale != "de" || !slices.Equal(f.Locales, []string{"en", "es", "fr", "ja"}) {
		t.Errorf("source %s, targets %v", f.SourceLocale, f.Locales)
	}
	island, components, keys := 0, map[string]bool{}, map[string]bool{}
	for _, m := range f.Messages {
		if keys[m.Key] {
			t.Errorf("%s appears twice", m.Key)
		}
		keys[m.Key] = true
		if m.Web.File == "" || m.Web.Component == "" || m.Web.Kind == "" {
			t.Errorf("%s has no place in the app: %+v", m.Key, m.Web)
		}
		if strings.HasSuffix(m.Web.File, ".tsx") {
			island++
		}
		components[m.Web.Component] = true
		for _, locale := range f.Locales {
			if m.Translations[locale] == "" {
				t.Errorf("%s has no %s", m.Key, locale)
			}
		}
	}
	if island == 0 {
		t.Error("no message is used in the React island")
	}
	if f.GoUsages() == 0 {
		t.Error("the server side reuses no message")
	}
	if len(f.NewKeys) != 5 || f.InvalidKey == "" || f.InvalidLine == 0 {
		t.Errorf("the pull request adds %d keys; invalid %q at line %d", len(f.NewKeys), f.InvalidKey, f.InvalidLine)
	}
	if f.InvalidSource == f.RepairedSource {
		t.Error("the invalid message is the same as its repair")
	}
}
