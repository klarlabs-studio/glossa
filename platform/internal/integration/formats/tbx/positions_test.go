package tbx_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
)

func TestReadRecordsPositions(t *testing.T) {
	doc := `<?xml version="1.0" encoding="utf-8"?>
<tbx type="TBX-Basic" style="dca" xml:lang="en" xmlns="urn:iso:std:iso:30042:ed-2">
 <tbxHeader><fileDesc><sourceDesc><p>x</p></sourceDesc></fileDesc></tbxHeader>
 <text><body>
  <conceptEntry id="c1">
   <langSec xml:lang="en"><termSec><term>cart</term></termSec></langSec>
  </conceptEntry>
  <conceptEntry id="c2"><langSec xml:lang="en"><termSec><term>basket</term></termSec></langSec></conceptEntry>
 </body></text>
</tbx>`
	tb := read(t, []byte(doc))
	want := []formats.Position{
		{Line: 5, Column: 3, Ref: "conceptEntry[1]"},
		{Line: 8, Column: 3, Ref: "conceptEntry[2]"},
	}
	if len(tb.Concepts) != len(want) {
		t.Fatalf("%d concepts", len(tb.Concepts))
	}
	for i, w := range want {
		if tb.Concepts[i].Pos != w {
			t.Errorf("concept %s pos = %+v, want %+v", tb.Concepts[i].ID, tb.Concepts[i].Pos, w)
		}
	}
}
