package xliff_test

import (
	"errors"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/xliff"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

const positioned = `<?xml version="1.0" encoding="UTF-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en" trgLang="de">
 <file id="checkout" original="checkout">
  <group id="g">
   <unit id="pay" name="checkout.pay">
    <segment state="final">
     <source>Pay</source>
     <target>Zahlen</target>
    </segment>
   </unit>
  </group>
  <unit id="u2" name="checkout.title"><segment><source>Checkout</source></segment></unit>
 </file>
 <file id="f2"><unit id="a"><segment state="translated"><source>A</source><target>B</target></segment></unit></file>
</xliff>`

func TestReadRecordsPositions(t *testing.T) {
	cat := read(t, []byte(positioned), xliff.ReadOptions{})
	want := []struct {
		entry, target formats.Position
	}{
		{formats.Position{Line: 5, Column: 4, Ref: "#/f=checkout/u=pay"}, formats.Position{Line: 8, Column: 6, Ref: "#/f=checkout/u=pay"}},
		{formats.Position{Line: 12, Column: 3, Ref: "#/f=checkout/u=u2"}, formats.Position{}},
		{formats.Position{Line: 14, Column: 16, Ref: "#/f=f2/u=a"}, formats.Position{Line: 14, Column: 75, Ref: "#/f=f2/u=a"}},
	}
	if len(cat.Entries) != len(want) {
		t.Fatalf("%d entries, want %d", len(cat.Entries), len(want))
	}
	for i, w := range want {
		e := cat.Entries[i]
		if e.Pos != w.entry {
			t.Errorf("entry %q pos = %+v, want %+v", e.ID, e.Pos, w.entry)
		}
		if w.target.IsZero() {
			if len(e.Targets) != 0 {
				t.Errorf("entry %q has targets", e.ID)
			}
			continue
		}
		if len(e.Targets) != 1 || e.Targets[0].Pos != w.target {
			t.Errorf("entry %q target pos = %+v, want %+v", e.ID, e.Targets, w.target)
		}
	}
}

func TestReadTargetLocaleOption(t *testing.T) {
	deAT := bcp47.MustParse("de-AT")
	withTrg := `<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en" trgLang="de">
<file id="f"><unit id="a"><segment state="final"><source>a</source><target>b</target></segment></unit></file></xliff>`
	withoutTrg := strings.Replace(withTrg, ` trgLang="de"`, "", 1)

	t.Run("trgLang by default", func(t *testing.T) {
		cat := read(t, []byte(withTrg), xliff.ReadOptions{})
		if got := cat.Entries[0].Targets[0].Locale; got != de || cat.TargetLocale != de {
			t.Errorf("target locale = %s, catalog %s; want de", got, cat.TargetLocale)
		}
	})
	t.Run("the option overrides trgLang", func(t *testing.T) {
		cat := read(t, []byte(withTrg), xliff.ReadOptions{TargetLocale: deAT})
		if got := cat.Entries[0].Targets[0].Locale; got != deAT || cat.TargetLocale != deAT {
			t.Errorf("target locale = %s, catalog %s; want de-AT", got, cat.TargetLocale)
		}
	})
	t.Run("the option names the locale a file without trgLang lacks", func(t *testing.T) {
		cat := read(t, []byte(withoutTrg), xliff.ReadOptions{TargetLocale: deAT})
		if got := cat.Entries[0].Targets[0].Locale; got != deAT || cat.TargetLocale != deAT {
			t.Errorf("target locale = %s, catalog %s; want de-AT", got, cat.TargetLocale)
		}
	})
	t.Run("a source-only file has no target locale", func(t *testing.T) {
		src := `<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.1" srcLang="en"><file id="f"><unit id="a"><segment><source>a</source></segment></unit></file></xliff>`
		if cat := read(t, []byte(src), xliff.ReadOptions{}); !cat.TargetLocale.IsZero() {
			t.Errorf("target locale = %s, want none", cat.TargetLocale)
		}
	})
	t.Run("targets without any locale stay an error", func(t *testing.T) {
		_, err := xliff.Read(strings.NewReader(withoutTrg), xliff.ReadOptions{})
		if !errors.Is(err, formats.ErrInvalid) {
			t.Errorf("err = %v, want ErrInvalid", err)
		}
	})
}
