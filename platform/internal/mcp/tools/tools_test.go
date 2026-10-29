package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// readTools are the read tools of RFC 0005 §7.3, in the order Read
// registers them: the nine of wave 2 and check_run, which computes and
// stores nothing and so carries the `read` scope.
var readTools = []string{
	tools.CatalogSearchName, tools.MessageGetName, tools.TranslationGetName, tools.UsagesGetName,
	tools.TMSearchName, tools.TermLookupName, tools.StyleRulesName, tools.FindingsListName,
	tools.CheckRunName, tools.ExplainDeliveryName,
}

// ── the world under test ────────────────────────────────────────────

// world is one seeded tenant with one project, plus a second tenant
// whose project every isolation test asks for.
type world struct {
	tenant  tenancy.ID
	other   tenancy.ID
	project uuid.UUID
	// otherProject belongs to `other` and is the id an isolation test
	// hands a tool from a session bound to `tenant`.
	otherProject uuid.UUID
	message      uuid.UUID
	sources      tools.Sources
	knowledge    *fakeKnowledge
	quality      *fakeQuality
	// writes are the write ports' fakes, so a test can assert what
	// reached a context and not only what came back.
	writes *writers
}

func seed() *world {
	w := &world{
		tenant: tenancy.NewID(), other: tenancy.NewID(),
		project: uuid.New(), otherProject: uuid.New(), message: uuid.New(),
	}
	summary := func(key, ns, state, source string) tools.MessageSummary {
		return tools.MessageSummary{
			ID: uuid.New().String(), Key: key, Namespace: ns, State: state,
			Source: source, Syntax: "mf2", Revision: 3,
		}
	}
	pay := summary("checkout.pay", "checkout", "active", "Pay {$amount}")
	pay.ID = w.message.String()
	catalog := &fakeCatalog{projects: map[uuid.UUID]tenancy.ID{
		w.project: w.tenant, w.otherProject: w.other,
	}}
	add := func(tenant tenancy.ID, project uuid.UUID, s tools.MessageSummary, unused bool) {
		catalog.rows = append(catalog.rows, catalogRow{
			tenant: tenant, project: project, unused: unused,
			detail: tools.MessageDetail{
				MessageSummary: s, Description: "the pay button", Arguments: []string{"amount"},
			},
		})
	}
	add(w.tenant, w.project, pay, false)
	add(w.tenant, w.project, summary("checkout.tip", "checkout", "active", "Add a tip"), true)
	add(w.tenant, w.project, summary("home.title", "", "active", "Welcome"), false)
	// The other tenant's project holds a message with the same key, so a
	// leak would be visible rather than merely possible.
	add(w.other, w.otherProject, summary("checkout.pay", "checkout", "active", "SECRET"), false)

	translations := &fakeTranslations{rows: []translationRow{
		{tenant: w.tenant, project: w.project, tr: tools.Translation{
			MessageID: w.message.String(), Key: "checkout.pay", Locale: "de", Text: "Zahlen {$amount}",
			Syntax: "mf2", State: "approved", Origin: "human", By: "person:1",
			SourceRevision: 2, CurrentSourceRevision: 3, Outdated: true, Revision: 4,
			Warnings: []string{"argument_missing"},
		}},
		{tenant: w.other, project: w.otherProject, tr: tools.Translation{
			Key: "checkout.pay", Locale: "de", Text: "SECRET",
		}},
	}}

	usages := &fakeUsages{rows: []usageRow{
		{
			tenant: w.tenant, project: w.project, key: "checkout.pay", message: w.message, captures: 4,
			usages: []tools.Usage{
				{File: "src/Checkout.vue", Line: 12, Component: "Checkout", Route: "/checkout", Kind: "t", OnDefaultBranch: true},
				{File: "src/Cart.vue", Line: 90, Component: "Cart", Route: "/cart", Kind: "t", OnDefaultBranch: true},
				{File: "src/Pay.vue", Line: 3, Component: "Pay", Route: "/pay", Kind: "t", OnDefaultBranch: true},
			},
			neighbours: []tools.Neighbour{{ID: uuid.New().String(), Key: "checkout.total"}},
		},
		{tenant: w.other, project: w.otherProject, key: "checkout.pay", message: w.message, captures: 99},
	}}

	w.knowledge = &fakeKnowledge{rows: []knowledgeRow{
		{
			tenant: w.tenant, project: w.project,
			matches: []tools.TMMatch{
				{Unit: uuid.New().String(), Score: 101, Kind: "context", Source: "Pay", Target: "Zahlen", Syntax: "mf2", MessageKey: "checkout.pay"},
				{Unit: uuid.New().String(), Score: 82, Kind: "fuzzy", Source: "Pay now", Target: "Jetzt zahlen", Syntax: "mf2"},
			},
			terms: []tools.Term{{Concept: uuid.New().String(), Text: "cart", Status: "preferred", Start: 0, End: 4}},
			style: tools.Style{
				Fields:  json.RawMessage(`{"formality":{"register":"formal"}}`),
				Rules:   []tools.StyleRule{{ID: "no-exclamation", Title: "No exclamation marks"}},
				Sources: []tools.StyleGuideRef{{ID: uuid.New().String(), Version: 2, Scope: "project"}},
			},
		},
		{tenant: w.other, project: w.otherProject, matches: []tools.TMMatch{{Unit: "secret", Score: 100, Target: "SECRET"}}},
	}}

	w.quality = &fakeQuality{rows: []qualityRow{
		{
			tenant: w.tenant, project: w.project,
			run: tools.CheckRun{
				ID: uuid.New().String(), Ref: "main", Trigger: "cli", Conclusion: "failure",
				PolicyVersion: 3, Layers: []string{"structure", "parity"}, Errors: 1, Warnings: 1,
			},
			findings: []tools.Finding{
				{Fingerprint: "fp-parity", Layer: "parity", Code: "argument_missing", Severity: "error", Key: "checkout.pay", Locale: "de"},
				{Fingerprint: "fp-structure", Layer: "structure", Code: "parse_error", Severity: "warning", Key: "home.title", Locale: "fr"},
			},
		},
		{tenant: w.other, project: w.otherProject, run: tools.CheckRun{ID: "secret", Ref: "secret"}},
	}}

	release := uuid.New()
	delivery := &fakeDelivery{rows: []deliveryRow{
		{
			tenant: w.tenant, project: w.project, environment: "production",
			served: tools.Served{
				ReleaseID: release.String(), Version: 7, Digest: "abc", Environment: "production",
				SourceLocale: "en", Locales: []string{"en", "de", "de-AT"},
				Fallback: map[string][]string{"de-AT": {"de"}},
				Artifacts: map[string]map[string]string{
					"en":    {"checkout": "d-en"},
					"de":    {"checkout": "d-de"},
					"de-AT": {"checkout": "d-de-at"},
				},
			},
			carried: map[string]map[string][]string{
				"en": {"checkout": {"checkout.pay"}},
				"de": {"checkout": {"checkout.pay"}},
				// de-AT ships the namespace but not this message, which is
				// why resolution has to walk on to de.
				"de-AT": {"checkout": {}},
			},
		},
		{
			tenant: w.other, project: w.otherProject, environment: "production",
			served: tools.Served{ReleaseID: uuid.New().String(), SourceLocale: "en", Locales: []string{"en"}},
		},
	}}

	w.sources = tools.Sources{
		Catalog: catalog, Translations: translations, Usages: usages,
		Knowledge: w.knowledge, Quality: w.quality, Delivery: delivery,
	}
	w.writes = seedWrites(w)
	return w
}

// ── the service the tools run in ────────────────────────────────────

type fakeAuth struct{ caller app.Caller }

func (f fakeAuth) Authenticate(context.Context, string) (app.Caller, error) { return f.caller, nil }

type recordingAudit struct{ entries []app.AuditEntry }

func (r *recordingAudit) Record(_ context.Context, e app.AuditEntry) error {
	r.entries = append(r.entries, e)
	return nil
}

func callerWith(t *testing.T, tenant tenancy.ID, scopes ...string) app.Caller {
	t.Helper()
	ss, err := identity.ParseScopes(scopes)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	token := identity.NewTokenID()
	return app.Caller{
		Tenant: tenant, Token: token, Scopes: ss,
		Principal: authz.Principal{
			Actor: identity.TokenActor(token), Tenant: tenant, TokenTenant: tenant,
			Grant: identity.GrantForScopes(ss),
		},
	}
}

// session builds a session on the world's tenant, with every tool the
// world's ports support — read and write — registered in the service,
// because the toolset gate is only worth testing against the tools it
// actually guards.
func session(t *testing.T, w *world, audit *recordingAudit, scopes ...string) (*app.Service, app.Session) {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []string{"read"}
	}
	caller := callerWith(t, w.tenant, scopes...)
	opts := []app.Option{app.WithTools(tools.All(w.sources)...)}
	if audit != nil {
		opts = append(opts, app.WithAudit(audit))
	}
	svc, err := app.New(fakeAuth{caller: caller}, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := domain.ToolsetRead
	if slices.Contains(scopes, "write") {
		ts = domain.ToolsetWrite
	}
	return svc, svc.Open("streamable-http", caller, ts)
}

// call runs a tool with JSON arguments built from a map.
func call(t *testing.T, svc *app.Service, sess app.Session, name string, args map[string]any) (app.Result, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("arguments: %v", err)
	}
	return svc.Call(t.Context(), sess, name, raw)
}

// ── registration ────────────────────────────────────────────────────

func TestReadRegistersTheNineToolsInTheReadToolset(t *testing.T) {
	w := seed()
	got := tools.Read(w.sources)
	names := make([]string, len(got))
	for i, tool := range got {
		names[i] = tool.Name
		if tool.Toolset != domain.ToolsetRead {
			t.Errorf("%s is in the %s toolset, want read", tool.Name, tool.Toolset)
		}
		if !tool.ReadOnly {
			t.Errorf("%s is not marked read-only", tool.Name)
		}
		if tool.Permission == "" {
			t.Errorf("%s checks no permission", tool.Name)
		}
		if tool.Description == "" || tool.Title == "" {
			t.Errorf("%s has no description or title", tool.Name)
		}
	}
	if !slices.Equal(names, readTools) {
		t.Fatalf("tools = %v, want %v", names, readTools)
	}
}

// A port that is not wired leaves its tools out rather than advertising
// something that cannot answer.
func TestReadSkipsToolsWithoutTheirPort(t *testing.T) {
	got := tools.Read(tools.Sources{})
	if len(got) != 0 {
		t.Fatalf("tools = %d, want none", len(got))
	}
}

// No tool takes a tenant, and none accepts an argument it did not
// declare: the session's tenant is the only one there is (RFC 0005
// §7.2).
func TestNoToolTakesATenant(t *testing.T) {
	w := seed()
	for _, tool := range tools.All(w.sources) {
		var schema struct {
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
			AdditionalProperties *bool                      `json:"additionalProperties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("%s: schema: %v", tool.Name, err)
		}
		for name := range schema.Properties {
			if strings.Contains(name, "tenant") {
				t.Errorf("%s declares a %q argument", tool.Name, name)
			}
		}
		if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
			t.Errorf("%s accepts undeclared arguments", tool.Name)
		}
		if !slices.Contains(schema.Required, "project") {
			t.Errorf("%s does not require a project", tool.Name)
		}
	}
}

func TestATenantArgumentIsRefused(t *testing.T) {
	w := seed()
	svc, sess := session(t, w, nil)
	_, err := call(t, svc, sess, tools.CatalogSearchName, map[string]any{
		"project": w.project.String(), "tenant": w.other.String(),
	})
	var invalid *app.InvalidArgumentError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want an invalid-argument error", err)
	}
}

// ── happy paths ─────────────────────────────────────────────────────

func TestToolsAnswerSeededData(t *testing.T) {
	w := seed()
	svc, sess := session(t, w, nil)
	tests := []struct {
		name  string
		tool  string
		args  map[string]any
		check func(*testing.T, map[string]any)
	}{
		{
			name: "catalog_search lists a project's messages in key order",
			tool: tools.CatalogSearchName,
			args: map[string]any{"project": w.project.String()},
			check: func(t *testing.T, data map[string]any) {
				keys := keysOf(t, data["messages"])
				if !slices.Equal(keys, []string{"checkout.pay", "checkout.tip", "home.title"}) {
					t.Fatalf("keys = %v", keys)
				}
			},
		},
		{
			name: "catalog_search narrows by key prefix",
			tool: tools.CatalogSearchName,
			args: map[string]any{"project": w.project.String(), "key_prefix": "checkout."},
			check: func(t *testing.T, data map[string]any) {
				if keys := keysOf(t, data["messages"]); len(keys) != 2 {
					t.Fatalf("keys = %v, want the two checkout messages", keys)
				}
			},
		},
		{
			name: "catalog_search answers `unused` from Context",
			tool: tools.CatalogSearchName,
			args: map[string]any{"project": w.project.String(), "unused": true},
			check: func(t *testing.T, data map[string]any) {
				if keys := keysOf(t, data["messages"]); !slices.Equal(keys, []string{"checkout.tip"}) {
					t.Fatalf("keys = %v, want the unused message", keys)
				}
			},
		},
		{
			name: "message_get returns the message with its usages, captures and neighbours",
			tool: tools.MessageGetName,
			args: map[string]any{"project": w.project.String(), "key": "checkout.pay"},
			check: func(t *testing.T, data map[string]any) {
				if data["key"] != "checkout.pay" || data["source"] != "Pay {$amount}" {
					t.Fatalf("message = %v", data)
				}
				if n := len(data["usages"].([]any)); n != 3 {
					t.Fatalf("usages = %d, want 3", n)
				}
				if data["captures"].(float64) != 4 {
					t.Fatalf("captures = %v, want 4", data["captures"])
				}
				if n := len(data["neighbours"].([]any)); n != 1 {
					t.Fatalf("neighbours = %d, want 1", n)
				}
			},
		},
		{
			name: "translation_get returns the text with its provenance and outdated flag",
			tool: tools.TranslationGetName,
			args: map[string]any{"project": w.project.String(), "key": "checkout.pay", "locale": "de"},
			check: func(t *testing.T, data map[string]any) {
				if data["text"] != "Zahlen {$amount}" || data["state"] != "approved" ||
					data["origin"] != "human" || data["outdated"] != true {
					t.Fatalf("translation = %v", data)
				}
			},
		},
		{
			name: "usages_get returns file and line",
			tool: tools.UsagesGetName,
			args: map[string]any{"project": w.project.String(), "key": "checkout.pay"},
			check: func(t *testing.T, data map[string]any) {
				first := data["usages"].([]any)[0].(map[string]any)
				if first["file"] != "src/Checkout.vue" || first["line"].(float64) != 12 {
					t.Fatalf("usage = %v", first)
				}
				if data["truncated"] != false {
					t.Fatalf("truncated = %v", data["truncated"])
				}
			},
		},
		{
			name: "tm_search returns matches best first",
			tool: tools.TMSearchName,
			args: map[string]any{
				"project": w.project.String(), "text": "Pay", "source_locale": "en", "target_locale": "de",
			},
			check: func(t *testing.T, data map[string]any) {
				matches := data["matches"].([]any)
				if len(matches) != 2 || matches[0].(map[string]any)["score"].(float64) != 101 {
					t.Fatalf("matches = %v", matches)
				}
			},
		},
		{
			name: "term_lookup returns the recognized terms",
			tool: tools.TermLookupName,
			args: map[string]any{"project": w.project.String(), "text": "cart total", "locale": "en"},
			check: func(t *testing.T, data map[string]any) {
				terms := data["terms"].([]any)
				if len(terms) != 1 || terms[0].(map[string]any)["text"] != "cart" {
					t.Fatalf("terms = %v", terms)
				}
			},
		},
		{
			name: "style_rules returns the merged fields, rules and sources",
			tool: tools.StyleRulesName,
			args: map[string]any{"project": w.project.String(), "locale": "de"},
			check: func(t *testing.T, data map[string]any) {
				if len(data["rules"].([]any)) != 1 || len(data["sources"].([]any)) != 1 {
					t.Fatalf("style = %v", data)
				}
				fields := data["fields"].(map[string]any)["formality"].(map[string]any)
				if fields["register"] != "formal" {
					t.Fatalf("fields = %v", data["fields"])
				}
			},
		},
		{
			name: "findings_list returns the run and its findings",
			tool: tools.FindingsListName,
			args: map[string]any{"project": w.project.String()},
			check: func(t *testing.T, data map[string]any) {
				run := data["run"].(map[string]any)
				if run["ref"] != "main" || run["conclusion"] != "failure" {
					t.Fatalf("run = %v", run)
				}
				if n := len(data["findings"].([]any)); n != 2 {
					t.Fatalf("findings = %d, want 2", n)
				}
			},
		},
		{
			name: "findings_list filters by layer",
			tool: tools.FindingsListName,
			args: map[string]any{"project": w.project.String(), "layer": "parity"},
			check: func(t *testing.T, data map[string]any) {
				findings := data["findings"].([]any)
				if len(findings) != 1 || findings[0].(map[string]any)["code"] != "argument_missing" {
					t.Fatalf("findings = %v", findings)
				}
			},
		},
		{
			name: "explain_delivery walks the fallback chain to the locale that has the message",
			tool: tools.ExplainDeliveryName,
			args: map[string]any{
				"project": w.project.String(), "environment": "production", "locale": "de-AT", "key": "checkout.pay",
			},
			check: func(t *testing.T, data map[string]any) {
				if data["locale"] != "de-AT" {
					t.Fatalf("active locale = %v", data["locale"])
				}
				if chain := stringsOf(data["chain"]); !slices.Equal(chain, []string{"de-AT", "de", "en"}) {
					t.Fatalf("chain = %v", chain)
				}
				if data["resolved_from"] != "de" {
					t.Fatalf("resolved_from = %v, want de", data["resolved_from"])
				}
				steps := data["steps"].([]any)
				if steps[0].(map[string]any)["outcome"] != "missing" ||
					steps[1].(map[string]any)["outcome"] != "found" {
					t.Fatalf("steps = %v", steps)
				}
			},
		},
		{
			name: "explain_delivery without a key still explains negotiation",
			tool: tools.ExplainDeliveryName,
			args: map[string]any{"project": w.project.String(), "environment": "production", "locale": "fr"},
			check: func(t *testing.T, data map[string]any) {
				if data["locale"] != "en" {
					t.Fatalf("active locale = %v, want the source locale", data["locale"])
				}
				if data["resolved_from"] != nil {
					t.Fatalf("resolved_from = %v, want none", data["resolved_from"])
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := call(t, svc, sess, tc.tool, tc.args)
			if err != nil {
				t.Fatalf("%s: %v", tc.tool, err)
			}
			if res.Explanation == "" {
				t.Errorf("%s answered without an explanation", tc.tool)
			}
			if len(res.Affected) != 0 {
				t.Errorf("%s reports affected ids; a read changes nothing", tc.tool)
			}
			tc.check(t, jsonOf(res.Data))
		})
	}
}

func keysOf(t *testing.T, v any) []string {
	t.Helper()
	rows, ok := v.([]any)
	if !ok {
		t.Fatalf("messages = %v", v)
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i], _ = row.(map[string]any)["key"].(string)
	}
	return out
}

func stringsOf(v any) []string {
	rows, _ := v.([]any)
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i], _ = row.(string)
	}
	return out
}

// ── tenant isolation ────────────────────────────────────────────────

// Every tool must answer "not found" for another tenant's project — not
// "forbidden", which would confirm the id exists (RFC 0005 §7.2). The
// fakes enforce the same rule row-level security does, so this is the
// real path and not an assertion about the tools' arguments.
func TestCrossTenantIdsAreNotFound(t *testing.T) {
	w := seed()
	svc, sess := session(t, w, nil)
	cases := map[string]map[string]any{
		tools.CatalogSearchName:   {"project": w.otherProject.String()},
		tools.MessageGetName:      {"project": w.otherProject.String(), "key": "checkout.pay"},
		tools.TranslationGetName:  {"project": w.otherProject.String(), "key": "checkout.pay", "locale": "de"},
		tools.UsagesGetName:       {"project": w.otherProject.String(), "key": "checkout.pay"},
		tools.TMSearchName:        {"project": w.otherProject.String(), "text": "Pay", "source_locale": "en", "target_locale": "de"},
		tools.TermLookupName:      {"project": w.otherProject.String(), "text": "cart", "locale": "en"},
		tools.StyleRulesName:      {"project": w.otherProject.String(), "locale": "de"},
		tools.FindingsListName:    {"project": w.otherProject.String()},
		tools.CheckRunName:        {"project": w.otherProject.String()},
		tools.ExplainDeliveryName: {"project": w.otherProject.String(), "environment": "production", "locale": "de"},
	}
	for _, name := range readTools {
		t.Run(name, func(t *testing.T) {
			args, ok := cases[name]
			if !ok {
				t.Fatalf("no cross-tenant case for %s", name)
			}
			res, err := call(t, svc, sess, name, args)
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("err = %v, want not found", err)
			}
			if errors.Is(err, authz.ErrForbidden) {
				t.Fatal("a cross-tenant id was refused as forbidden, which confirms it exists")
			}
			// Nothing of the other tenant's may travel back, not even in
			// the error's text.
			if strings.Contains(err.Error(), w.other.String()) ||
				strings.Contains(err.Error(), w.otherProject.String()) ||
				strings.Contains(fmt.Sprint(res.Data), "SECRET") {
				t.Fatalf("the refusal carries the other tenant's detail: %v / %v", err, res.Data)
			}
		})
	}
}

// catalog_search is the one tool whose non-empty answer would be the
// leak itself: the other tenant's project holds a message with the same
// key, and a session bound to this tenant must simply not see it.
func TestSearchNeverCrossesTenants(t *testing.T) {
	w := seed()
	svc, sess := session(t, w, nil)
	res, err := call(t, svc, sess, tools.CatalogSearchName, map[string]any{
		"project": w.project.String(), "key_prefix": "checkout.pay",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(res.Data); strings.Contains(got, "SECRET") {
		t.Fatalf("the search returned another tenant's source: %s", got)
	}
}

// ── limits and cursors ──────────────────────────────────────────────

func TestLimitsAreBoundedAndEnforced(t *testing.T) {
	w := seed()
	svc, sess := session(t, w, nil)
	tests := []struct {
		name  string
		tool  string
		args  map[string]any
		limit int
	}{
		{name: "catalog_search", tool: tools.CatalogSearchName, args: map[string]any{"project": w.project.String()}, limit: 101},
		{name: "message_get", tool: tools.MessageGetName, args: map[string]any{"project": w.project.String(), "key": "checkout.pay"}, limit: 0},
		{name: "usages_get", tool: tools.UsagesGetName, args: map[string]any{"project": w.project.String(), "key": "checkout.pay"}, limit: -1},
		{name: "tm_search over the TM cap", tool: tools.TMSearchName, args: map[string]any{
			"project": w.project.String(), "text": "Pay", "source_locale": "en", "target_locale": "de",
		}, limit: 51},
		{name: "term_lookup", tool: tools.TermLookupName, args: map[string]any{
			"project": w.project.String(), "text": "cart", "locale": "en",
		}, limit: 101},
		{name: "findings_list", tool: tools.FindingsListName, args: map[string]any{"project": w.project.String()}, limit: 101},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"limit": tc.limit}
			for k, v := range tc.args {
				args[k] = v
			}
			_, err := call(t, svc, sess, tc.tool, args)
			var invalid *app.InvalidArgumentError
			if !errors.As(err, &invalid) || invalid.Argument != "limit" {
				t.Fatalf("err = %v, want an invalid limit", err)
			}
		})
	}
}

func TestDefaultLimitsReachTheContexts(t *testing.T) {
	w := seed()
	svc, sess := session(t, w, nil)
	if _, err := call(t, svc, sess, tools.TMSearchName, map[string]any{
		"project": w.project.String(), "text": "Pay", "source_locale": "en", "target_locale": "de",
	}); err != nil {
		t.Fatal(err)
	}
	if w.knowledge.lastTM.Limit != tools.DefaultLimit {
		t.Fatalf("TM limit = %d, want the default %d", w.knowledge.lastTM.Limit, tools.DefaultLimit)
	}
	if _, err := call(t, svc, sess, tools.FindingsListName, map[string]any{
		"project": w.project.String(),
	}); err != nil {
		t.Fatal(err)
	}
	if w.quality.lastQuery.Limit != tools.DefaultLimit {
		t.Fatalf("findings limit = %d, want the default %d", w.quality.lastQuery.Limit, tools.DefaultLimit)
	}
}

// A list tool whose source is keyset-paginated hands the cursor back,
// and continuing with it does not repeat a row.
func TestCatalogSearchPaginates(t *testing.T) {
	w := seed()
	svc, sess := session(t, w, nil)
	res, err := call(t, svc, sess, tools.CatalogSearchName, map[string]any{
		"project": w.project.String(), "limit": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	first := jsonOf(res.Data)
	cursor, _ := first["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("no cursor on a truncated page")
	}
	res, err = call(t, svc, sess, tools.CatalogSearchName, map[string]any{
		"project": w.project.String(), "limit": 2, "cursor": cursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	second := jsonOf(res.Data)
	if keys := keysOf(t, second["messages"]); !slices.Equal(keys, []string{"home.title"}) {
		t.Fatalf("second page = %v", keys)
	}
	if second["next_cursor"] != nil {
		t.Fatalf("a last page issued a cursor: %v", second["next_cursor"])
	}
}

// usages_get has no cursor to give — Context answers a message's usages
// in one bounded read — so it must say that the list was cut.
func TestUsagesReportTruncation(t *testing.T) {
	w := seed()
	svc, sess := session(t, w, nil)
	res, err := call(t, svc, sess, tools.UsagesGetName, map[string]any{
		"project": w.project.String(), "key": "checkout.pay", "limit": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	data := jsonOf(res.Data)
	if len(data["usages"].([]any)) != 1 || data["truncated"] != true {
		t.Fatalf("usages = %v", data)
	}
}

// ── the toolset gate ────────────────────────────────────────────────

// Wave 2 ships no mutating tool. The gate is asserted anyway, because
// wave 3 adds five and the day it does is the wrong day to find out.
func TestAReadSessionCannotCallAMutatingTool(t *testing.T) {
	w := seed()
	svc, sess := session(t, w, nil, "read", "write")
	if sess.Toolset != domain.ToolsetWrite {
		t.Fatal("the helper opened the wrong toolset")
	}
	// The same service, entered through a read session.
	read := svc.Open("streamable-http", callerWith(t, w.tenant, "read", "write"), domain.ToolsetRead)
	if _, err := call(t, svc, read, "message_upsert", nil); !errors.Is(err, domain.ErrToolNotInSession) {
		t.Fatalf("err = %v, want ErrToolNotInSession", err)
	}
	listed := make([]string, 0, len(readTools))
	for _, tool := range svc.Tools(domain.ToolsetRead) {
		if tool.Name == app.WhoAmIName {
			continue
		}
		listed = append(listed, tool.Name)
	}
	if !slices.Equal(listed, readTools) {
		t.Fatalf("a read session sees %v, want the nine read tools", listed)
	}
	// Every read tool is reachable from a write session too.
	for _, name := range readTools {
		if _, ok := toolNamed(svc.Tools(domain.ToolsetWrite), name); !ok {
			t.Errorf("a write session does not see %s", name)
		}
	}
}

func toolNamed(all []app.Tool, name string) (app.Tool, bool) {
	for _, tool := range all {
		if tool.Name == name {
			return tool, true
		}
	}
	return app.Tool{}, false
}

// ── the audit ledger ────────────────────────────────────────────────

// The ledger records the shape of the arguments, never their content.
// tm_search is the case that matters: its `text` is message text, so it
// must never be a selector (RFC 0005 §7.2, §11).
func TestAuditRecordsShapesAndRedactsText(t *testing.T) {
	w := seed()
	audit := &recordingAudit{}
	svc, sess := session(t, w, audit)
	secret := "Pay the invoice now, Mrs Andersson"
	if _, err := call(t, svc, sess, tools.TMSearchName, map[string]any{
		"project": w.project.String(), "text": secret, "source_locale": "en", "target_locale": "de",
	}); err != nil {
		t.Fatal(err)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(audit.entries))
	}
	e := audit.entries[0]
	if e.Tool != tools.TMSearchName || e.Outcome != domain.OutcomeOK {
		t.Fatalf("row = %+v", e)
	}
	if got := e.Arguments["text"]; got != fmt.Sprintf("string(len=%d)", len([]rune(secret))) {
		t.Fatalf("text shape = %q, want its length only", got)
	}
	for _, v := range e.Arguments {
		if strings.Contains(v, "Andersson") {
			t.Fatalf("the ledger carries message text: %v", e.Arguments)
		}
	}
	// Identifiers and enumerations stay readable: a row nobody can read
	// is not an audit.
	if e.Arguments["project"] != w.project.String() || e.Arguments["target_locale"] != "de" {
		t.Fatalf("selectors were redacted: %v", e.Arguments)
	}
}

// Every read tool's free-text arguments are redacted, and every one of
// its selectors is readable. The table is the contract.
func TestEveryToolRedactsItsFreeText(t *testing.T) {
	w := seed()
	text := "some source text"
	tests := []struct {
		tool     string
		args     map[string]any
		redacted []string
	}{
		{tool: tools.TMSearchName, redacted: []string{"text"}, args: map[string]any{
			"project": w.project.String(), "text": text, "source_locale": "en", "target_locale": "de",
		}},
		{tool: tools.TermLookupName, redacted: []string{"text"}, args: map[string]any{
			"project": w.project.String(), "text": text, "locale": "en",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			audit := &recordingAudit{}
			svc, sess := session(t, w, audit)
			if _, err := call(t, svc, sess, tc.tool, tc.args); err != nil {
				t.Fatal(err)
			}
			shape := audit.entries[0].Arguments
			for _, name := range tc.redacted {
				if shape[name] != fmt.Sprintf("string(len=%d)", len([]rune(text))) {
					t.Fatalf("%s.%s = %q, want a length", tc.tool, name, shape[name])
				}
			}
			if shape["project"] != w.project.String() {
				t.Fatalf("%s hid its project: %v", tc.tool, shape)
			}
		})
	}
}

// A refusal is audited exactly like an answer, with the outcome that
// says what happened.
func TestARefusalIsAudited(t *testing.T) {
	w := seed()
	audit := &recordingAudit{}
	svc, sess := session(t, w, audit)
	if _, err := call(t, svc, sess, tools.MessageGetName, map[string]any{
		"project": w.otherProject.String(), "key": "checkout.pay",
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if len(audit.entries) != 1 || audit.entries[0].Outcome != domain.OutcomeInvalid {
		t.Fatalf("audit = %+v, want one invalid row", audit.entries)
	}
	if audit.entries[0].Arguments["project"] != w.otherProject.String() {
		t.Fatalf("the refused call's project was not recorded: %v", audit.entries[0].Arguments)
	}
}
