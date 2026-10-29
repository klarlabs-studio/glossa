//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

type message struct {
	ID             string `json:"id"`
	Key            string `json:"key"`
	State          string `json:"state"`
	SourceRevision int    `json:"source_revision"`
	Source         struct {
		Text      string         `json:"text"`
		Syntax    string         `json:"syntax"`
		Model     map[string]any `json:"model"`
		Arguments []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"arguments"`
	} `json:"source"`
}

type translation struct {
	Locale                string `json:"locale"`
	Text                  string `json:"text"`
	State                 string `json:"state"`
	Origin                string `json:"origin"`
	SourceRevision        int    `json:"source_revision"`
	CurrentSourceRevision int    `json:"current_source_revision"`
	Outdated              bool   `json:"outdated"`
	Revision              int    `json:"revision"`
}

// TestCoreLoopOverHTTP is the M1 catalog flow through the generated
// server: create a project, add locales, push messages in ICU MF1 with an
// API token, translate, revise the source, and watch the translation
// become outdated once Localization has processed the event.
func TestCoreLoopOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID

	// A project; the source locale is canonicalized.
	create := call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
		body:    map[string]any{"slug": "shop", "name": "Shop", "source_locale": "en"},
		headers: map[string]string{"Idempotency-Key": "p1"}}
	r := s.do(create)
	r.want(t, http.StatusCreated, "")
	var project struct {
		ID           string `json:"id"`
		SourceLocale string `json:"source_locale"`
		Settings     struct {
			DefaultSyntax  string `json:"default_syntax"`
			ReviewRequired bool   `json:"review_required"`
		} `json:"settings"`
	}
	r.decode(t, &project)
	if project.SourceLocale != "en" || project.Settings.DefaultSyntax != "mf1" || !project.Settings.ReviewRequired ||
		r.header.Get("ETag") != `"1"` || r.header.Get("Location") != base+"/projects/"+project.ID {
		t.Fatalf("project = %s %v", r.body, r.header)
	}
	if replay := s.do(create); replay.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("replay = %v", replay.header)
	}
	p := base + "/projects/" + project.ID

	// Locales.
	r = s.do(call{method: "POST", path: p + "/locales", cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"code": "de_de"}})
	r.want(t, http.StatusCreated, "")
	var loc struct {
		Code      string `json:"code"`
		Direction string `json:"direction"`
	}
	r.decode(t, &loc)
	if loc.Code != "de-DE" || r.header.Get("Location") != p+"/locales/de-DE" {
		t.Errorf("locale = %+v %v", loc, r.header)
	}
	s.do(call{method: "POST", path: p + "/locales", cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"code": "de-DE"}}).
		want(t, http.StatusOK, "")
	r = s.do(call{method: "POST", path: p + "/locales", cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"code": "ar"}})
	r.decode(t, &loc)
	if loc.Direction != "rtl" {
		t.Errorf("ar direction = %s", loc.Direction)
	}
	s.do(call{method: "POST", path: p + "/locales", cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"code": "de-u-co-phonebk"}}).
		want(t, http.StatusBadRequest, "invalid_locale")
	var locales struct {
		Items []struct {
			Code     string `json:"code"`
			IsSource bool   `json:"is_source"`
		} `json:"items"`
	}
	s.do(call{method: "GET", path: p + "/locales", cookie: ada.cookie}).decode(t, &locales)
	if len(locales.Items) != 3 || locales.Items[2].Code != "en" || !locales.Items[2].IsSource {
		t.Errorf("locales = %+v", locales.Items)
	}

	// Fallback graph: first write without If-Match, then with it.
	r = s.do(call{method: "PUT", path: p + "/fallback-graph", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"fallback": map[string][]string{"*": {"en"}}}})
	r.want(t, http.StatusOK, "")
	s.do(call{method: "PUT", path: p + "/fallback-graph", cookie: ada.cookie, csrf: ada.csrf,
		body:    map[string]any{"fallback": map[string][]string{"de-DE": {"ar"}, "ar": {"de-DE"}}},
		headers: map[string]string{"If-Match": r.header.Get("ETag")}}).want(t, http.StatusBadRequest, "fallback_cycle")

	// The CLI pushes messages in ICU MF1 with a write token.
	var tok struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "cli", "scopes": []string{"write"}}}).decode(t, &tok)
	push := call{method: "POST", path: p + "/message-upserts", bearer: tok.Secret, body: map[string]any{"items": []map[string]any{
		{"key": "cart.items", "text": "{count, plural, one {# item} other {# items}}", "description": "Cart badge"},
		{"key": "checkout.pay", "text": "Pay {amount, number, ::currency/EUR}"},
		{"key": "Bad Key", "text": "x"},
	}}}
	var pushed struct {
		Results []struct {
			Key    string `json:"key"`
			Status string `json:"status"`
			Error  *struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"results"`
	}
	r = s.do(push)
	r.want(t, http.StatusOK, "")
	r.decode(t, &pushed)
	if pushed.Results[0].Status != "created" || pushed.Results[1].Status != "created" ||
		pushed.Results[2].Status != "failed" || pushed.Results[2].Error.Code != "invalid_message_key" {
		t.Fatalf("push = %s", r.body)
	}
	s.do(push).decode(t, &pushed)
	if pushed.Results[0].Status != "unchanged" || pushed.Results[1].Status != "unchanged" {
		t.Errorf("second push = %+v", pushed.Results)
	}

	r = s.do(call{method: "GET", path: p + "/messages/cart.items", cookie: ada.cookie})
	r.want(t, http.StatusOK, "")
	var msg message
	r.decode(t, &msg)
	if msg.Source.Syntax != "mf1" || msg.Source.Model["type"] != "select" || len(msg.Source.Arguments) != 1 ||
		msg.Source.Arguments[0].Type != "number" || msg.SourceRevision != 1 {
		t.Errorf("message = %s", r.body)
	}
	msgTag := r.header.Get("ETag")

	// Translate. Structural QA rejects a translation that drops an
	// argument, with the findings.
	tr := p + "/messages/checkout.pay/translations/de-DE"
	r = s.do(call{method: "PUT", path: tr, cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"text": "Jetzt bezahlen"}})
	r.want(t, http.StatusUnprocessableEntity, "structural_qa_failed")
	if !strings.Contains(string(r.body), `"missing-argument"`) {
		t.Errorf("findings = %s", r.body)
	}
	tr = p + "/messages/cart.items/translations/de-DE"
	r = s.do(call{method: "PUT", path: tr, cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"text": "{count, plural, one {# Artikel} other {# Artikel}}", "origin": "ai",
			"origin_detail": map[string]string{"model": "test-model", "prompt_version": "1"}}})
	r.want(t, http.StatusCreated, "")
	var de translation
	r.decode(t, &de)
	if de.State != "needs_review" || de.Origin != "ai" || de.Outdated || de.SourceRevision != 1 || r.header.Get("ETag") != `"1"` {
		t.Errorf("translation = %s", r.body)
	}
	s.do(call{method: "PUT", path: tr, cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"text": "{count, plural, one {# Stück} other {# Stück}}"}}).
		want(t, http.StatusPreconditionRequired, "precondition_required")
	s.do(call{method: "PUT", path: p + "/messages/cart.items/translations/en", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"text": "x"}}).want(t, http.StatusConflict, "source_locale")

	// Once a second reviewer exists, the author never approves their own
	// text, even as the owner (RFC 0006 Q6, amended 2026-10-08).
	s.do(call{method: "POST", path: base + "/members", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"email": "rita@example.com", "roles": []string{"reviewer"}, "locales": []string{"de"}}}).
		want(t, http.StatusCreated, "")
	rita := s.signIn("rita@example.com")
	s.do(call{method: "POST", path: tr + "/reviews", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"state": "approved"}, headers: map[string]string{"If-Match": `"1"`}}).
		want(t, http.StatusForbidden, "own_text")
	// The second reviewer approves.
	r = s.do(call{method: "POST", path: tr + "/reviews", cookie: rita.cookie, csrf: rita.csrf,
		body: map[string]string{"state": "approved"}, headers: map[string]string{"If-Match": `"1"`}})
	r.want(t, http.StatusOK, "")
	r.decode(t, &de)
	if de.State != "approved" || de.Revision != 2 {
		t.Errorf("reviewed = %s", r.body)
	}

	// Revise the source; the translation becomes outdated once
	// Localization has processed catalog.message.source_revised.
	s.do(call{method: "PUT", path: p + "/messages/cart.items/source", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"text": "{count, plural, one {# product} other {# products}}"}}).
		want(t, http.StatusPreconditionRequired, "precondition_required")
	r = s.do(call{method: "PUT", path: p + "/messages/cart.items/source", cookie: ada.cookie, csrf: ada.csrf,
		body:    map[string]string{"text": "{count, plural, one {# product} other {# products}}"},
		headers: map[string]string{"If-Match": msgTag}})
	r.want(t, http.StatusOK, "")
	r.decode(t, &msg)
	if msg.SourceRevision != 2 || msg.Key != "cart.items" {
		t.Fatalf("revised = %s", r.body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.do(call{method: "GET", path: tr, cookie: ada.cookie}).decode(t, &de)
		if de.Outdated || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !de.Outdated || de.SourceRevision != 1 || de.CurrentSourceRevision != 2 {
		t.Fatalf("after the source revision: %+v", de)
	}
	var outdated struct {
		Items []message `json:"items"`
	}
	s.do(call{method: "GET", path: p + "/messages?outdated_in=de_DE", cookie: ada.cookie}).decode(t, &outdated)
	if len(outdated.Items) != 1 || outdated.Items[0].Key != "cart.items" {
		t.Errorf("outdated_in = %+v", outdated.Items)
	}
	var missing struct {
		Items []message `json:"items"`
	}
	s.do(call{method: "GET", path: p + "/messages?missing_in=de-DE", cookie: ada.cookie}).decode(t, &missing)
	if len(missing.Items) != 1 || missing.Items[0].Key != "checkout.pay" {
		t.Errorf("missing_in = %+v", missing.Items)
	}
	var history struct {
		Items []struct {
			Kind   string         `json:"kind"`
			Origin string         `json:"origin"`
			Detail map[string]any `json:"origin_detail"`
		} `json:"items"`
	}
	s.do(call{method: "GET", path: tr + "/revisions", cookie: ada.cookie}).decode(t, &history)
	if len(history.Items) != 2 || history.Items[0].Kind != "review" || history.Items[1].Detail["model"] != "test-model" {
		t.Errorf("history = %+v", history.Items)
	}
	var sources struct {
		Items []struct {
			Revision int `json:"revision"`
		} `json:"items"`
	}
	s.do(call{method: "GET", path: p + "/messages/cart.items/source-revisions", cookie: ada.cookie}).decode(t, &sources)
	if len(sources.Items) != 2 || sources.Items[0].Revision != 2 {
		t.Errorf("source log = %+v", sources.Items)
	}
}

// Translators write only in their locales; people outside the tenant
// see nothing.
func TestTranslatorScopeOverHTTP(t *testing.T) {
	s := startServer(t)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	var project struct{ ID string }
	s.do(call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"slug": "shop", "name": "Shop", "source_locale": "en",
			"settings": map[string]any{"default_syntax": "mf1", "review_required": false}}}).decode(t, &project)
	p := base + "/projects/" + project.ID
	for _, l := range []string{"de", "fr"} {
		s.do(call{method: "POST", path: p + "/locales", cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"code": l}}).
			want(t, http.StatusCreated, "")
	}
	s.do(call{method: "POST", path: p + "/messages", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"key": "home.title", "text": "Welcome"}}).want(t, http.StatusCreated, "")
	s.do(call{method: "POST", path: base + "/members", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"email": "bob@example.com", "roles": []string{"translator"}, "locales": []string{"fr"}}}).
		want(t, http.StatusCreated, "")
	bob := s.signIn("bob@example.com") // accepts the invitation

	r := s.do(call{method: "PUT", path: p + "/messages/home.title/translations/fr", cookie: bob.cookie, csrf: bob.csrf,
		body: map[string]string{"text": "Bienvenue"}})
	r.want(t, http.StatusCreated, "")
	var fr translation
	r.decode(t, &fr)
	if fr.State != "approved" {
		t.Errorf("state without required review = %s", fr.State)
	}
	s.do(call{method: "PUT", path: p + "/messages/home.title/translations/de", cookie: bob.cookie, csrf: bob.csrf,
		body: map[string]string{"text": "Willkommen"}}).want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "POST", path: p + "/messages", cookie: bob.cookie, csrf: bob.csrf,
		body: map[string]string{"key": "x", "text": "x"}}).want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "GET", path: p + "/messages/home.title", cookie: bob.cookie}).want(t, http.StatusOK, "")

	carol := s.signIn("carol@example.com")
	s.do(call{method: "GET", path: p, cookie: carol.cookie}).want(t, http.StatusForbidden, "forbidden")
	var me struct {
		Person struct {
			IndividualTenantID string `json:"individual_tenant_id"`
		} `json:"person"`
	}
	s.do(call{method: "GET", path: "/v1/me", cookie: carol.cookie}).decode(t, &me)
	// In her own tenant, Acme's project doesn't exist.
	s.do(call{method: "GET", path: "/v1/tenants/" + me.Person.IndividualTenantID + "/projects/" + project.ID, cookie: carol.cookie}).
		want(t, http.StatusNotFound, "not_found")
	s.do(call{method: "GET", path: "/v1/tenants/" + me.Person.IndividualTenantID + "/projects/" + project.ID + "/locales", cookie: carol.cookie}).
		want(t, http.StatusNotFound, "not_found")
}
