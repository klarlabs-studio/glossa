package po_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/po"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

func TestReadRecordsPositions(t *testing.T) {
	cat := readFile(t, "testdata/de.po", po.ReadOptions{})
	if cat.TargetLocale != bcp47.MustParse("de-DE") {
		t.Errorf("target locale = %s, want de-DE", cat.TargetLocale)
	}
	want := []struct {
		entry  formats.Position
		target int // msgstr line; 0 without a translation
	}{
		{formats.Position{Line: 15, Column: 1, Ref: `msgid "Checkout"`}, 16},
		{formats.Position{Line: 20, Column: 1, Ref: `msgid "Hello %s, you have {braces} and \"quotes\""`}, 21},
		{formats.Position{Line: 23, Column: 1, Ref: `msgctxt "verb" msgid "Open"`}, 25},
		{formats.Position{Line: 27, Column: 1, Ref: `msgctxt "adjective" msgid "Open"`}, 29},
		{formats.Position{Line: 34, Column: 1, Ref: `msgid "One item"`}, 36},
		{formats.Position{Line: 39, Column: 1, Ref: `msgid "Untranslated"`}, 0},
		{formats.Position{Line: 42, Column: 1, Ref: `msgid "Line one\nLine two\ttabbed \\ backslash"`}, 44},
	}
	if len(cat.Entries) != len(want) {
		t.Fatalf("%d entries, want %d", len(cat.Entries), len(want))
	}
	for i, w := range want {
		e := cat.Entries[i]
		if e.Pos != w.entry {
			t.Errorf("entry %d pos = %+v, want %+v", i, e.Pos, w.entry)
		}
		if w.target == 0 {
			continue
		}
		wantTarget := formats.Position{Line: w.target, Column: 1, Ref: w.entry.Ref}
		if len(e.Targets) != 1 || e.Targets[0].Pos != wantTarget {
			t.Errorf("entry %d target = %+v, want pos %+v", i, e.Targets, wantTarget)
		}
	}
}
