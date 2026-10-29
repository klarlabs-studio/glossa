package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateWritesTypedAccessorsAndChecksStaleness(t *testing.T) {
	w := newWorkspace(t).withProject(nil, map[string]string{"en": sourceEN})
	var out generateJSON
	w.json(&out, "generate").want(t, ExitOK)
	if out.Messages != 3 || len(out.Files) != 5 || !out.Files[0].Changed {
		t.Fatalf("generate = %+v", out)
	}
	var kinds []string
	for _, f := range out.Files {
		kinds = append(kinds, f.Kind)
	}
	if strings.Join(kinds, ",") != "typescript,vue,react,go,dart" {
		t.Errorf("kinds = %v", kinds)
	}
	ts := w.read("src/glossa/messages.ts")
	for _, want := range []string{`"checkout.pay": { amount: number };`, "pay: (values: Messages[\"checkout.pay\"]): string"} {
		if !strings.Contains(ts, want) {
			t.Errorf("messages.ts lacks %q", want)
		}
	}
	if vue := w.read("src/glossa/glossa-vue.ts"); !strings.Contains(vue, `from "./messages.js"`) {
		t.Errorf("glossa-vue.ts = %s", vue)
	}
	if react := w.read("src/glossa/glossa-react.ts"); !strings.Contains(react, `from "./messages.js"`) ||
		!strings.Contains(react, `declare module "@klarlabs-studio/glossa/react"`) {
		t.Errorf("glossa-react.ts = %s", react)
	}
	if goSrc := w.read("internal/msg/messages.go"); !strings.Contains(goSrc, "package msg") ||
		!strings.Contains(goSrc, "func (m Messages) CheckoutPay(amount float64, opts ...glossa.Option) string") {
		t.Errorf("messages.go = %s", goSrc)
	}
	if dartSrc := w.read("lib/glossa/messages.dart"); !strings.Contains(dartSrc, "// dart format off") ||
		!strings.Contains(dartSrc, "import 'package:glossa/glossa.dart';") ||
		!strings.Contains(dartSrc, "String pay({required num amount, String? defaultText}) => translate(") ||
		!strings.Contains(dartSrc, "static const String checkoutPay = 'checkout.pay';") {
		t.Errorf("messages.dart = %s", dartSrc)
	}
	w.json(&out, "generate", "--check").want(t, ExitOK)
	w.write("locales/en.json", `{"checkout.pay": "Pay {total, number}"}`)
	r := w.run("generate", "--check")
	r.want(t, ExitCheckFailed)
	if !strings.Contains(r.stdout, "is stale") {
		t.Errorf("--check output = %s", r.stdout)
	}
}

func TestGenerateNeedsOutputs(t *testing.T) {
	w := newWorkspace(t)
	w.write("glossa.yaml", "version: 1\nserver: x\nproject: p\nsource_locale: en\ncatalogs:\n  path: locales/{locale}.json\n")
	w.write("locales/en.json", `{}`)
	var doc errorDoc
	w.json(&doc, "generate").want(t, ExitUsage)
	if doc.Error.Code != "nothing_to_generate" {
		t.Errorf("error = %+v", doc.Error)
	}
}

// fakeV03 is a Glossa v0.3 API with a de source and an en locale.
func fakeV03(t *testing.T) *httptest.Server {
	t.Helper()
	bundles := map[string]string{
		"de": `{"project":"site","locale":"de","messages":{"cart.items":"{count, plural, one {# Artikel} other {# Artikel}}","home.title":"Willkommen","legal.note":""},"statuses":{"cart.items":"approved","home.title":"approved"}}`,
		"en": `{"project":"site","locale":"en","messages":{"cart.items":"{count, plural, one {# item} other {# items}}","home.title":"Welcome","legal.note":""},"statuses":{"cart.items":"approved","home.title":"ai_translated"}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer glossa_v03key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/projects/site/locales":
			_, _ = io.WriteString(w, `[{"code":"de"},{"code":"en"}]`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/projects/site/locales/"):
			locale := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/site/locales/"), "/messages")
			_, _ = io.WriteString(w, bundles[locale])
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImportFromV03IsIdempotentAndMapsStatuses(t *testing.T) {
	srv := newFakeServer(t)
	srv.sourceLocale, srv.locales = "de", []string{"de"}
	old := fakeV03(t)
	w := newWorkspace(t).withProject(srv, nil)
	w.write("glossa.yaml", strings.Replace(w.read("glossa.yaml"), "source_locale: en", "source_locale: de", 1))
	w.env["GLOSSA_V0_KEY"] = "glossa_v03key"
	args := []string{"import", "--from", "v0", "--v0-url", old.URL, "--v0-project", "site"}

	var dry importJSON
	w.json(&dry, append(args, "--dry-run")...).want(t, ExitOK)
	if dry.Summary["message"]["planned"] != 2 || dry.Summary["translation"]["planned"] != 2 || srv.countRequests("POST") != 0 {
		t.Fatalf("dry run = %+v", dry.Summary)
	}

	var out importJSON
	w.json(&out, args...).want(t, ExitOK)
	if strings.Join(out.LocalesAdded, ",") != "en" || out.Summary["message"]["created"] != 2 || out.Summary["translation"]["created"] != 2 {
		t.Fatalf("import = %+v", out)
	}
	byKey := map[string]importItem{}
	for _, it := range out.Items {
		if it.Kind == "translation" {
			byKey[it.Key] = it
		}
	}
	// approved in v0.3, but a token can't approve where review is required.
	if it := byKey["cart.items"]; it.V0Status != "approved" || it.State != "needs_review" || !it.Downgraded {
		t.Errorf("cart.items = %+v", it)
	}
	if it := byKey["home.title"]; it.V0Status != "ai_translated" || it.State != "needs_review" || it.Downgraded {
		t.Errorf("home.title = %+v", it)
	}
	if tr := srv.translations["en"]["home.title"]; tr.origin != "import" {
		t.Errorf("provenance = %+v", tr)
	}

	// A reviewer approves one; a re-run changes nothing and keeps it approved.
	srv.translations["en"]["cart.items"].state = "approved"
	w.json(&out, args...).want(t, ExitOK)
	if out.Summary["message"]["unchanged"] != 2 || out.Summary["translation"]["unchanged"] != 2 || len(out.LocalesAdded) != 0 {
		t.Errorf("re-run = %+v", out.Summary)
	}
	if srv.translations["en"]["cart.items"].state != "approved" {
		t.Error("the re-run undid a review")
	}
}

func TestImportFromV03WithoutReviewKeepsApprovals(t *testing.T) {
	srv := newFakeServer(t)
	srv.sourceLocale, srv.locales, srv.reviewRequired = "de", []string{"de", "en"}, false
	old := fakeV03(t)
	w := newWorkspace(t).withProject(srv, nil)
	w.write("glossa.yaml", strings.Replace(w.read("glossa.yaml"), "source_locale: en", "source_locale: de", 1))
	w.env["GLOSSA_V0_KEY"] = "glossa_v03key"
	var out importJSON
	w.json(&out, "import", "--from", "v0", "--v0-url", old.URL+"/api/v1", "--v0-project", "site").want(t, ExitOK)
	if srv.translations["en"]["cart.items"].state != "approved" || srv.translations["en"]["home.title"].state != "needs_review" {
		t.Errorf("states = %s / %s", srv.translations["en"]["cart.items"].state, srv.translations["en"]["home.title"].state)
	}
}

func TestImportUsageErrors(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	var doc errorDoc
	w.json(&doc, "import", "--from", "crowdin").want(t, ExitUsage)
	w.json(&doc, "import", "--from", "v0").want(t, ExitUsage)
	w.json(&doc, "import", "--from", "v0", "--v0-url", "http://x").want(t, ExitUsage)
	if doc.Error.Code != "no_v0_key" {
		t.Errorf("error = %+v", doc.Error)
	}
	old := fakeV03(t)
	w.env["GLOSSA_V0_KEY"] = "wrong"
	w.json(&doc, "import", "--from", "v0", "--v0-url", old.URL, "--v0-project", "site").want(t, ExitNetwork)
	if doc.Error.Code != "v0_unauthenticated" {
		t.Errorf("error = %+v", doc.Error)
	}
	w.env["GLOSSA_V0_KEY"] = "glossa_v03key"
	srv.sourceLocale = "fr"
	w.write("glossa.yaml", strings.Replace(w.read("glossa.yaml"), "source_locale: en", "source_locale: fr", 1))
	w.json(&doc, "import", "--from", "v0", "--v0-url", old.URL, "--v0-project", "site").want(t, ExitUsage)
	if doc.Error.Code != "source_locale_missing" || !strings.Contains(doc.Error.Why, "de, en") {
		t.Errorf("error = %+v", doc.Error)
	}
}
