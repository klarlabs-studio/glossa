package jsoncat_test

import (
	"bytes"
	"os"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/formatstest"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/jsoncat"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// FuzzRead: any input is an error or a catalog, never a panic, and a
// catalog that was read writes (flat, same syntax) and reads back
// unchanged. Seeds: the golden files and testdata/fuzz/FuzzRead; run
// `go test -fuzz=FuzzRead -fuzztime=30s -fuzzminimizetime=5s` to search
// further.
func FuzzRead(f *testing.F) {
	for _, name := range []string{"en.flat.json", "en.nested.json", "de.mf2.json"} {
		doc, err := os.ReadFile("testdata/golden/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(doc, name == "de.mf2.json")
	}
	f.Add([]byte(`{"a": {"b": "{n, select, x {X} other {#}}"}, "c.d": "'{'"}`), false)
	f.Fuzz(func(t *testing.T, doc []byte, mf2 bool) {
		syntax := mfcontent.MF1
		if mf2 {
			syntax = mfcontent.MF2
		}
		opts := jsoncat.ReadOptions{Locale: en, Syntax: syntax, Limits: formats.Limits{MaxBytes: 1 << 20}}
		cat, err := jsoncat.Read(bytes.NewReader(doc), opts)
		if err != nil {
			return
		}
		var out bytes.Buffer
		if err := jsoncat.Write(&out, cat, jsoncat.WriteOptions{Syntax: syntax}); err != nil {
			t.Fatalf("read catalog does not write: %v", err)
		}
		again, err := jsoncat.Read(&out, opts)
		if err != nil {
			t.Fatalf("written catalog does not read: %v\n%s", err, out.Bytes())
		}
		formatstest.SameCatalog(t, sortedByID(cat), again)
	})
}
