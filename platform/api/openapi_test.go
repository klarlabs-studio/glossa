package api_test

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"go.klarlabs.de/glossa/platform/api"
)

func load(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(api.Spec)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return doc
}

type operation struct {
	method, path string
	op           *openapi3.Operation
	params       openapi3.Parameters // path-level + operation-level
}

func operations(doc *openapi3.T) []operation {
	var out []operation
	for _, path := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Value(path)
		for method, op := range item.Operations() {
			out = append(out, operation{method: method, path: path, op: op, params: append(slices.Clone(item.Parameters), op.Parameters...)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path+out[i].method < out[j].path+out[j].method })
	return out
}

func (o operation) name() string { return o.method + " " + o.path }

func (o operation) param(in, name string) *openapi3.Parameter {
	for _, p := range o.params {
		if p.Value != nil && p.Value.In == in && strings.EqualFold(p.Value.Name, name) {
			return p.Value
		}
	}
	return nil
}

func TestSpecIsValidOpenAPI31(t *testing.T) {
	doc := load(t)
	if !strings.HasPrefix(doc.OpenAPI, "3.1.") {
		t.Errorf("openapi = %q, want 3.1.x", doc.OpenAPI)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("invalid OpenAPI document: %v", err)
	}
}

// The rules below are the conventions in info.description, executable.

var (
	snakeCase   = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	kebabPath   = regexp.MustCompile(`^/v1(/([a-z][a-z0-9]*(-[a-z0-9]+)*|\{[a-z_]+\}))+$`)
	camelOpID   = regexp.MustCompile(`^[a-z][a-zA-Z0-9]+$`)
	problemJSON = "application/problem+json"
)

func TestOperationsFollowTheConventions(t *testing.T) {
	doc := load(t)
	seenIDs := map[string]bool{}
	for _, o := range operations(doc) {
		t.Run(o.name(), func(t *testing.T) {
			checkNaming(t, o, seenIDs)
			checkSecurity(t, doc, o)
			checkErrorResponses(t, o)
			checkTenantScoping(t, o)
			checkPagination(t, o)
			checkIdempotency(t, o)
			checkConcurrency(t, o)
		})
	}
}

func checkNaming(t *testing.T, o operation, seen map[string]bool) {
	if !kebabPath.MatchString(o.path) {
		t.Errorf("path is not /v1 + kebab-case segments")
	}
	id := o.op.OperationID
	if !camelOpID.MatchString(id) || seen[id] {
		t.Errorf("operationId %q missing, not camelCase, or duplicated", id)
	}
	seen[id] = true
	if len(o.op.Tags) != 1 {
		t.Errorf("needs exactly one tag")
	}
	if o.op.Summary == "" {
		t.Errorf("needs a summary")
	}
}

// Every operation states its security explicitly — public ones with
// `security: []` — because glossa-server enforces what the contract says.
func checkSecurity(t *testing.T, doc *openapi3.T, o operation) {
	if o.op.Security == nil {
		t.Fatal("no explicit security; use `security: []` for public operations")
	}
	unsafe := o.method != http.MethodGet && o.method != http.MethodHead
	for _, alt := range *o.op.Security {
		for scheme := range alt {
			if doc.Components.SecuritySchemes[scheme] == nil {
				t.Errorf("unknown security scheme %q", scheme)
			}
		}
		_, session := alt["session"]
		_, csrf := alt["csrf"]
		if session && unsafe && !csrf {
			t.Errorf("unsafe operation accepts the session cookie without the csrf header")
		}
		if csrf && !session {
			t.Errorf("csrf only makes sense together with the session cookie")
		}
	}
}

func checkErrorResponses(t *testing.T, o operation) {
	for code, ref := range o.op.Responses.Map() {
		if code >= "400" {
			if ref.Value == nil || ref.Value.Content.Get(problemJSON) == nil {
				t.Errorf("%s response is not %s", code, problemJSON)
			}
		}
	}
	if o.op.Security != nil && len(*o.op.Security) > 0 && o.op.Responses.Value("401") == nil {
		t.Errorf("authenticated operation doesn't document 401")
	}
}

func checkTenantScoping(t *testing.T, o operation) {
	if !strings.HasPrefix(o.path, "/v1/tenants/{") {
		return
	}
	if !strings.HasPrefix(o.path, "/v1/tenants/{tenant}") {
		t.Errorf("the tenant path parameter must be named {tenant}")
	}
	if o.op.Responses.Value("403") == nil {
		t.Errorf("tenant-scoped operation doesn't document 403")
	}
}

func checkPagination(t *testing.T, o operation) {
	ok := o.op.Responses.Value("200")
	if o.method != http.MethodGet || ok == nil || ok.Value.Content.Get("application/json") == nil {
		return
	}
	schema := ok.Value.Content.Get("application/json").Schema.Value
	_, isList := schema.Properties["items"]
	if !isList {
		return
	}
	// x-glossa-unpaged exempts a list the server itself bounds — a hard
	// cap is a stronger promise than a cursor — and must say why.
	if reason, _ := o.op.Extensions["x-glossa-unpaged"].(string); reason != "" {
		return
	}
	if _, ok := o.op.Extensions["x-glossa-unpaged"]; ok {
		t.Errorf("x-glossa-unpaged must say why the list needs no cursor")
		return
	}
	if _, ok := schema.Properties["next_page_token"]; !ok {
		t.Errorf("list response lacks next_page_token")
	}
	if o.param("query", "page_size") == nil || o.param("query", "page_token") == nil {
		t.Errorf("list operation lacks page_size/page_token")
	}
}

func checkIdempotency(t *testing.T, o operation) {
	creates := o.method == http.MethodPost && o.op.Responses.Value("201") != nil
	// x-glossa-idempotency explains why an operation is idempotent by
	// other means; it must say why.
	if reason, _ := o.op.Extensions["x-glossa-idempotency"].(string); reason != "" {
		return
	}
	if creates && o.param("header", "Idempotency-Key") == nil {
		t.Errorf("creating POST doesn't accept Idempotency-Key (or explain x-glossa-idempotency)")
	}
}

func checkConcurrency(t *testing.T, o operation) {
	if o.method != http.MethodPatch {
		return
	}
	p := o.param("header", "If-Match")
	if p == nil || !p.Required {
		t.Errorf("PATCH must require If-Match")
	}
	for _, code := range []string{"412", "428"} {
		if o.op.Responses.Value(code) == nil {
			t.Errorf("PATCH doesn't document %s", code)
		}
	}
	if ok := o.op.Responses.Value("200"); ok == nil || ok.Value.Headers["ETag"] == nil {
		t.Errorf("PATCH response lacks ETag")
	}
}

func TestSchemaPropertiesAreSnakeCase(t *testing.T) {
	doc := load(t)
	for name, ref := range doc.Components.Schemas {
		for prop := range ref.Value.Properties {
			if !snakeCase.MatchString(prop) {
				t.Errorf("schema %s: property %q is not snake_case", name, prop)
			}
		}
	}
}

func TestQueryParametersAreSnakeCase(t *testing.T) {
	doc := load(t)
	for _, o := range operations(doc) {
		for _, p := range o.params {
			if p.Value.In == "query" && !snakeCase.MatchString(p.Value.Name) {
				t.Errorf("%s: query parameter %q is not snake_case", o.name(), p.Value.Name)
			}
		}
	}
}

func TestTimestampsAreDateTime(t *testing.T) {
	doc := load(t)
	for name, ref := range doc.Components.Schemas {
		for prop, p := range ref.Value.Properties {
			if strings.HasSuffix(prop, "_at") && (p.Value.Format != "date-time") {
				t.Errorf("schema %s: %s must be a date-time (use #/components/schemas/Timestamp)", name, prop)
			}
		}
	}
}
