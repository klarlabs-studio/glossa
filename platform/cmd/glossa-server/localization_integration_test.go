//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// translatedProject is a project over HTTP: source en, locales de and
// fr, four messages pushed with a write token, and translations — de
// for cart.items (then outdated by a source revision) and checkout.pay
// (draft), fr for checkout.pay.
type translatedProject struct {
	s     *server
	ada   session
	path  string // /v1/tenants/{tenant}/projects/{project}
	token string // a read token
}

func newTranslatedProject(t *testing.T) translatedProject {
	t.Helper()
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
	var tok struct {
		Secret string `json:"secret"`
	}
	s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"name": "ci", "scopes": []string{"read"}}}).decode(t, &tok)
	s.do(call{method: "POST", path: p + "/message-upserts", cookie: ada.cookie, csrf: ada.csrf, body: map[string]any{"items": []map[string]any{
		{"key": "cart.items", "text": "{count, plural, one {# item} other {# items}}"},
		{"key": "checkout.pay", "text": "Pay {amount, number}"},
		{"key": "checkout.total", "text": "Total"},
	}}}).want(t, http.StatusOK, "")
	put := func(key, locale, text string, state string) {
		body := map[string]string{"text": text}
		if state != "" {
			body["state"] = state
		}
		s.do(call{method: "PUT", path: p + "/messages/" + key + "/translations/" + locale, cookie: ada.cookie, csrf: ada.csrf,
			body: body}).want(t, http.StatusCreated, "")
	}
	put("cart.items", "de", "{count, plural, one {# Artikel} other {# Artikel}}", "")
	put("checkout.pay", "de", "{amount, number} zahlen", "draft")
	put("checkout.pay", "fr", "Payer {amount, number}", "")
	r := s.do(call{method: "GET", path: p + "/messages/cart.items", cookie: ada.cookie})
	s.do(call{method: "PUT", path: p + "/messages/cart.items/source", cookie: ada.cookie, csrf: ada.csrf,
		body:    map[string]string{"text": "{count, plural, one {# product} other {# products}}"},
		headers: map[string]string{"If-Match": r.header.Get("ETag")}}).want(t, http.StatusOK, "")
	// Localization learns of the revision from the outbox.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var tr struct {
			Outdated bool `json:"outdated"`
		}
		s.do(call{method: "GET", path: p + "/messages/cart.items/translations/de", cookie: ada.cookie}).decode(t, &tr)
		if tr.Outdated {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cart.items/de never became outdated")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return translatedProject{s: s, ada: ada, path: p, token: tok.Secret}
}

type projectTranslation struct {
	Key            string `json:"key"`
	Namespace      string `json:"namespace"`
	MessageState   string `json:"message_state"`
	Locale         string `json:"locale"`
	Text           string `json:"text"`
	State          string `json:"state"`
	SourceRevision int    `json:"source_revision"`
	Outdated       bool   `json:"outdated"`
}

// The CLI reads every translation of a project a page at a time instead
// of one request per message.
func TestProjectTranslationsOverHTTP(t *testing.T) {
	tp := newTranslatedProject(t)
	s := tp.s
	var page struct {
		Items         []projectTranslation `json:"items"`
		NextPageToken *string              `json:"next_page_token"`
	}
	var got []string
	url := tp.path + "/translations?locale=de&locale=fr&page_size=2"
	for i := 0; ; i++ {
		page.NextPageToken = nil
		r := s.do(call{method: "GET", path: url, bearer: tp.token})
		r.want(t, http.StatusOK, "")
		r.decode(t, &page)
		for _, it := range page.Items {
			got = append(got, it.Key+"/"+it.Locale)
		}
		if page.NextPageToken == nil || i > 5 {
			break
		}
		url = tp.path + "/translations?locale=de&locale=fr&page_size=2&page_token=" + *page.NextPageToken
	}
	if strings.Join(got, " ") != "cart.items/de checkout.pay/de checkout.pay/fr" {
		t.Errorf("all = %v", got)
	}

	s.do(call{method: "GET", path: tp.path + "/translations?locale=de&outdated=true", cookie: tp.ada.cookie}).decode(t, &page)
	if len(page.Items) != 1 {
		t.Fatalf("outdated = %+v", page.Items)
	}
	if it := page.Items[0]; it.Key != "cart.items" || it.Namespace != "default" || it.MessageState != "active" ||
		!it.Outdated || it.SourceRevision != 1 || it.Text == "" {
		t.Errorf("outdated item = %+v", it)
	}
	s.do(call{method: "GET", path: tp.path + "/translations?locale=de&locale=fr&state=draft&key_prefix=checkout.", cookie: tp.ada.cookie}).
		decode(t, &page)
	if len(page.Items) != 1 || page.Items[0].Key != "checkout.pay" || page.Items[0].Locale != "de" || page.Items[0].State != "draft" {
		t.Errorf("draft checkout. = %+v", page.Items)
	}

	s.do(call{method: "GET", path: tp.path + "/translations", bearer: tp.token}).want(t, http.StatusBadRequest, "invalid_request")
	s.do(call{method: "GET", path: tp.path + "/translations?locale=de&state=done", bearer: tp.token}).
		want(t, http.StatusBadRequest, "invalid_state")
	s.do(call{method: "GET", path: tp.path + "/translations?locale=de&message_state=gone", bearer: tp.token}).
		want(t, http.StatusBadRequest, "invalid_message_state")
	s.do(call{method: "GET", path: tp.path + "/translations?locale=not+a+locale", bearer: tp.token}).
		want(t, http.StatusBadRequest, "invalid_locale")
	s.do(call{method: "GET", path: tp.path + "/translations?locale=de" + strings.Repeat("&locale=fr", 20), bearer: tp.token}).
		want(t, http.StatusBadRequest, "too_many_locales")
	s.do(call{method: "GET", path: tp.path + "/translations?locale=de"}).want(t, http.StatusUnauthorized, "unauthenticated")
	carol := s.signIn("carol@example.com")
	s.do(call{method: "GET", path: tp.path + "/translations?locale=de", cookie: carol.cookie}).want(t, http.StatusForbidden, "forbidden")
}

// Studio's badges and the CLI's status read every locale's counts in
// one request.
func TestTranslationStatsOverHTTP(t *testing.T) {
	tp := newTranslatedProject(t)
	s := tp.s
	type counts struct {
		Code       string         `json:"code"`
		Direction  string         `json:"direction"`
		IsSource   bool           `json:"is_source"`
		Translated int            `json:"translated"`
		Missing    int            `json:"missing"`
		Outdated   int            `json:"outdated"`
		States     map[string]int `json:"states"`
	}
	var stats struct {
		Messages int      `json:"messages"`
		Locales  []counts `json:"locales"`
	}
	r := s.do(call{method: "GET", path: tp.path + "/translation-stats", bearer: tp.token})
	r.want(t, http.StatusOK, "")
	r.decode(t, &stats)
	if stats.Messages != 3 || len(stats.Locales) != 3 {
		t.Fatalf("stats = %s", r.body)
	}
	de, en, fr := stats.Locales[0], stats.Locales[1], stats.Locales[2]
	if de.Code != "de" || de.Translated != 2 || de.Missing != 1 || de.Outdated != 1 ||
		de.States["draft"] != 1 || de.States["approved"] != 1 || de.States["needs_review"] != 0 || de.States["rejected"] != 0 {
		t.Errorf("de = %+v", de)
	}
	if en.Code != "en" || !en.IsSource || en.Direction != "ltr" || en.Translated != 3 || en.Missing != 0 || en.States["approved"] != 3 {
		t.Errorf("en = %+v", en)
	}
	if fr.Code != "fr" || fr.Translated != 1 || fr.Missing != 2 || fr.Outdated != 0 {
		t.Errorf("fr = %+v", fr)
	}
	s.do(call{method: "GET", path: tp.path + "/translation-stats"}).want(t, http.StatusUnauthorized, "unauthenticated")
	carol := s.signIn("carol@example.com")
	s.do(call{method: "GET", path: tp.path + "/translation-stats", cookie: carol.cookie}).want(t, http.StatusForbidden, "forbidden")
	s.do(call{method: "GET", path: strings.Replace(tp.path, "/projects/", "/projects/0", 1) + "/translation-stats", bearer: tp.token}).
		want(t, http.StatusNotFound, "not_found")
}

// `agent` is a first-class origin on the wire (RFC 0005 §7.3): it goes
// in through the contract, comes back out of the translation and stands
// in the revision log as itself, not as `ai` with a JSON hint. The enum
// grew by exactly one value, so anything else is still refused.
func TestAgentOriginOverHTTP(t *testing.T) {
	tp := newTranslatedProject(t)
	s := tp.s
	path := tp.path + "/messages/checkout.total/translations/de"
	s.do(call{method: "PUT", path: path, cookie: tp.ada.cookie, csrf: tp.ada.csrf,
		body: map[string]any{"text": "Gesamt", "state": "needs_review", "origin": "agent",
			"origin_detail": map[string]any{"via": "mcp", "tool": "translation_propose"}}}).
		want(t, http.StatusCreated, "")

	var tr struct {
		Origin string `json:"origin"`
		State  string `json:"state"`
	}
	r := s.do(call{method: "GET", path: path, cookie: tp.ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &tr)
	if tr.Origin != "agent" || tr.State != "needs_review" {
		t.Errorf("translation = %+v, want an agent's proposal", tr)
	}

	var log struct {
		Items []struct {
			Origin       string         `json:"origin"`
			OriginDetail map[string]any `json:"origin_detail"`
		} `json:"items"`
	}
	r = s.do(call{method: "GET", path: path + "/revisions", cookie: tp.ada.cookie})
	r.want(t, http.StatusOK, "")
	r.decode(t, &log)
	if len(log.Items) != 1 || log.Items[0].Origin != "agent" {
		t.Fatalf("revision log = %s", r.body)
	}
	if log.Items[0].OriginDetail["via"] != "mcp" || log.Items[0].OriginDetail["tool"] != "translation_propose" {
		t.Errorf("origin_detail = %v: it still says which surface and tool", log.Items[0].OriginDetail)
	}

	s.do(call{method: "PUT", path: tp.path + "/messages/checkout.total/translations/fr",
		cookie: tp.ada.cookie, csrf: tp.ada.csrf, body: map[string]any{"text": "Total", "origin": "robot"}}).
		want(t, http.StatusBadRequest, "invalid_origin")
}
