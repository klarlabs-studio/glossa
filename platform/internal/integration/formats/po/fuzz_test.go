package po_test

import (
	"bytes"
	"os"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/po"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// FuzzRead: any input is an error or a catalog, never a panic, and every
// message read is valid. Seeds: testdata/de.po, the plural cases and
// testdata/fuzz/FuzzRead; run
// `go test -fuzz=FuzzRead -fuzztime=30s -fuzzminimizetime=5s` to search
// further.
func FuzzRead(f *testing.F) {
	sample, err := os.ReadFile("testdata/de.po")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(sample)
	for _, c := range pluralCases {
		f.Add([]byte(pluralPO(c.header, formsIn(c.want))))
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		cat, err := po.Read(bytes.NewReader(doc), po.ReadOptions{
			Locale: bcp47.MustParse("pl"), Limits: formats.Limits{MaxBytes: 1 << 20},
		})
		if err != nil {
			return
		}
		for _, e := range cat.Entries {
			if e.Source.IsZero() {
				t.Fatalf("entry %q without source", e.ID)
			}
			for _, tg := range e.Targets {
				if !tg.State.Valid() || tg.Content.IsZero() {
					t.Fatalf("entry %q: target %+v", e.ID, tg)
				}
			}
		}
	})
}
