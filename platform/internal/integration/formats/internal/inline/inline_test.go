package inline_test

import (
	"reflect"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/inline"
)

func spans(t *testing.T, src string) []inline.Span {
	t.Helper()
	m, err := mf.ParseMF2(src)
	if err != nil {
		t.Fatal(err)
	}
	s, err := inline.Spans(m.Pattern)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSpansPairsNestedMarkup(t *testing.T) {
	got := spans(t, "a {$x :number} {#b}c{#i}d{/i}{/b} {#br/}{#u}{/s}")
	var kinds []inline.Kind
	var texts []string
	for _, s := range got {
		kinds = append(kinds, s.Kind)
		texts = append(texts, s.Text)
	}
	wantKinds := []inline.Kind{
		inline.Text, inline.Placeholder, inline.Text, inline.Open, inline.Text, inline.Open, inline.Text,
		inline.Close, inline.Close, inline.Text, inline.Placeholder, inline.IsolatedOpen, inline.IsolatedClose,
	}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Errorf("kinds = %v, want %v", kinds, wantKinds)
	}
	if texts[1] != "{$x :number}" || texts[3] != "{#b}" || texts[8] != "{/b}" || texts[10] != "{#br /}" {
		t.Errorf("texts = %q", texts)
	}
	if got[3].Pair != 8 || got[8].Pair != 3 || got[5].Pair != 7 {
		t.Errorf("pairs: %+v", got)
	}
}

func TestSpansLeavesCrossingMarkupIsolated(t *testing.T) {
	for _, s := range spans(t, "{#a}{#b}{/a}{/b}") {
		if s.Kind == inline.Open || s.Kind == inline.Close {
			t.Fatalf("crossing markup paired: %+v", s)
		}
	}
}

func TestAssignIDsReusesSourceIDsInTarget(t *testing.T) {
	src := spans(t, "{$a} and {$b} {#b}x{/b} {$a}")
	tgt := spans(t, "{$b} und {$a} {$a} {#b}y{/b} {$new}")
	inline.AssignIDs(src, tgt)
	ids := func(ss []inline.Span) []string {
		var out []string
		for _, s := range ss {
			if s.ID != "" {
				out = append(out, s.Text+"="+s.ID)
			}
		}
		return out
	}
	if got, want := ids(src), []string{"{$a}=1", "{$b}=2", "{#b}=3", "{/b}=3", "{$a}=4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("source ids = %v, want %v", got, want)
	}
	if got, want := ids(tgt), []string{"{$b}=2", "{$a}=1", "{$a}=4", "{#b}=3", "{/b}=3", "{$new}=5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("target ids = %v, want %v", got, want)
	}
}

func TestParseElement(t *testing.T) {
	for _, ok := range []string{"{$x}", "{#b}", "{/b}", "{#br/}", "{|lit| :string @a=b}"} {
		if _, err := inline.ParseElement(ok); err != nil {
			t.Errorf("ParseElement(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "text", "{$x}{$y}", "a{$x}", "{{x}}", ".input {$x} {{{$x}}}", "{"} {
		if el, err := inline.ParseElement(bad); err == nil {
			t.Errorf("ParseElement(%q) = %#v", bad, el)
		}
	}
}

func TestBuilderMergesText(t *testing.T) {
	var b inline.Builder
	if len(b.Pattern()) != 0 || b.Pattern() == nil {
		t.Error("empty builder pattern must be empty, not nil")
	}
	b.Text("a")
	b.Text("")
	b.Text("b")
	b.Element(mf.Expression{Arg: mf.VariableRef{Name: "x"}})
	b.Text("c")
	want := mf.Pattern{mf.Text("ab"), mf.Expression{Arg: mf.VariableRef{Name: "x"}}, mf.Text("c")}
	if !reflect.DeepEqual(b.Pattern(), want) {
		t.Errorf("pattern = %#v", b.Pattern())
	}
}
