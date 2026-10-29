package tbx_test

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/tbx"
)

// FuzzRead: any input is an error or a termbase, never a panic, and a
// termbase that was read writes and reads back unchanged. Seeds: the
// golden file and testdata/fuzz/FuzzRead; run
// `go test -fuzz=FuzzRead -fuzztime=30s -fuzzminimizetime=5s` to search
// further.
func FuzzRead(f *testing.F) {
	golden, err := os.ReadFile("testdata/golden/termbase.tbx")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(golden)
	f.Add([]byte(`<martif type="TBX-Basic" xml:lang="en"><text><body><termEntry id="1"><langSet xml:lang="de"><ntig><termGrp><term>a</term><termNote type="normativeAuthorization">deprecatedTerm</termNote></termGrp></ntig></langSet></termEntry></body></text></martif>`))
	f.Fuzz(func(t *testing.T, doc []byte) {
		tb, err := tbx.Read(bytes.NewReader(doc), tbx.ReadOptions{Limits: formats.Limits{MaxBytes: 1 << 20}})
		if err != nil {
			return
		}
		tb = sortTerms(tb)
		var out bytes.Buffer
		if err := tbx.Write(&out, tb); err != nil {
			if len(emptyConcepts(tb)) > 0 {
				return // TBX needs a term per concept; a foreign file may have none
			}
			t.Fatalf("read termbase does not write: %v", err)
		}
		again, err := tbx.Read(&out, tbx.ReadOptions{})
		if err != nil {
			t.Fatalf("written termbase does not read: %v\n%s", err, out.Bytes())
		}
		sameTermbase(t, normalized(tb), again)
	})
}

// normalized applies Write's documented normalizations: a default
// document language and concept IDs, and TBX-Basic parts of speech.
func normalized(tb formats.Termbase) formats.Termbase {
	if tb.Language.IsZero() {
		tb.Language = en
	}
	for i := range tb.Concepts {
		c := &tb.Concepts[i]
		if c.ID == "" {
			c.ID = fmt.Sprint("c", i+1)
		}
		for j := range c.Terms {
			switch c.Terms[j].PartOfSpeech {
			case "", "noun", "verb", "adjective", "adverb", "other":
			default:
				c.Terms[j].PartOfSpeech = "other"
			}
		}
	}
	return tb
}

func emptyConcepts(tb formats.Termbase) []int {
	var out []int
	for i, c := range tb.Concepts {
		if len(c.Terms) == 0 {
			out = append(out, i)
		}
	}
	return out
}
