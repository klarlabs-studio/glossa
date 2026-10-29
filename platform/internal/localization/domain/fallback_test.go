package domain_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

func tags(ss ...string) []bcp47.Tag {
	out := make([]bcp47.Tag, len(ss))
	for i, s := range ss {
		out[i] = bcp47.MustParse(s)
	}
	return out
}

var projectLocales = tags("de", "de-AT", "de-CH", "en", "en-GB", "fr", "fr-CA")

func TestFallbackGraphCanonicalizes(t *testing.T) {
	g, err := domain.NewFallbackGraph(map[string][]string{
		"de_at": {"de-ch", "DE"},
		"*":     {"en"},
		"fr-CA": {},
	}, projectLocales)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"de-AT": {"de-CH", "de"}, "*": {"en"}}
	if got := g.Edges(); !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %v, want %v", got, want)
	}
	if !g.Mentions(bcp47.MustParse("de-CH")) || g.Mentions(bcp47.MustParse("fr")) {
		t.Error("Mentions is wrong")
	}
}

func TestFallbackGraphRejects(t *testing.T) {
	cases := map[string]struct {
		edges map[string][]string
		code  string
	}{
		"unknown key":        {map[string][]string{"it": {"en"}}, "fallback_unknown_locale"},
		"unknown target":     {map[string][]string{"de": {"it"}}, "fallback_unknown_locale"},
		"invalid tag":        {map[string][]string{"de": {"not a tag"}}, "fallback_invalid_locale"},
		"star as target":     {map[string][]string{"de": {"*"}}, "fallback_invalid_locale"},
		"self reference":     {map[string][]string{"de": {"de"}}, "fallback_self_reference"},
		"duplicate target":   {map[string][]string{"de-AT": {"de", "de"}}, "fallback_duplicate"},
		"duplicate key":      {map[string][]string{"de-AT": {"de"}, "de_AT": {"en"}}, "fallback_duplicate"},
		"two-cycle":          {map[string][]string{"de": {"de-AT"}, "de-AT": {"de"}}, "fallback_cycle"},
		"three-cycle":        {map[string][]string{"de-AT": {"de-CH"}, "de-CH": {"de"}, "de": {"de-AT"}}, "fallback_cycle"},
		"cycle behind chain": {map[string][]string{"fr-CA": {"fr"}, "fr": {"en"}, "en": {"en-GB"}, "en-GB": {"fr"}}, "fallback_cycle"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := domain.NewFallbackGraph(tc.edges, projectLocales)
			var fe *domain.FallbackError
			if !errors.As(err, &fe) || fe.Code != tc.code {
				t.Errorf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

// "*" is not a node: pointing every locale at en while en has its own
// chain is not a cycle.
func TestDefaultChainIsNotACycle(t *testing.T) {
	_, err := domain.NewFallbackGraph(map[string][]string{"*": {"en"}, "en": {"de"}}, projectLocales)
	if err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestChainFollowsSpec(t *testing.T) {
	g, err := domain.NewFallbackGraph(map[string][]string{
		"de-AT": {"de-CH"}, "de-CH": {"de"}, "*": {"en"}, "en": {"en-GB"},
	}, projectLocales)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		// Explicit edges, depth-first, then "*" expanded, then the source.
		"de-AT": "de-AT,de-CH,de,en,en-GB,fr",
		// No entry: available truncations, then "*".
		"fr-CA": "fr-CA,fr,en,en-GB",
		"en":    "en,en-GB,fr",
	}
	for l, want := range cases {
		got := g.Chain(bcp47.MustParse(l), projectLocales, bcp47.MustParse("fr"))
		if s := join(got); s != want {
			t.Errorf("Chain(%s) = %s, want %s", l, s, want)
		}
	}
}

func join(ts []bcp47.Tag) string {
	s := make([]string, len(ts))
	for i, t := range ts {
		s[i] = t.String()
	}
	return strings.Join(s, ",")
}
