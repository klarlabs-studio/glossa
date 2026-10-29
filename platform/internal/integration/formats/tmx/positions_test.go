package tmx_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
)

func TestReadRecordsPositions(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
 <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" o-tmf="x" creationtool="x" creationtoolversion="1"/>
 <body>
  <tu tuid="pay">
   <tuv xml:lang="en"><seg>Pay</seg></tuv>
   <tuv xml:lang="de"><seg>Zahlen</seg></tuv>
   <tuv xml:lang="fr"><seg>Payer</seg></tuv>
  </tu>
  <tu><tuv xml:lang="en"><seg>Cart</seg></tuv><tuv xml:lang="de"><seg>Warenkorb</seg></tuv></tu>
 </body>
</tmx>`
	units := readAll(t, []byte(doc))
	want := []formats.Position{
		{Line: 7, Column: 4, Ref: "tu[1]"},
		{Line: 8, Column: 4, Ref: "tu[1]"},
		{Line: 10, Column: 47, Ref: "tu[2]"},
	}
	if len(units) != len(want) {
		t.Fatalf("%d units, want %d", len(units), len(want))
	}
	for i, w := range want {
		if units[i].Pos != w {
			t.Errorf("unit %d (%s) pos = %+v, want %+v", i, units[i].TargetLocale, units[i].Pos, w)
		}
	}
}
