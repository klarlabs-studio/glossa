package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// testToken is a well-formed API token.
const testToken = "glossa_api_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// fakeServer is an in-memory glossa-server with the /v1 operations the
// CLI uses, faithful to the contract's shapes and item semantics.
type fakeServer struct {
	t   *testing.T
	srv *httptest.Server

	mu             sync.Mutex
	reviewRequired bool
	// checkPolicy is the project's stored check policy, as the contract
	// renders it; nil leaves settings.check_policy out of the response,
	// the way a server that predates the setting would.
	checkPolicy map[string]any
	// policyDoc is what GET …/check-policy answers. nil leaves the
	// route 404, the way a server that predates the endpoint would, so
	// a test that does not set one exercises the settings fallback.
	policyDoc    map[string]any
	sourceLocale string
	locales      []string // including the source
	messages     map[string]*fakeMessage
	translations map[string]map[string]*fakeTranslation // locale → key
	rel          *fakeReleases
	kn           *fakeKnowledge
	io           *fakeInterchange
	ctx          *fakeContext
	branches     *fakeBranches
	qa           *fakeQuality
	gh           *fakeGitHub
	ci           *fakeCI
	dev          *fakeDevice
	members      *fakeMembers
	wf           *fakeWorkflows
	audit        *fakeAuditImports
	trail        *fakeAuditTrail
	appr         *fakeApprovals
	rv           *fakeReviews
	requests     []string
}

// accepts reports whether an Authorization header names a credential
// this server knows: the API token, or a CI token it minted.
func (f *fakeServer) accepts(header string) bool {
	if header == "Bearer "+testToken || personOf(header) != "" {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return (f.ci.minted != "" && header == "Bearer "+f.ci.minted) || f.dev.accepts(header)
}

type fakeMessage struct {
	key      string
	content  mfcontent.Content
	revision int
	state    string
	// namespace is the message's bundle; empty is the default one, the
	// way the contract renders a message nobody moved.
	namespace string
	// description is the last one an upsert sent (omitted: kept).
	description string
}

// ns is the namespace the server reports for the message.
func (m *fakeMessage) ns() string {
	if m.namespace == "" {
		return "default"
	}
	return m.namespace
}

type fakeTranslation struct {
	content        mfcontent.Content
	state          string
	origin         string
	sourceRevision int
	revision       int
	// originDetail is the origin_detail of the latest import write.
	originDetail map[string]any
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{t: t, reviewRequired: true, sourceLocale: "en", locales: []string{"en"},
		messages: map[string]*fakeMessage{}, translations: map[string]map[string]*fakeTranslation{}, rel: newFakeReleases(), kn: newFakeKnowledge(), io: newFakeInterchange(), ctx: newFakeContext(), branches: newFakeBranches(), gh: newFakeGitHub(), ci: &fakeCI{}, dev: &fakeDevice{}, qa: newFakeQuality()}
	mux := http.NewServeMux()
	p := "/v1/tenants/ten_1/projects/prj_1"
	mux.HandleFunc("GET /v1/tenants", f.tenants)
	mux.HandleFunc("GET /v1/tenants/ten_1/projects", f.projects)
	mux.HandleFunc("GET "+p+"/check-policy", f.getCheckPolicy)
	mux.HandleFunc("GET "+p+"/locales", f.listLocales)
	mux.HandleFunc("POST "+p+"/locales", f.addLocale)
	mux.HandleFunc("GET "+p+"/fallback-graph", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONResp(w, 200, map[string]any{"fallback": map[string][]string{"*": {"en"}}})
	})
	mux.HandleFunc("GET "+p+"/messages", f.listMessages)
	mux.HandleFunc("GET "+p+"/namespaces", f.listNamespaces)
	mux.HandleFunc("POST "+p+"/message-upserts", f.upsert)
	mux.HandleFunc("GET "+p+"/messages/{key}/translations", f.listTranslations)
	mux.HandleFunc("POST "+p+"/translation-imports", f.importTranslations)
	mux.HandleFunc("GET "+p+"/translations", f.listProjectTranslations)
	mux.HandleFunc("GET "+p+"/translation-stats", f.translationStats)
	f.routeReleases(mux, p)
	f.routeKnowledge(mux)
	f.routeInterchange(mux)
	f.routeContext(mux, p)
	f.routeBranches(mux, p)
	f.routeQuality(mux, p)
	f.routeGitHub(mux)
	f.routeCI(mux)
	f.routeDevice(mux)
	f.routeMembers(mux)
	f.routeWorkflows(mux)
	f.routeAuditImports(mux, p)
	f.routeAuditTrail(mux)
	f.routeApprovals(mux, p)
	f.routeReviews(mux, p)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path+" "+r.Header.Get("Idempotency-Key"))
		f.mu.Unlock()
		// The OIDC exchange is the one unauthenticated operation: the
		// ID token in the body is the credential (RFC 0004 §6.3).
		if r.URL.Path == "/v1/auth/github-oidc-exchanges" || r.URL.Path == "/v1/auth/device-authorizations" || r.URL.Path == "/v1/auth/device-sessions" {
			mux.ServeHTTP(w, r)
			return
		}
		if !f.accepts(r.Header.Get("Authorization")) {
			problemResp(w, 401, "unauthenticated", "invalid token")
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) URL() string { return f.srv.URL }

// close stops the server, so a command pointed at it gets a refused
// connection on a local port rather than a DNS lookup. It is how a test
// says "the server is out of reach"; the cleanup closing it twice is
// harmless.
func (f *fakeServer) close() { f.srv.Close() }

func writeJSONResp(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func problemResp(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "urn:glossa:problem:" + code, "code": code, "title": code, "status": status, "detail": detail})
}

func (f *fakeServer) tenants(w http.ResponseWriter, _ *http.Request) {
	writeJSONResp(w, 200, map[string]any{"items": []map[string]any{{"id": "ten_1", "kind": "organization", "slug": "acme", "name": "Acme", "created_at": "2026-09-19T00:00:00Z"}}})
}

func (f *fakeServer) projects(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	settings := map[string]any{"default_syntax": "mf1", "review_required": f.reviewRequired}
	if f.checkPolicy != nil {
		settings["check_policy"] = f.checkPolicy
	}
	writeJSONResp(w, 200, map[string]any{"items": []map[string]any{{"id": "prj_1", "slug": "shop", "name": "Shop",
		"source_locale": f.sourceLocale, "settings": settings,
		"created_at": "2026-09-19T00:00:00Z", "updated_at": "2026-09-19T00:00:00Z"}}})
}

func (f *fakeServer) localeJSON(code string) map[string]any {
	return map[string]any{"code": code, "direction": string(bcp47.MustParse(code).Direction()), "is_source": code == f.sourceLocale, "created_at": "2026-09-19T00:00:00Z"}
}

func (f *fakeServer) listLocales(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var items []map[string]any
	for _, l := range f.locales {
		items = append(items, f.localeJSON(l))
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) addLocale(w http.ResponseWriter, r *http.Request) {
	var body struct{ Code string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	tag, err := bcp47.Parse(body.Code)
	if err != nil {
		problemResp(w, 400, "invalid_locale", err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.locales {
		if l == tag.String() {
			writeJSONResp(w, 200, f.localeJSON(l))
			return
		}
	}
	f.locales = append(f.locales, tag.String())
	sort.Strings(f.locales)
	writeJSONResp(w, 201, f.localeJSON(tag.String()))
}

func (f *fakeServer) messageJSON(m *fakeMessage) map[string]any {
	var model, args any
	_ = json.Unmarshal(m.content.ModelJSON(), &model)
	_ = json.Unmarshal(m.content.ArgumentsJSON(), &args)
	return map[string]any{"id": "msg_" + m.key, "key": m.key, "namespace": m.ns(), "description": m.description, "state": m.state,
		"source":          map[string]any{"text": m.content.Text, "syntax": string(m.content.Syntax), "model": model, "arguments": args, "markup": []any{}},
		"source_revision": m.revision, "created_at": "2026-09-19T00:00:00Z", "updated_at": "2026-09-19T00:00:00Z"}
}

func (f *fakeServer) listMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.messages))
	for k := range f.messages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	items := []map[string]any{}
	for _, k := range keys {
		m := f.messages[k]
		if s := q.Get("state"); s != "" && m.state != s {
			continue
		}
		if p := q.Get("key_prefix"); p != "" && !strings.HasPrefix(k, p) {
			continue
		}
		if l := q.Get("missing_in"); l != "" && f.translations[l][k] != nil {
			continue
		}
		if l := q.Get("outdated_in"); l != "" && (f.translations[l][k] == nil || f.translations[l][k].sourceRevision >= m.revision) {
			continue
		}
		items = append(items, f.messageJSON(m))
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

// listNamespaces counts the messages by state; the fake's messages are
// all in the default namespace.
func (f *fakeServer) listNamespaces(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := []map[string]any{}
	if len(f.messages) > 0 {
		active, obsolete := 0, 0
		for _, m := range f.messages {
			if m.state == "obsolete" {
				obsolete++
			} else {
				active++
			}
		}
		items = append(items, map[string]any{"name": "default", "active_messages": active, "obsolete_messages": obsolete})
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) upsert(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []struct {
			Key, Text   string
			Syntax      *string
			Description *string
		}
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	var results []map[string]any
	for _, it := range body.Items {
		c, err := mfcontent.Parse(mfcontent.MF1, it.Text, bcp47.MustParse(f.sourceLocale))
		if err != nil {
			results = append(results, map[string]any{"key": it.Key, "status": "failed", "error": map[string]any{"code": "invalid_message", "detail": err.Error()}})
			continue
		}
		m, ok := f.messages[it.Key]
		status := "unchanged"
		switch {
		case !ok:
			m = &fakeMessage{key: it.Key, content: c, revision: 1, state: "active"}
			f.messages[it.Key] = m
			status = "created"
		case !m.content.SameModel(c):
			m.content, m.revision, m.state, status = c, m.revision+1, "active", "revised"
		case m.state != "active":
			m.state, status = "active", "updated"
		}
		if it.Description != nil {
			m.description = *it.Description
		}
		results = append(results, map[string]any{"key": it.Key, "status": status, "message": f.messageJSON(m)})
	}
	writeJSONResp(w, 200, map[string]any{"results": results})
}

func (f *fakeServer) translationJSON(key, locale string, t *fakeTranslation) map[string]any {
	var model any
	_ = json.Unmarshal(t.content.ModelJSON(), &model)
	cur := f.messages[key].revision
	return map[string]any{"id": "tr_" + key + "_" + locale, "message_id": "msg_" + key, "locale": locale, "text": t.content.Text,
		"syntax": "mf1", "model": model, "state": t.state, "origin": t.origin, "author": "token:1",
		"source_revision": t.sourceRevision, "current_source_revision": cur, "outdated": t.sourceRevision < cur,
		"warnings": []any{}, "revision": t.revision, "created_at": "2026-09-19T00:00:00Z", "updated_at": time.Now().UTC().Format(time.RFC3339)}
}

func (f *fakeServer) listTranslations(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.messages[key] == nil {
		problemResp(w, 404, "not_found", "no message")
		return
	}
	items := []map[string]any{}
	for _, l := range f.locales {
		if t := f.translations[l][key]; t != nil {
			items = append(items, f.translationJSON(key, l, t))
		}
	}
	writeJSONResp(w, 200, map[string]any{"items": items})
}

func (f *fakeServer) importTranslations(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []struct {
			Key, Locale, Text string
			State             *string
			OriginDetail      map[string]any `json:"origin_detail"`
		}
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	var results []map[string]any
	fail := func(key, locale, code string) {
		results = append(results, map[string]any{"key": key, "locale": locale, "error": map[string]any{"code": code, "detail": code}})
	}
	for _, it := range body.Items {
		m := f.messages[it.Key]
		tag, err := bcp47.Parse(it.Locale)
		switch {
		case m == nil:
			fail(it.Key, it.Locale, "message_not_found")
			continue
		case err != nil || !f.hasLocale(tag.String()):
			fail(it.Key, it.Locale, "locale_not_found")
			continue
		case it.State != nil && *it.State == "approved" && f.reviewRequired:
			fail(it.Key, it.Locale, "review_forbidden")
			continue
		}
		c, err := mfcontent.Parse(mfcontent.MF1, it.Text, tag)
		if err != nil {
			fail(it.Key, it.Locale, "invalid_message")
			continue
		}
		if hasErrors(mf.CheckCompat(m.content.Model, c.Model, tag.String())) {
			fail(it.Key, it.Locale, "structural_qa_failed")
			continue
		}
		state := "approved"
		if f.reviewRequired {
			state = "needs_review"
		}
		if it.State != nil {
			state = *it.State
		}
		if f.translations[tag.String()] == nil {
			f.translations[tag.String()] = map[string]*fakeTranslation{}
		}
		t := f.translations[tag.String()][it.Key]
		status := "unchanged"
		switch {
		case t == nil:
			t = &fakeTranslation{content: c, state: state, origin: "import", sourceRevision: m.revision, revision: 1}
			f.translations[tag.String()][it.Key] = t
			status = "created"
		case !t.content.SameModel(c) || t.sourceRevision != m.revision:
			t.content, t.state, t.sourceRevision, t.revision = c, state, m.revision, t.revision+1
			status = "revised"
		case it.State != nil && *it.State != t.state:
			t.state, t.revision, status = *it.State, t.revision+1, "reviewed"
		}
		if status != "unchanged" {
			t.originDetail = it.OriginDetail
		}
		results = append(results, map[string]any{"key": it.Key, "locale": tag.String(), "status": status, "translation": f.translationJSON(it.Key, tag.String(), t)})
	}
	writeJSONResp(w, 200, map[string]any{"results": results})
}

func (f *fakeServer) hasLocale(code string) bool {
	for _, l := range f.locales {
		if l == code {
			return true
		}
	}
	return false
}

func hasErrors(fs []mf.Finding) bool {
	for _, f := range fs {
		if f.Severity == mf.SeverityError {
			return true
		}
	}
	return false
}

// countRequests counts requests whose "METHOD path" starts with prefix.
func (f *fakeServer) countRequests(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// getCheckPolicy answers the project's check-policy document, or 404
// when the test set none — which is what an older server does, and what
// sends `glossa check` to settings.check_policy instead.
func (f *fakeServer) getCheckPolicy(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	doc := f.policyDoc
	f.mu.Unlock()
	if doc == nil {
		problemResp(w, http.StatusNotFound, "not_found", "no check policy endpoint")
		return
	}
	writeJSONResp(w, http.StatusOK, doc)
}
