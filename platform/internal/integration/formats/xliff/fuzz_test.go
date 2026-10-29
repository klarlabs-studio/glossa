package xliff_test

import (
	"bytes"
	"os"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/formatstest"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/xliff"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// FuzzRead: any input is an error or a catalog, never a panic, and a
// catalog that was read writes and reads back unchanged. The seeds (the
// golden file plus testdata/fuzz/FuzzRead) run with every `go test`; run
// `go test -fuzz=FuzzRead -fuzztime=30s` to search further.
func FuzzRead(f *testing.F) {
	golden, err := os.ReadFile("testdata/golden/catalog.de.xlf")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(golden)
	f.Add([]byte(`<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.0" srcLang="en" trgLang="de"><file id="f"><group id="g"><unit id="u"><segment state="reviewed"><source>a<mrk id="m">b</mrk><cp hex="0001"/></source><target>c</target></segment><ignorable><source> </source></ignorable></unit></group></file></xliff>`))
	f.Add([]byte(`<!DOCTYPE x [<!ENTITY e SYSTEM "file:///etc/passwd">]><xliff>&e;</xliff>`))
	f.Fuzz(func(t *testing.T, doc []byte) {
		cat, err := xliff.Read(bytes.NewReader(doc), xliff.ReadOptions{Limits: formats.Limits{MaxBytes: 1 << 20}})
		if err != nil || len(cat.Entries) == 0 {
			return
		}
		target := targetLocale(cat)
		var out bytes.Buffer
		if err := xliff.Write(&out, cat, xliff.WriteOptions{TargetLocale: target}); err != nil {
			t.Fatalf("read catalog does not write: %v", err)
		}
		again, err := xliff.Read(&out, xliff.ReadOptions{})
		if err != nil {
			t.Fatalf("written catalog does not read: %v\n%s", err, out.Bytes())
		}
		formatstest.SameCatalog(t, byNamespace(cat), again)
	})
}

func targetLocale(cat formats.Catalog) bcp47.Tag {
	for _, e := range cat.Entries {
		if len(e.Targets) > 0 {
			return e.Targets[0].Locale
		}
	}
	return bcp47.Tag{}
}
