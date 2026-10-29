package glossa

import (
	"encoding/json"
	htmltemplate "html/template"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	texttemplate "text/template"
)

// runtimes/testdata/markup.json: the safe-markup rules shared with
// @klarlabs-studio/glossa/elements (SAFE_TAGS, parts → HTML).
const markupFixture = "../testdata/markup.json"

type markupCase struct {
	Description string `json:"description"`
	Parts       []struct {
		Type    PartType       `json:"type"`
		Value   string         `json:"value"`
		Source  string         `json:"source"`
		Kind    MarkupKind     `json:"kind"`
		Name    string         `json:"name"`
		Options map[string]any `json:"options"`
		Parts   []struct {
			Value string `json:"value"`
		} `json:"parts"`
	} `json:"parts"`
	HTML string `json:"html"`
}

// parts converts the fixture's MF2-shaped parts: a value's text is its
// value, its joined sub-parts, or {source} for a fallback.
func (c markupCase) parts() []Part {
	out := make([]Part, len(c.Parts))
	for i, p := range c.Parts {
		part := Part{Type: p.Type, Value: p.Value, Source: p.Source, Kind: p.Kind, Name: p.Name, Options: p.Options}
		for _, sub := range p.Parts {
			part.Value += sub.Value
		}
		if p.Type == PartFallback {
			part.Value = "{" + p.Source + "}"
		}
		out[i] = part
	}
	return out
}

func TestMarkupFixture(t *testing.T) {
	b, err := os.ReadFile(markupFixture)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SafeTags []string     `json:"safeTags"`
		VoidTags []string     `json:"voidTags"`
		Cases    []markupCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if got, want := slices.Sorted(maps.Keys(safeTags)), slices.Sorted(slices.Values(fixture.SafeTags)); !slices.Equal(got, want) {
		t.Errorf("safeTags = %v, want the shared list %v", got, want)
	}
	if got, want := slices.Sorted(maps.Keys(voidTags)), slices.Sorted(slices.Values(fixture.VoidTags)); !slices.Equal(got, want) {
		t.Errorf("voidTags = %v, want %v", got, want)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range fixture.Cases {
		if got := partsHTML(c.parts()); got != c.HTML {
			t.Errorf("%s:\n got %s\nwant %s", c.Description, got, c.HTML)
		}
	}
}

var markupCatalogs = map[string]map[string]string{
	"en": {
		"terms":   "By continuing you accept the {#link href=|/terms|}terms of service{/link}.",
		"welcome": "Hi {#b}{$name}{/b}, you have {#strong}{$n :integer} new{/strong} messages.{#br/}Bye",
		"styles":  "a{#b}b{#i}c{#u}d{/u}{/i}{/b}{#em}e{/em}{#strong}f{/strong}{#span}g{/span}{#br/}h{#b}{/b}",
		"crossed": "{#b}1{#i}2{/b}3{/i}",
		"only":    "{#br/}",
		"broken":  "Hi {$name :nope}",
	},
	"ar": {"welcome": "مرحبا {#b}{$name}{/b}"},
}

func markupClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	cfg.Bundled = buildRelease(t, "rel_markup", 1, markupCatalogs, "en", "ar").fs()
	return newTestClient(t, cfg)
}

func TestLocalizerParts(t *testing.T) {
	c := markupClient(t, Config{DisableBidiIsolation: true})
	got := c.For("en").Parts("terms", nil)
	want := []Part{
		{Type: PartText, Value: "By continuing you accept the "},
		{Type: PartMarkup, Kind: MarkupOpen, Name: "link", Options: map[string]any{"href": "/terms"}},
		{Type: PartText, Value: "terms of service"},
		{Type: PartMarkup, Kind: MarkupClose, Name: "link"},
		{Type: PartText, Value: "."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parts\n got %+v\nwant %+v", got, want)
	}
	parts := c.For("en").Parts("welcome", Args{"name": "Ada", "n": 1200})
	if s := PartsText(parts); s != "Hi Ada, you have 1,200 new messages.Bye" {
		t.Errorf("parts text %q", s)
	}
	if p := parts[2]; p.Type != PartString || p.Value != "Ada" || p.Source != "$name" {
		t.Errorf("placeholder part %+v", p)
	}
}

func TestPartsBidiIsolation(t *testing.T) {
	c := markupClient(t, Config{})
	on := c.For("en").Parts("welcome", Args{"name": "Ada", "n": 2})
	if !slices.ContainsFunc(on, func(p Part) bool { return p.Type == PartBidiIsolation }) {
		t.Errorf("isolation is on by default: %+v", on)
	}
	off := c.For("en").Parts("welcome", Args{"name": "Ada", "n": 2}, BidiIsolation(false))
	if slices.ContainsFunc(off, func(p Part) bool { return p.Type == PartBidiIsolation }) {
		t.Errorf("BidiIsolation(false) keeps isolation parts: %+v", off)
	}
	if got := c.For("ar").HTML("welcome", Args{"name": "Ada"}); got != "مرحبا <b>\u2068Ada\u2069</b>" {
		t.Errorf("HTML isolated: %q", got)
	}
	if got := c.For("ar").HTML("welcome", Args{"name": "Ada"}, BidiIsolation(false)); got != "مرحبا <b>Ada</b>" {
		t.Errorf("HTML not isolated: %q", got)
	}
	if got := c.For("ar").Runs("welcome", Args{"name": "Ada"}); got[1].Text != "\u2068Ada\u2069" {
		t.Errorf("Runs isolated: %+v", got)
	}
	if got := c.For("ar").Runs("welcome", Args{"name": "Ada"}, BidiIsolation(false)); got[1].Text != "Ada" {
		t.Errorf("Runs not isolated: %+v", got)
	}
}

func TestPartsFallbacksMatchT(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		opts     []Option
		want     string
		errType  ErrorType
	}{
		{"missing message renders its ID", "nope", nil, "nope", ErrorMissingMessage},
		{"missing message renders the inline default", "nope", []Option{Default("<Hi> & bye")}, "<Hi> & bye", ErrorMissingMessage},
		{"empty output renders the ID", "only", nil, "only", ""},
		{"format errors render the fallback and are reported", "broken", nil, "Hi {$name}", ErrorFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &errorLog{}
			c := markupClient(t, Config{DisableBidiIsolation: true, OnError: log.handle})
			l := c.For("en")
			args := Args{"name": "Ada"}
			if got := l.T(tc.id, args, tc.opts...); got != tc.want {
				t.Fatalf("T = %q, want %q", got, tc.want)
			}
			if got := l.Parts(tc.id, args, tc.opts...); PartsText(got) != tc.want {
				t.Errorf("Parts = %+v, want text %q", got, tc.want)
			}
			if got, want := l.HTML(tc.id, args, tc.opts...), htmltemplate.HTML(escapeText(tc.want)); got != want {
				t.Errorf("HTML = %q, want %q", got, want)
			}
			if got := l.Runs(tc.id, args, tc.opts...); !reflect.DeepEqual(got, []Run{{Text: tc.want}}) {
				t.Errorf("Runs = %+v", got)
			}
			// T, Parts, HTML and Runs report the same error each; the
			// reporter rate-limits the repeats.
			var types []ErrorType
			if tc.errType != "" {
				types = []ErrorType{tc.errType}
			}
			if got := log.types(); !slices.Equal(got, types) {
				t.Errorf("reported %v, want %v", got, types)
			}
		})
	}
}

func TestLocalizerHTML(t *testing.T) {
	c := markupClient(t, Config{DisableBidiIsolation: true})
	l := c.For("en")
	if got := l.HTML("terms", nil); got != "By continuing you accept the terms of service." {
		t.Errorf("a link from a translation must stay text: %q", got)
	}
	got := l.HTML("welcome", Args{"name": `<script>alert("x")</script>`, "n": 3})
	want := htmltemplate.HTML(`Hi <b>&lt;script&gt;alert("x")&lt;/script&gt;</b>, you have <strong>3 new</strong> messages.<br>Bye`)
	if got != want {
		t.Errorf("HTML\n got %s\nwant %s", got, want)
	}
	if got := l.HTML("crossed", nil); got != "<b>1<i>2</i></b>3" {
		t.Errorf("crossed: %s", got)
	}
}

func TestTemplateTH(t *testing.T) {
	c := markupClient(t, Config{DisableBidiIsolation: true, OnError: allowMissing(t)})
	base := htmltemplate.Must(htmltemplate.New("e").Funcs(TemplateFuncs()).Parse(
		`<p>{{th "welcome" "name" .Name "n" 2}}</p><p>{{th "terms"}}</p><p title="{{th "welcome" "name" "A" "n" 1}}">{{th "nope"}}</p>`))
	tmpl := htmltemplate.Must(base.Clone())
	var b strings.Builder
	if err := tmpl.Funcs(c.For("en").FuncMap()).Execute(&b, map[string]any{"Name": "<Ada>"}); err != nil {
		t.Fatal(err)
	}
	want := `<p>Hi <b>&lt;Ada&gt;</b>, you have <strong>2 new</strong> messages.<br>Bye</p>` +
		`<p>By continuing you accept the terms of service.</p>` +
		`<p title="Hi A, you have 1 new messages.Bye">nope</p>`
	if got := b.String(); got != want {
		t.Errorf("th\n got %s\nwant %s", got, want)
	}

	var p strings.Builder
	placeholder := texttemplate.Must(texttemplate.New("p").Funcs(TemplateFuncs()).Parse(`{{th "a.<b>" "k" 1}}`))
	if err := placeholder.Execute(&p, nil); err != nil {
		t.Fatal(err)
	}
	if p.String() != "a.&lt;b&gt;" {
		t.Errorf("th placeholder: %q", p.String())
	}
}

func TestLocalizerRuns(t *testing.T) {
	c := markupClient(t, Config{DisableBidiIsolation: true})
	got := c.For("en").Runs("styles", nil)
	want := []Run{
		{Text: "a"},
		{Text: "b", Bold: true},
		{Text: "c", Bold: true, Italic: true},
		{Text: "d", Bold: true, Italic: true, Underline: true},
		{Text: "e", Italic: true},
		{Text: "f", Bold: true},
		{Text: "g\nh"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Runs\n got %+v\nwant %+v", got, want)
	}
	if got := c.For("en").Runs("crossed", nil); !reflect.DeepEqual(got, []Run{
		{Text: "1", Bold: true}, {Text: "2", Bold: true, Italic: true}, {Text: "3"},
	}) {
		t.Errorf("crossed: %+v", got)
	}
	if got := c.For("en").Runs("terms", nil); !reflect.DeepEqual(got, []Run{{Text: "By continuing you accept the terms of service."}}) {
		t.Errorf("unsafe markup keeps its text in the current style: %+v", got)
	}
}

func TestRunStyle(t *testing.T) {
	for run, want := range map[Run]string{
		{}:                            "",
		{Bold: true}:                  "B",
		{Italic: true}:                "I",
		{Underline: true}:             "U",
		{Bold: true, Italic: true}:    "BI",
		{Bold: true, Underline: true}: "BU",
		{Bold: true, Italic: true, Underline: true}: "BIU",
	} {
		if got := run.Style(); got != want {
			t.Errorf("%+v.Style() = %q, want %q", run, got, want)
		}
	}
}
