package tmx_test

import (
	"bytes"
	"os"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tmx"
)

// FuzzRead: any input is an error or units, never a panic, and units
// that were read write and read back unchanged. Seeds: the golden file,
// foreign TMX and testdata/fuzz/FuzzRead; run
// `go test -fuzz=FuzzRead -fuzztime=30s -fuzzminimizetime=5s` to search
// further.
func FuzzRead(f *testing.F) {
	golden, err := os.ReadFile("testdata/golden/memory.tmx")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(golden)
	f.Add([]byte(foreignTMX[len(`<?xml version="1.0" encoding="UTF-16"?>`):]))
	f.Fuzz(func(t *testing.T, doc []byte) {
		units, err := tmx.ReadAll(bytes.NewReader(doc), tmx.ReadOptions{Limits: formats.Limits{MaxBytes: 1 << 20}})
		if err != nil || len(units) == 0 {
			return
		}
		var out bytes.Buffer
		if err := tmx.Write(&out, units, tmx.WriteOptions{}); err != nil {
			t.Fatalf("read units do not write: %v", err)
		}
		again, err := tmx.ReadAll(&out, tmx.ReadOptions{})
		if err != nil {
			t.Fatalf("written units do not read: %v\n%s", err, out.Bytes())
		}
		sameUnits(t, units, again)
	})
}
