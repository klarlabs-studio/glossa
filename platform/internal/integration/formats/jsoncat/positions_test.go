package jsoncat_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/jsoncat"
)

func TestReadRecordsPositions(t *testing.T) {
	doc := "{\n" +
		"  \"checkout\": {\n" +
		"    \"pay\": \"Pay\",\n" +
		"    \"a/b~c\": {\"d\": \"x\"}\n" +
		"  },\n" +
		"  \"app.name\": \"Glossa\"\n" +
		"}\n"
	for _, tc := range []struct {
		name   string
		opts   jsoncat.ReadOptions
		target bool
	}{
		{"source catalog", jsoncat.ReadOptions{Locale: en}, false},
		{"translations", jsoncat.ReadOptions{Locale: de, SourceLocale: en}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cat := read(t, []byte(doc), tc.opts)
			want := map[string]formats.Position{
				"checkout.pay":     {Line: 3, Column: 5, Ref: "/checkout/pay"},
				"checkout.a/b~c.d": {Line: 4, Column: 15, Ref: "/checkout/a~1b~0c/d"},
				"app.name":         {Line: 6, Column: 3, Ref: "/app.name"},
			}
			if len(cat.Entries) != len(want) {
				t.Fatalf("%d entries", len(cat.Entries))
			}
			for _, e := range cat.Entries {
				if e.Pos != want[e.ID] {
					t.Errorf("%s pos = %+v, want %+v", e.ID, e.Pos, want[e.ID])
				}
				if tc.target && (len(e.Targets) != 1 || e.Targets[0].Pos != want[e.ID]) {
					t.Errorf("%s target pos = %+v, want %+v", e.ID, e.Targets, want[e.ID])
				}
			}
			if tc.target && cat.TargetLocale != de || !tc.target && !cat.TargetLocale.IsZero() {
				t.Errorf("target locale = %q", cat.TargetLocale)
			}
		})
	}
}
