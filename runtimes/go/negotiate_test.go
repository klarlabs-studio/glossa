package glossa_test

import (
	"reflect"
	"testing"

	glossa "go.klarlabs.de/glossa/runtimes/go"
)

func TestResolveLocale(t *testing.T) {
	set := glossa.LocaleSet{
		SourceLocale: "en",
		Locales:      []string{"en", "de", "de-AT", "fr"},
		Fallback:     map[string][]string{"de-AT": {"de"}, "*": {"en"}},
	}
	tests := []struct {
		name       string
		requested  []string
		wantActive string
		wantChain  []string
	}{
		{name: "exact", requested: []string{"de"}, wantActive: "de", wantChain: []string{"de", "en"}},
		{
			name: "explicit fallback", requested: []string{"de-AT"}, wantActive: "de-AT",
			wantChain: []string{"de-AT", "de", "en"},
		},
		{name: "lookup truncation", requested: []string{"de-CH"}, wantActive: "de", wantChain: []string{"de", "en"}},
		{
			name: "nothing matches falls back to the source", requested: []string{"ja"},
			wantActive: "en", wantChain: []string{"en"},
		},
		{
			name: "malformed tags are dropped", requested: []string{"!!", "fr"}, wantActive: "fr",
			wantChain: []string{"fr", "en"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, active, chain := glossa.ResolveLocale(set, tc.requested)
			if active != tc.wantActive || !reflect.DeepEqual(chain, tc.wantChain) {
				t.Fatalf("active = %q, chain = %v; want %q, %v", active, chain, tc.wantActive, tc.wantChain)
			}
		})
	}
}
