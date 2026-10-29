//go:build integration

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
)

// startServerCovered boots the real composition root exactly as
// startServer does — migrations, generated router, Guard, every
// context — with cov wired as the authz.Coverage assignment-scoped
// visibility filters through (RFC 0006 §3.3). Workflow's assignments
// implement it in production; nothing in this build does yet, so the
// test brings its own, through build's own seam.
func startServerCovered(t *testing.T, cov authz.Coverage) *server {
	t.Helper()
	env, err := dbtest.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
	addr := freeAddr(t)
	logs := &syncBuffer{}
	lookup := lookupFrom(map[string]string{
		"DATABASE_URL":                env.AppDSN,
		"MIGRATION_DATABASE_URL":      env.OwnerDSN,
		"GLOSSA_MIGRATE":              "up",
		"GLOSSA_HTTP_ADDR":            addr,
		"GLOSSA_AUTH_SECRET":          testAuthSecret,
		"GLOSSA_MAIL_DRIVER":          "log",
		"GLOSSA_STUDIO_URL":           "https://studio.test",
		"GLOSSA_STORAGE_DIR":          t.TempDir(),
		"GLOSSA_OUTBOX_POLL_INTERVAL": "50ms",
	})
	cfg, err := config.Load(lookup)
	if err != nil {
		t.Fatal(err)
	}
	logger := observability.NewLogger(logs, cfg.LogLevel, tenantAttrs)
	ctx, cancel := context.WithCancel(context.Background())
	if err := migrate(ctx, cfg, logger); err != nil {
		cancel()
		t.Fatal(err)
	}
	a, err := build(ctx, cfg, logger, lookup, func(b *contexts) { b.coverage = cov })
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		defer a.pool.Close()
		done <- a.run(ctx)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("server did not stop")
			}
		})
	}
	t.Cleanup(stop)
	waitReady(t, "http://"+addr+"/readyz", done)
	return &server{t: t, base: "http://" + addr, logs: logs, db: env, stop: stop}
}

// fillPath turns an operation's route pattern into a request path:
// {tenant} and {project} from fixed, every other path parameter — and
// every required query parameter — a value of the type the contract
// declares, so the generated router binds it and the request reaches
// the Guard and the use case.
func fillPath(t *testing.T, pattern string, fixed map[string]string) string {
	t.Helper()
	doc, err := apiv1.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	method, path, _ := strings.Cut(pattern, " ")
	item := doc.Paths.Find(path)
	if item == nil {
		t.Fatalf("%s is not in the contract", pattern)
	}
	op := item.GetOperation(method)
	params := append(slices.Clone(item.Parameters), op.Parameters...)
	query := url.Values{}
	value := func(name, format, typ string) string {
		switch {
		case fixed[name] != "":
			return fixed[name]
		case format == "uuid":
			return uuid.NewString()
		case typ == "integer":
			return "1"
		case name == "locale":
			return "de"
		case name == "digest":
			return strings.Repeat("a", 64)
		}
		return "x"
	}
	for _, p := range params {
		if p.Value == nil {
			continue
		}
		format, typ := "", ""
		if s := p.Value.Schema; s != nil && s.Value != nil {
			format = s.Value.Format
			if s.Value.Type != nil && len(*s.Value.Type) > 0 {
				typ = (*s.Value.Type)[0]
			}
			if s.Value.Items != nil && s.Value.Items.Value != nil {
				format = s.Value.Items.Value.Format
			}
		}
		switch p.Value.In {
		case "path":
			path = strings.ReplaceAll(path, "{"+p.Value.Name+"}", url.PathEscape(value(p.Value.Name, format, typ)))
		case "query":
			if p.Value.Required {
				query.Set(p.Value.Name, value(p.Value.Name, format, typ))
			}
		}
	}
	for name, v := range fixed {
		path = strings.ReplaceAll(path, "{"+name+"}", v)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return path
}

// restrictedFixture is a tenant with two projects, a project-scoped
// read token and a vendor member whose one assignment covers one unit.
type restrictedFixture struct {
	s                    *server
	ada                  session
	base, a, b           string // /v1/tenants/{t}, …/projects/{a}, …/projects/{b}
	tenant, aID, bID     string
	scopedToken, ownerRO string
	vera                 session
}

func newRestrictedFixture(t *testing.T) restrictedFixture {
	t.Helper()
	cov := &authztest.Coverage{}
	s := startServerCovered(t, cov)
	ada := s.signIn("ada@example.com")
	var org struct{ ID string }
	s.do(call{method: "POST", path: "/v1/tenants", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]string{"slug": "acme", "name": "Acme"}}).decode(t, &org)
	base := "/v1/tenants/" + org.ID
	project := func(slug string, locales ...string) (string, string) {
		var p struct{ ID string }
		r := s.do(call{method: "POST", path: base + "/projects", cookie: ada.cookie, csrf: ada.csrf,
			body: map[string]any{"slug": slug, "name": slug, "source_locale": "en",
				"settings": map[string]any{"default_syntax": "mf1", "review_required": false}}})
		r.want(t, http.StatusCreated, "")
		r.decode(t, &p)
		path := base + "/projects/" + p.ID
		for _, l := range locales {
			s.do(call{method: "POST", path: path + "/locales", cookie: ada.cookie, csrf: ada.csrf, body: map[string]string{"code": l}}).
				want(t, http.StatusCreated, "")
		}
		return p.ID, path
	}
	aID, a := project("shop", "de", "fr")
	bID, b := project("other", "de")
	push := func(p string, keys ...string) {
		var items []map[string]any
		for _, k := range keys {
			items = append(items, map[string]any{"key": k, "text": "Text of " + k})
		}
		s.do(call{method: "POST", path: p + "/message-upserts", cookie: ada.cookie, csrf: ada.csrf,
			body: map[string]any{"items": items}}).want(t, http.StatusOK, "")
	}
	push(a, "pay", "cancel")
	push(b, "elsewhere")
	put := func(p, key, locale string) {
		s.do(call{method: "PUT", path: p + "/messages/" + key + "/translations/" + locale, cookie: ada.cookie, csrf: ada.csrf,
			body: map[string]string{"text": key + " " + locale}}).want(t, http.StatusCreated, "")
	}
	put(a, "pay", "de")
	put(a, "pay", "fr")
	put(a, "cancel", "de")
	put(b, "elsewhere", "de")
	for _, p := range []string{aID, bID} {
		s.do(call{method: "POST", path: base + "/export-jobs", cookie: ada.cookie, csrf: ada.csrf,
			body: map[string]any{"project_id": p, "format": "json"}}).want(t, http.StatusCreated, "")
	}
	var pay struct{ ID string }
	s.do(call{method: "GET", path: a + "/messages/pay", cookie: ada.cookie}).decode(t, &pay)

	token := func(name string, projects ...string) string {
		var tok struct {
			Secret string `json:"secret"`
		}
		r := s.do(call{method: "POST", path: base + "/tokens", cookie: ada.cookie, csrf: ada.csrf,
			body: map[string]any{"name": name, "scopes": []string{"read"}, "projects": projects}})
		r.want(t, http.StatusCreated, "")
		r.decode(t, &tok)
		return tok.Secret
	}
	// The restriction is set through the API that sets it (RFC 0006 §13
	// wave 3): a token's project scope, a member's visibility.
	scoped, ro := token("scoped", aID), token("everything")
	var member struct{ ID string }
	r := s.do(call{method: "POST", path: base + "/members", cookie: ada.cookie, csrf: ada.csrf,
		body: map[string]any{"email": "vera@lingo.example", "roles": []string{"translator"}, "locales": []string{"de", "fr"},
			"visibility": "assigned"}})
	r.want(t, http.StatusCreated, "")
	r.decode(t, &member)
	vera := s.signIn("vera@lingo.example")
	cov.Assign(identity.MemberID(uuid.MustParse(member.ID)), uuid.MustParse(aID), uuid.MustParse(pay.ID), "de")
	return restrictedFixture{s: s, ada: ada, base: base, a: a, b: b, tenant: org.ID, aID: aID, bID: bID,
		scopedToken: scoped, ownerRO: ro, vera: vera}
}

// TestRestrictionsOverHTTP holds the real server — every context behind
// the generated router and the Guard — to RFC 0006 §3.3 and §4.1, with
// the operation list generated from the contract and the expectations
// read from the decisions in restrictions_test.go.
func TestRestrictionsOverHTTP(t *testing.T) {
	f := newRestrictedFixture(t)
	s := f.s
	ops, err := apiv1.Requirements()
	if err != nil {
		t.Fatal(err)
	}
	var gets []string
	for pattern := range ops {
		if strings.HasPrefix(pattern, "GET /v1/tenants/{tenant}") {
			gets = append(gets, pattern)
		}
	}
	sort.Strings(gets)

	// Project scope: for every GET operation addressed to a project, a
	// project outside the token's scope answers exactly as a project
	// that does not exist — same status, same body.
	t.Run("project scope: every project-addressed read", func(t *testing.T) {
		n := 0
		for _, pattern := range gets {
			if restrictions[pattern].project != projectPath {
				continue
			}
			n++
			outside := s.do(call{method: "GET", path: fillPath(t, pattern, map[string]string{"tenant": f.tenant, "project": f.bID}), bearer: f.scopedToken})
			missing := s.do(call{method: "GET", path: fillPath(t, pattern, map[string]string{"tenant": f.tenant, "project": uuid.NewString()}), bearer: f.scopedToken})
			if outside.status != http.StatusNotFound || outside.problem.Code != "not_found" {
				t.Errorf("%s out of scope = %d %q (%s), want 404 not_found", pattern, outside.status, outside.problem.Code, outside.body)
			}
			if outside.status != missing.status || !bytes.Equal(outside.body, missing.body) {
				t.Errorf("%s tells a project out of scope (%d %s) from one that does not exist (%d %s)",
					pattern, outside.status, outside.body, missing.status, missing.body)
			}
		}
		if n < 40 {
			t.Errorf("only %d project-addressed reads swept; the generated list is broken", n)
		}
	})
	t.Run("project scope: in scope, and tenant-level lists", func(t *testing.T) {
		s.do(call{method: "GET", path: f.a, bearer: f.scopedToken}).want(t, http.StatusOK, "")
		s.do(call{method: "GET", path: f.a + "/messages/pay/translations/de", bearer: f.scopedToken}).want(t, http.StatusOK, "")
		ids := func(path, bearer string) []string {
			var page struct {
				Items []struct {
					ID        string `json:"id"`
					ProjectID string `json:"project_id"`
				} `json:"items"`
			}
			r := s.do(call{method: "GET", path: path, bearer: bearer})
			r.want(t, http.StatusOK, "")
			r.decode(t, &page)
			var out []string
			for _, it := range page.Items {
				out = append(out, it.ID+"|"+it.ProjectID)
			}
			return out
		}
		if got := ids(f.base+"/projects", f.scopedToken); len(got) != 1 || !strings.HasPrefix(got[0], f.aID) {
			t.Errorf("a project-scoped token lists projects %v; want only %s", got, f.aID)
		}
		if got := ids(f.base+"/projects", f.ownerRO); len(got) != 2 {
			t.Errorf("an unscoped token lists %d projects; want both", len(got))
		}
		if got := ids(f.base+"/export-jobs", f.scopedToken); len(got) != 1 || !strings.HasSuffix(got[0], f.aID) {
			t.Errorf("a project-scoped token lists export jobs %v; want only project %s's", got, f.aID)
		}
		s.do(call{method: "POST", path: f.base + "/export-jobs", bearer: f.scopedToken,
			body: map[string]any{"project_id": f.bID, "format": "json"}}).want(t, http.StatusNotFound, "not_found")
	})

	// Visibility `assigned`: every GET operation the decisions refuse
	// an assigned member answers 4xx — never data — whatever it names.
	t.Run("assigned: every read the decisions refuse", func(t *testing.T) {
		for _, pattern := range gets {
			d := restrictions[pattern]
			path := fillPath(t, pattern, map[string]string{"tenant": f.tenant, "project": f.aID})
			r := s.do(call{method: "GET", path: path, cookie: f.vera.cookie})
			switch d.assigned {
			case assignedDenied:
				// A deployment without a GitHub App answers its GitHub
				// routes 503 before anything is read, for everyone.
				unavailable := r.status == http.StatusServiceUnavailable && r.problem.Code == "github_not_configured"
				if (r.status < 400 || r.status >= 500) && !unavailable {
					t.Errorf("%s answered an assigned member %d (%s); the decision is %q", pattern, r.status, r.body, d.assigned)
				}
			case assignedAllowed:
				if r.status != http.StatusOK {
					t.Errorf("%s = %d for an assigned member; the decision is %q", pattern, r.status, d.assigned)
				}
			case assignedOwn:
				// Their own work: the list answers (with nothing, here —
				// Workflow holds no assignment of theirs), and an id that
				// is not theirs is not found.
				addressed := strings.Contains(pattern, "{assignment}")
				if !addressed && r.status != http.StatusOK || addressed && r.status != http.StatusNotFound {
					t.Errorf("%s = %d (%s) for an assigned member; the decision is %q", pattern, r.status, r.body, d.assigned)
				}
				if !addressed && strings.Contains(string(r.body), `"id"`) {
					t.Errorf("%s shows an assigned member work that is not theirs: %s", pattern, r.body)
				}
			}
		}
	})
	t.Run("assigned: covered units and nothing else", func(t *testing.T) {
		vera := f.vera
		get := func(path string) reply { return s.do(call{method: "GET", path: path, cookie: vera.cookie}) }
		keysOf := func(path string) []string {
			var page struct {
				Items []struct {
					ID     string `json:"id"`
					Key    string `json:"key"`
					Locale string `json:"locale"`
				} `json:"items"`
			}
			r := get(path)
			r.want(t, http.StatusOK, "")
			r.decode(t, &page)
			var out []string
			for _, it := range page.Items {
				switch {
				case it.Key != "" && it.Locale != "":
					out = append(out, it.Key+"/"+it.Locale)
				case it.Key != "":
					out = append(out, it.Key)
				case it.Locale != "":
					out = append(out, it.Locale)
				default:
					out = append(out, it.ID)
				}
			}
			return out
		}
		if got := keysOf(f.base + "/projects"); !slices.Equal(got, []string{f.aID}) {
			t.Errorf("projects = %v, want only %s", got, f.aID)
		}
		get(f.a).want(t, http.StatusOK, "")
		get(f.b).want(t, http.StatusNotFound, "not_found")
		if got := keysOf(f.a + "/messages"); !slices.Equal(got, []string{"pay"}) {
			t.Errorf("messages = %v, want only pay", got)
		}
		get(f.a+"/messages/pay").want(t, http.StatusOK, "")
		get(f.a+"/messages/cancel").want(t, http.StatusNotFound, "not_found")
		get(f.a+"/messages/pay/source-revisions").want(t, http.StatusOK, "")
		get(f.a+"/messages/cancel/source-revisions").want(t, http.StatusNotFound, "not_found")
		if got := keysOf(f.a + "/messages/pay/translations"); !slices.Equal(got, []string{"de"}) {
			t.Errorf("translations of pay = %v, want only de", got)
		}
		get(f.a+"/messages/pay/translations/de").want(t, http.StatusOK, "")
		get(f.a+"/messages/pay/translations/fr").want(t, http.StatusNotFound, "not_found")
		get(f.a+"/messages/cancel/translations/de").want(t, http.StatusNotFound, "not_found")
		if got := keysOf(f.a + "/translations?locale=de&locale=fr"); !slices.Equal(got, []string{"pay/de"}) {
			t.Errorf("project translations = %v, want only pay/de", got)
		}
		get(f.a+"/locales").want(t, http.StatusOK, "")
		get(f.a+"/messages/pay/usages").want(t, http.StatusOK, "")
		get(f.a+"/messages/cancel/usages").want(t, http.StatusNotFound, "not_found")
		get(f.base+"/effective-style-guide?project="+f.aID+"&locale=de").want(t, http.StatusOK, "")
		get(f.base+"/effective-style-guide?project="+f.aID+"&locale=fr").want(t, http.StatusNotFound, "not_found")
		get(f.base+"/tm-concordance?q=pay").want(t, http.StatusForbidden, "forbidden")

		cur := get(f.a + "/messages/pay/translations/de")
		s.do(call{method: "PUT", path: f.a + "/messages/pay/translations/de", cookie: vera.cookie, csrf: vera.csrf,
			body: map[string]string{"text": "Bezahlen"}, headers: map[string]string{"If-Match": cur.header.Get("ETag")}}).
			want(t, http.StatusOK, "")
		for _, other := range []string{"/messages/pay/translations/fr", "/messages/cancel/translations/de"} {
			s.do(call{method: "PUT", path: f.a + other, cookie: vera.cookie, csrf: vera.csrf,
				body: map[string]string{"text": "x"}}).want(t, http.StatusNotFound, "not_found")
		}
		for _, refused := range []call{
			{method: "POST", path: f.a + "/messages/pay/translations/de/reviews", body: map[string]any{"state": "approved"}, headers: map[string]string{"If-Match": `"1"`}},
			{method: "POST", path: f.a + "/releases", body: map[string]any{"environment": "production"}},
			{method: "POST", path: f.base + "/export-jobs", body: map[string]any{"project_id": f.aID, "format": "json"}},
			{method: "POST", path: f.base + "/import-jobs", body: map[string]any{"project_id": f.aID, "format": "json", "options": map[string]any{"locale": "de"}}},
			{method: "POST", path: f.base + "/tm-lookups", body: map[string]any{"source_locale": "en", "target_locale": "de", "source": "pay"}},
			{method: "POST", path: f.a + "/translation-imports", body: map[string]any{"items": []map[string]any{{"key": "pay", "locale": "de", "text": "x"}}}},
		} {
			refused.cookie, refused.csrf = vera.cookie, vera.csrf
			if r := s.do(refused); r.status != http.StatusForbidden {
				t.Errorf("%s %s = %d (%s) for an assigned member, want 403", refused.method, refused.path, r.status, r.body)
			}
		}
	})
	t.Run("unrestricted: sees what they saw before", func(t *testing.T) {
		var page struct {
			Items []struct{ Key string } `json:"items"`
		}
		r := s.do(call{method: "GET", path: f.a + "/messages", cookie: f.ada.cookie})
		r.want(t, http.StatusOK, "")
		r.decode(t, &page)
		if len(page.Items) != 2 {
			t.Errorf("the owner lists %d messages, want 2", len(page.Items))
		}
		s.do(call{method: "GET", path: f.b, cookie: f.ada.cookie}).want(t, http.StatusOK, "")
	})
}
