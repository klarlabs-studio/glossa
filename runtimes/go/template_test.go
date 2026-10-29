package glossa

import (
	htmltemplate "html/template"
	"slices"
	"strings"
	"sync"
	"testing"
	texttemplate "text/template"
)

var emailCatalogs = map[string]map[string]string{
	"en": {
		"email.subject":  "Welcome, {$name}!",
		"email.greeting": "Hi {$name} <3",
		"email.items":    ".input {$n :number} .match $n one {{one item}} * {{{$n} items}}",
	},
	"ar": {"email.subject": "مرحبا {$name}!"},
}

func emailClient(t *testing.T, log *errorLog) *Client {
	t.Helper()
	rel := buildRelease(t, "rel_1", 1, emailCatalogs, "en", "ar")
	cfg := Config{Bundled: rel.fs(), DisableBidiIsolation: true, OnError: allowMissing(t)}
	if log != nil {
		cfg.OnError = log.handle
	}
	return newTestClient(t, cfg)
}

func TestHTMLTemplateFuncs(t *testing.T) {
	c := emailClient(t, nil)
	base := htmltemplate.Must(htmltemplate.New("email").Funcs(TemplateFuncs()).Parse(
		`<html lang="{{lang}}" dir="{{dir}}"><h1>{{t "email.subject" "name" .Name}}</h1>` +
			`<p>{{t "email.greeting" .Args}}</p><p>{{t "email.items" "n" .N}}</p>` +
			`<p>{{td "email.footer" "Thanks for joining"}}</p></html>`))

	render := func(locale string) string {
		tmpl := htmltemplate.Must(base.Clone())
		var b strings.Builder
		data := map[string]any{"Name": "Ada", "N": 2, "Args": Args{"name": "Ada"}}
		if err := tmpl.Funcs(c.For(locale).FuncMap()).Execute(&b, data); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	en := `<html lang="en" dir="ltr"><h1>Welcome, Ada!</h1><p>Hi Ada &lt;3</p><p>2 items</p><p>Thanks for joining</p></html>`
	if got := render("en-GB"); got != en {
		t.Fatalf("en:\n got %s\nwant %s", got, en)
	}
	if got := render("ar"); !strings.HasPrefix(got, `<html lang="ar" dir="rtl"><h1>مرحبا Ada!</h1><p>Hi Ada &lt;3</p>`) {
		t.Fatalf("ar: %s", got)
	}
}

func TestTextTemplateFuncs(t *testing.T) {
	c := emailClient(t, nil)
	tmpl := texttemplate.Must(texttemplate.New("cli").Funcs(c.For("en").FuncMap()).Parse(`{{t "email.greeting" "name" "Bob"}} [{{lang}}/{{dir}}]`))
	var b strings.Builder
	if err := tmpl.Execute(&b, nil); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "Hi Bob <3 [en/ltr]" {
		t.Fatalf("got %q", got)
	}
}

func TestTemplateFuncsPlaceholders(t *testing.T) {
	tmpl := texttemplate.Must(texttemplate.New("x").Funcs(TemplateFuncs()).Parse(`{{t "a.b" "k" 1}}|{{td "c" "Default"}}|{{lang}}|{{dir}}`))
	var b strings.Builder
	if err := tmpl.Execute(&b, nil); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "a.b|Default||ltr" {
		t.Fatalf("got %q", got)
	}
}

func TestTemplateArgsMisuseIsReportedNotFatal(t *testing.T) {
	log := &errorLog{}
	c := emailClient(t, log)
	tmpl := texttemplate.Must(texttemplate.New("x").Funcs(c.For("en").FuncMap()).Parse(`{{t "email.subject" "name" "Ada" 42}}|{{t "email.subject" 7 "x"}}`))
	var b strings.Builder
	if err := tmpl.Execute(&b, nil); err != nil {
		t.Fatalf("template execution must not fail: %v", err)
	}
	if got := b.String(); got != "Welcome, Ada!|Welcome, {$name}!" {
		t.Fatalf("got %q", got)
	}
	if n := len(log.all()); n == 0 || !slices.Contains(log.types(), ErrorFormat) {
		t.Fatalf("misuse not reported: %v", log.all())
	}
}

func TestTemplateFuncsConcurrentExecution(t *testing.T) {
	c := emailClient(t, nil)
	base := htmltemplate.Must(htmltemplate.New("e").Funcs(TemplateFuncs()).Parse(`{{t "email.subject" "name" .}}`))
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			locale := []string{"en", "ar"}[i%2]
			tmpl := htmltemplate.Must(base.Clone())
			var b strings.Builder
			if err := tmpl.Funcs(c.For(locale).FuncMap()).Execute(&b, "Ada"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
