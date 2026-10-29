package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	acmeUUID  = "0192a000-0000-7000-8000-000000000001"
	otherUUID = "0192a000-0000-7000-8000-000000000002"
)

// fakeTenancy is a small server for the tenants and projects commands:
// several tenants, projects with locales, createProject and addLocale.
type fakeTenancy struct {
	srv *httptest.Server

	mu         sync.Mutex
	tenants    []map[string]any
	projects   map[string][]map[string]any // tenant → projects
	locales    map[string][]string         // project id → codes
	forbid     bool
	keys       []string
	paths      []string
	failLocale string
}

func newFakeTenancy(t *testing.T) *fakeTenancy {
	f := &fakeTenancy{projects: map[string][]map[string]any{}, locales: map[string][]string{}}
	f.tenants = []map[string]any{
		{"id": acmeUUID, "kind": "organization", "slug": "acme", "name": "Acme", "created_at": "2026-09-19T00:00:00Z"},
		{"id": otherUUID, "kind": "individual", "slug": "felix", "name": "Felix", "created_at": "2026-09-19T00:00:00Z"},
	}
	f.projects[acmeUUID] = []map[string]any{f.project("prj_1", "shop", "Shop", "de")}
	f.locales["prj_1"] = []string{"de", "en"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSONResp(w, 200, map[string]any{"items": f.tenants})
	})
	mux.HandleFunc("GET /v1/tenants/{tenant}/projects", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.forbid {
			problemResp(w, 403, "forbidden", "needs catalog.read")
			return
		}
		items := f.projects[r.PathValue("tenant")]
		if items == nil {
			items = []map[string]any{}
		}
		writeJSONResp(w, 200, map[string]any{"items": items})
	})
	mux.HandleFunc("POST /v1/tenants/{tenant}/projects", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
		if f.forbid {
			problemResp(w, 403, "forbidden", "needs catalog.write")
			return
		}
		var body struct {
			Slug         string
			Name         string
			SourceLocale string `json:"source_locale"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		tenant := r.PathValue("tenant")
		for _, p := range f.projects[tenant] {
			if p["slug"] == body.Slug {
				problemResp(w, 409, "slug_taken", "the slug is taken")
				return
			}
		}
		id := "prj_" + body.Slug
		f.projects[tenant] = append(f.projects[tenant], f.project(id, body.Slug, body.Name, body.SourceLocale))
		f.locales[id] = []string{body.SourceLocale}
		writeJSONResp(w, 201, f.project(id, body.Slug, body.Name, body.SourceLocale))
	})
	mux.HandleFunc("GET /v1/tenants/{tenant}/projects/{project}/locales", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		items := []map[string]any{}
		for i, c := range f.locales[r.PathValue("project")] {
			items = append(items, map[string]any{"code": c, "direction": "ltr", "is_source": i == 0, "created_at": "2026-09-19T00:00:00Z"})
		}
		writeJSONResp(w, 200, map[string]any{"items": items})
	})
	mux.HandleFunc("POST /v1/tenants/{tenant}/projects/{project}/locales", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		var body struct{ Code string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Code == f.failLocale {
			problemResp(w, 400, "invalid_locale", "no")
			return
		}
		id := r.PathValue("project")
		f.locales[id] = append(f.locales[id], body.Code)
		writeJSONResp(w, 201, map[string]any{"code": body.Code, "direction": "ltr", "is_source": false, "created_at": "2026-09-19T00:00:00Z"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTenancy) project(id, slug, name, source string) map[string]any {
	return map[string]any{"id": id, "slug": slug, "name": name, "source_locale": source,
		"settings":   map[string]any{"default_syntax": "mf1", "review_required": true},
		"created_at": "2026-09-19T00:00:00Z", "updated_at": "2026-09-19T00:00:00Z"}
}

func (f *fakeTenancy) record(r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
}

func (f *fakeTenancy) workspace(t *testing.T, tenant string) *workspace {
	w := newWorkspace(t)
	y := "version: 1\nserver: " + f.srv.URL + "\n"
	if tenant != "" {
		y += "tenant: " + tenant + "\n"
	}
	w.write("glossa.yaml", y+"project: shop\nsource_locale: de\ncatalogs:\n  path: locales/{locale}.json\n")
	w.env["GLOSSA_TOKEN"] = testToken
	return w
}

func TestTenantsListsWhatTheCredentialCanActIn(t *testing.T) {
	f := newFakeTenancy(t)
	w := f.workspace(t, "")
	r := w.run("tenants", "--json")
	r.want(t, ExitOK)
	golden(t, "tenants.json", r.stdout)
	var out tenantsDoc
	w.json(&out, "tenants")
	if len(out.Tenants) != 2 || out.Tenants[0].Slug != "acme" || out.Tenants[1].Kind != "individual" {
		t.Fatalf("tenants = %+v", out)
	}
	human := w.run("tenants")
	human.want(t, ExitOK)
	if !strings.Contains(human.stdout, "felix") || !strings.Contains(human.stdout, "organization") {
		t.Fatalf("tenants:\n%s", human.stdout)
	}
}

func TestProjectsListShowsLocalesAndResolvesTheTenantSlug(t *testing.T) {
	f := newFakeTenancy(t)
	w := f.workspace(t, "acme")
	r := w.run("projects", "list", "--json")
	r.want(t, ExitOK)
	golden(t, "projects-list.json", r.stdout)
	if got := strings.Join(f.paths, "\n"); !strings.Contains(got, "GET /v1/tenants/"+acmeUUID+"/projects") || strings.Contains(got, "/tenants/acme/") {
		t.Fatalf("the slug was not resolved before building paths:\n%s", got)
	}
	human := w.run("projects", "list")
	human.want(t, ExitOK)
	if !strings.Contains(human.stdout, "shop") || !strings.Contains(human.stdout, "de,en") {
		t.Fatalf("projects list:\n%s", human.stdout)
	}
}

func TestTenantUUIDNeedsNoLookup(t *testing.T) {
	f := newFakeTenancy(t)
	w := f.workspace(t, acmeUUID)
	w.run("projects", "list").want(t, ExitOK)
	if strings.Contains(strings.Join(f.paths, "\n"), "GET /v1/tenants\n") || f.paths[0] != "GET /v1/tenants/"+acmeUUID+"/projects" {
		t.Fatalf("paths = %v", f.paths)
	}
}

func TestUnknownTenantSlugListsTheChoices(t *testing.T) {
	f := newFakeTenancy(t)
	r := f.workspace(t, "nope").run("projects", "list", "--json")
	r.want(t, ExitUsage)
	var doc errorDoc
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Error.Code != "tenant_not_found" || !strings.Contains(doc.Error.Fix, "acme ("+acmeUUID+")") || !strings.Contains(doc.Error.Fix, "felix") {
		t.Fatalf("error = %+v", doc.Error)
	}
}

func TestAmbiguousTenantListsThemInTheFix(t *testing.T) {
	f := newFakeTenancy(t)
	// Any command that needs a project reaches the same resolution.
	r := f.workspace(t, "").run("locales", "--json")
	r.want(t, ExitUsage)
	var doc errorDoc
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	e := doc.Error
	if e.Code != "tenant_ambiguous" || !strings.Contains(e.Fix, "acme ("+acmeUUID+")") || !strings.Contains(e.Fix, "felix ("+otherUUID+")") {
		t.Fatalf("error = %+v", e)
	}
	human := f.workspace(t, "").run("projects", "list")
	human.want(t, ExitUsage)
	if !strings.Contains(human.stderr, "one of: acme") {
		t.Fatalf("stderr:\n%s", human.stderr)
	}
}

func TestProjectsCreateAddsTheExtraLocalesAndPrintsTheID(t *testing.T) {
	f := newFakeTenancy(t)
	w := f.workspace(t, "acme")
	r := w.run("projects", "create", "--name", "Brotwerk", "--slug", "brotwerk", "--source-locale", "de", "--locales", "de,en,fr", "--json")
	r.want(t, ExitOK)
	golden(t, "projects-create.json", r.stdout)
	if got := strings.Join(f.locales["prj_brotwerk"], ","); got != "de,en,fr" {
		t.Fatalf("locales = %s", got)
	}
	if len(f.keys) != 1 || !strings.HasPrefix(f.keys[0], "glossa-cli-") {
		t.Fatalf("Idempotency-Key = %v", f.keys)
	}
	human := w.run("projects", "create", "--name", "Second", "--slug", "second", "--source-locale", "en", "--idempotency-key", "k-1")
	human.want(t, ExitOK)
	if !strings.Contains(human.stdout, "prj_second") || f.keys[1] != "k-1" {
		t.Fatalf("create:\n%s\nkeys %v", human.stdout, f.keys)
	}
}

func TestProjectsCreateOnATakenSlugIsAConflict(t *testing.T) {
	f := newFakeTenancy(t)
	r := f.workspace(t, "acme").run("projects", "create", "--name", "Shop", "--slug", "shop", "--source-locale", "de", "--json")
	r.want(t, ExitNetwork)
	var doc errorDoc
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Error.Code != "slug_taken" || !strings.Contains(doc.Error.Message, `"shop" already exists`) || !strings.Contains(doc.Error.Fix, "another --slug") {
		t.Fatalf("error = %+v", doc.Error)
	}
}

func TestProjectsForbidden(t *testing.T) {
	f := newFakeTenancy(t)
	f.forbid = true
	w := f.workspace(t, "acme")
	r := w.run("projects", "create", "--name", "X", "--slug", "x", "--source-locale", "de", "--json")
	r.want(t, ExitNetwork)
	var doc errorDoc
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Error.Code != "forbidden" || !strings.Contains(doc.Error.Fix, "catalog.write") {
		t.Fatalf("error = %+v", doc.Error)
	}
	w.run("projects", "list").want(t, ExitNetwork)
}

func TestProjectsCreateReportsAPartialFailure(t *testing.T) {
	f := newFakeTenancy(t)
	f.failLocale = "fr"
	r := f.workspace(t, "acme").run("projects", "create", "--name", "B", "--slug", "b", "--source-locale", "de", "--locales", "en,fr", "--json")
	r.want(t, ExitNetwork)
	if !strings.Contains(r.stdout, "prj_b") || !strings.Contains(r.stdout, "Added so far: en") {
		t.Fatalf("stdout:\n%s", r.stdout)
	}
}

func TestProjectsUsageErrors(t *testing.T) {
	f := newFakeTenancy(t)
	w := f.workspace(t, "acme")
	w.run("projects").want(t, ExitUsage)
	w.run("projects", "create", "--slug", "x").want(t, ExitUsage)
	w.run("projects", "create", "--name", "X", "--slug", "Not A Slug", "--source-locale", "de").want(t, ExitUsage)
	w.run("projects", "create", "--name", "X", "--slug", "x", "--source-locale", "!!").want(t, ExitUsage)
	if len(f.paths) != 0 {
		t.Fatalf("a refused command reached the server: %v", f.paths)
	}
}
