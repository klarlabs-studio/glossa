package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/httpapi"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/identity"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/defaults"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// fixture is the API over an in-memory store, one tenant, two projects
// the catalog knows and one it does not.
type fixture struct {
	api       *httpapi.API
	mem       *memory
	q         *instances
	tenant    tenancy.ID
	project   uuid.UUID
	other     uuid.UUID
	message   uuid.UUID
	owner     context.Context
	reader    context.Context
	anonymous context.Context
	// Assignments and approvals.
	work    *workMemory
	dir     *directory
	authors authors
	vendor  uuid.UUID
}

// person is a signed-in member with roles in locales, registered in the
// directory as working for vendor (uuid.Nil: for nobody).
func (f *fixture) person(roles, locales []string, vendor uuid.UUID) context.Context {
	ctx := authztest.Member(context.Background(), f.tenant, roles, locales...)
	p, _ := authz.From(ctx)
	f.dir.members[p.Member.UUID()] = app.Affiliation{Member: p.Member.UUID(), Roles: roles, Vendor: vendor}
	return ctx
}

func newFixture(t *testing.T, withInstances bool) *fixture {
	t.Helper()
	f := &fixture{mem: newMemory(), tenant: tenancy.NewID(), project: uuid.New(), other: uuid.New(), message: uuid.New()}
	cat := catalog{projects: map[uuid.UUID]map[string]uuid.UUID{
		f.project: {"shared.welcome": f.message},
		f.other:   {},
	}}
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	svc := app.New(f.mem, identity.Permissions{}, app.WithCatalog(cat), app.WithClock(func() time.Time { return clock }))
	var q app.InstanceQueries
	if withInstances {
		f.q = &instances{transitions: map[uuid.UUID][]app.TransitionView{}}
		q = f.q
	}
	f.work, f.vendor, f.authors = newWorkMemory(), uuid.New(), authors{}
	f.dir = &directory{members: map[uuid.UUID]app.Affiliation{}, vendors: map[string]uuid.UUID{"lingua": f.vendor}}
	work := app.NewWorkService(f.work, f.dir, f.authors, app.WithWorkClock(func() time.Time { return clock }), app.WithWorkCatalog(cat))
	f.api = httpapi.New(svc, q, cat, work)
	f.owner = f.person([]string{"owner"}, nil, uuid.Nil)
	// A translator holds workflows.read and not workflows.manage.
	f.reader = authztest.Member(context.Background(), f.tenant, []string{"translator"}, "de")
	f.anonymous = tenancy.ContextWithTenant(context.Background(), f.tenant)
	return f
}

// response is a rendered answer: status, problem code, body.
type response struct {
	status int
	code   string
	header http.Header
	body   []byte
}

// render writes what the generated strict server would for a handler's
// result, and what Identity's error writer does for the errors it maps
// (authorization) — so a test reads one status and one code whatever
// path produced them.
func render(t *testing.T, resp any, err error) response {
	t.Helper()
	rec := httptest.NewRecorder()
	switch {
	case errors.Is(err, authz.ErrUnauthenticated):
		problem.WriteDetails(rec, problem.New(http.StatusUnauthorized, problem.CodeUnauthenticated, "authentication required"))
	case errors.Is(err, authz.ErrNotVisible):
		problem.WriteDetails(rec, problem.New(http.StatusNotFound, problem.CodeNotFound, "no such resource"))
	case errors.Is(err, authz.ErrForbidden):
		problem.WriteDetails(rec, problem.New(http.StatusForbidden, problem.CodeForbidden, err.Error()))
	case err != nil:
		var d *problem.Details
		if !errors.As(err, &d) {
			t.Fatalf("unmapped error: %v", err)
		}
		problem.WriteDetails(rec, d)
	default:
		v := reflect.ValueOf(resp)
		var visit reflect.Value
		for i := range v.NumMethod() {
			if strings.HasPrefix(v.Type().Method(i).Name, "Visit") {
				visit = v.Method(i)
			}
		}
		if !visit.IsValid() {
			t.Fatalf("%T is not a response object", resp)
		}
		if out := visit.Call([]reflect.Value{reflect.ValueOf(http.ResponseWriter(rec))}); !out[0].IsNil() {
			t.Fatalf("render %T: %v", resp, out[0].Interface())
		}
	}
	r := response{status: rec.Code, header: rec.Header(), body: rec.Body.Bytes()}
	var p struct{ Code string }
	if rec.Code >= 400 {
		_ = json.Unmarshal(r.body, &p)
		r.code = p.Code
	}
	return r
}

func (r response) want(t *testing.T, status int, code string) response {
	t.Helper()
	if r.status != status || r.code != code {
		t.Fatalf("got %d %s, want %d %s: %s", r.status, r.code, status, code, r.body)
	}
	return r
}

// decode replaces v with the body: what a field the body omits was
// before does not survive into it.
func (r response) decode(t *testing.T, v any) {
	t.Helper()
	reflect.ValueOf(v).Elem().SetZero()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

// doc is the default review definition as a request body, renamed.
func doc(t *testing.T, name string) *apiv1.WorkflowDocument {
	t.Helper()
	var d apiv1.WorkflowDocument
	if err := json.Unmarshal(defaults.Review(), &d); err != nil {
		t.Fatal(err)
	}
	d["name"] = name
	if c, ok := d["chart"].(map[string]any); ok {
		c["id"] = name
	}
	return &d
}

// unknownGuard is §12.1's first refusal: a guard the vocabulary does not
// have.
func unknownGuard(t *testing.T) *apiv1.WorkflowDocument {
	t.Helper()
	d := doc(t, "legal")
	// The default's initial state, whatever the default says, with an
	// unknown guard on its first transition: everything else valid, so
	// the one finding is the one under test.
	chart := (*d)["chart"].(map[string]any)
	st := chart["states"].(map[string]any)[chart["initial"].(string)].(map[string]any)
	first := st["transitions"].([]any)[0].(map[string]any)
	first["guard"] = "legal_signed_off"
	guards, _ := (*d)["guards"].(map[string]any)
	if guards == nil {
		guards = map[string]any{}
		(*d)["guards"] = guards
	}
	guards["legal_signed_off"] = map[string]any{"use": "legal_department_said_yes"}
	return d
}

func (f *fixture) create(t *testing.T, ctx context.Context, body *apiv1.WorkflowDocument, project *string) response {
	t.Helper()
	resp, err := f.api.CreateWorkflowDefinition(ctx, apiv1.CreateWorkflowDefinitionRequestObject{
		Tenant: f.tenant.String(), Params: apiv1.CreateWorkflowDefinitionParams{Project: project}, Body: body,
	})
	return render(t, resp, err)
}

func (f *fixture) createOK(t *testing.T, name string) apiv1.WorkflowDefinitionSaved {
	t.Helper()
	var saved apiv1.WorkflowDefinitionSaved
	f.create(t, f.owner, doc(t, name), nil).want(t, http.StatusCreated, "").decode(t, &saved)
	return saved
}

func eventTypes(es []outbox.Event) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Type
	}
	return out
}

func ptr[T any](v T) *T { return &v }

// ── definitions ─────────────────────────────────────────────────────

func TestCreateGetListDefinitions(t *testing.T) {
	f := newFixture(t, false)
	r := f.create(t, f.owner, doc(t, "review"), nil).want(t, http.StatusCreated, "")
	var saved apiv1.WorkflowDefinitionSaved
	r.decode(t, &saved)
	if saved.Version != 1 || saved.Name != "review" || saved.Subject != apiv1.WorkflowSubjectTranslation || saved.ProjectId != nil {
		t.Errorf("saved = %s", r.body)
	}
	if r.header.Get("ETag") != `"1"` || !strings.HasSuffix(r.header.Get("Location"), "/workflow-definitions/"+saved.Id) {
		t.Errorf("headers = %v", r.header)
	}

	// The save is announced, in its transaction, by whoever made it.
	es := f.mem.published()
	if len(es) != 1 || es[0].Type != domain.EventDefinitionSaved || es[0].AggregateID != saved.Id {
		t.Fatalf("events = %v", eventTypes(es))
	}
	if es[0].Actor.Kind() != outbox.ActorKindPerson || string(es[0].Actor) != saved.CreatedBy {
		t.Errorf("actor = %q, created_by = %q", es[0].Actor, saved.CreatedBy)
	}

	resp, err := f.api.GetWorkflowDefinition(f.reader, apiv1.GetWorkflowDefinitionRequestObject{WorkflowDefinition: saved.Id})
	g := render(t, resp, err).want(t, http.StatusOK, "")
	if g.header.Get("ETag") != `"1"` {
		t.Errorf("ETag = %q", g.header.Get("ETag"))
	}

	// A project's own definition is listed for that project only.
	f.create(t, f.owner, doc(t, "project-only"), ptr(f.project.String())).want(t, http.StatusCreated, "")
	list := func(project *string) []string {
		resp, err := f.api.ListWorkflowDefinitions(f.reader, apiv1.ListWorkflowDefinitionsRequestObject{
			Params: apiv1.ListWorkflowDefinitionsParams{Project: project}})
		var out apiv1.WorkflowDefinitionList
		render(t, resp, err).want(t, http.StatusOK, "").decode(t, &out)
		names := make([]string, len(out.Items))
		for i, d := range out.Items {
			names[i] = d.Name
		}
		return names
	}
	if got := list(ptr(f.other.String())); strings.Join(got, ",") != "review" {
		t.Errorf("other project's definitions = %v", got)
	}
	if got := list(ptr(f.project.String())); strings.Join(got, ",") != "project-only,review" {
		t.Errorf("project's definitions = %v", got)
	}

	// Pages follow the names.
	resp2, err := f.api.ListWorkflowDefinitions(f.reader, apiv1.ListWorkflowDefinitionsRequestObject{
		Params: apiv1.ListWorkflowDefinitionsParams{PageSize: ptr(1)}})
	var page apiv1.WorkflowDefinitionList
	render(t, resp2, err).want(t, http.StatusOK, "").decode(t, &page)
	if len(page.Items) != 1 || page.Items[0].Name != "project-only" || page.NextPageToken == nil {
		t.Fatalf("first page = %+v", page)
	}
	resp2, err = f.api.ListWorkflowDefinitions(f.reader, apiv1.ListWorkflowDefinitionsRequestObject{
		Params: apiv1.ListWorkflowDefinitionsParams{PageSize: ptr(1), PageToken: page.NextPageToken}})
	render(t, resp2, err).want(t, http.StatusOK, "").decode(t, &page)
	if len(page.Items) != 1 || page.Items[0].Name != "review" || page.NextPageToken != nil {
		t.Errorf("second page = %+v", page)
	}

	// Taken names, unknown projects and unknown ids.
	f.create(t, f.owner, doc(t, "review"), nil).want(t, http.StatusConflict, "workflow_definition_exists")
	f.create(t, f.owner, doc(t, "elsewhere"), ptr(uuid.NewString())).want(t, http.StatusNotFound, "not_found")
	f.create(t, f.owner, nil, nil).want(t, http.StatusBadRequest, "invalid_request")
	for _, id := range []string{uuid.NewString(), "not-an-id"} {
		resp, err := f.api.GetWorkflowDefinition(f.reader, apiv1.GetWorkflowDefinitionRequestObject{WorkflowDefinition: id})
		render(t, resp, err).want(t, http.StatusNotFound, "not_found")
	}
	resp3, err := f.api.ListWorkflowDefinitions(f.reader, apiv1.ListWorkflowDefinitionsRequestObject{
		Params: apiv1.ListWorkflowDefinitionsParams{Project: ptr(uuid.NewString())}})
	render(t, resp3, err).want(t, http.StatusNotFound, "not_found")
}

// TestInvalidDefinitionIs422WithEveryFinding: §12.1's refusals, through
// the handler — nothing stored, nothing announced, every finding in the
// problem.
func TestInvalidDefinitionIs422WithEveryFinding(t *testing.T) {
	f := newFixture(t, false)
	r := f.create(t, f.owner, unknownGuard(t), nil).want(t, http.StatusUnprocessableEntity, "invalid_workflow")
	if r.header.Get("Content-Type") != problem.ContentType {
		t.Errorf("Content-Type = %q", r.header.Get("Content-Type"))
	}
	var p apiv1.WorkflowProblem
	r.decode(t, &p)
	if p.Findings == nil || len(*p.Findings) == 0 {
		t.Fatalf("422 without findings: %s", r.body)
	}
	found := false
	for _, fd := range *p.Findings {
		if fd.Rule == "" || fd.Severity == "" || fd.Message == "" {
			t.Errorf("incomplete finding %+v", fd)
		}
		if fd.Rule == domain.RuleUnknownPrimitive && fd.Path != nil && strings.Contains(*fd.Path, "legal_signed_off") {
			found = true
		}
	}
	if !found {
		t.Errorf("no unknown-primitive finding at guards.legal_signed_off: %s", r.body)
	}
	if es := f.mem.published(); len(es) != 0 {
		t.Errorf("a refused save published %v", eventTypes(es))
	}
	resp, err := f.api.ListWorkflowDefinitions(f.owner, apiv1.ListWorkflowDefinitionsRequestObject{})
	var list apiv1.WorkflowDefinitionList
	render(t, resp, err).want(t, http.StatusOK, "").decode(t, &list)
	if len(list.Items) != 0 {
		t.Errorf("a refused definition was stored: %+v", list.Items)
	}
}

func TestVersions(t *testing.T) {
	f := newFixture(t, false)
	saved := f.createOK(t, "review")
	save := func(ctx context.Context, ifMatch string, body *apiv1.WorkflowDocument) response {
		resp, err := f.api.SaveWorkflowDefinitionVersion(ctx, apiv1.SaveWorkflowDefinitionVersionRequestObject{
			WorkflowDefinition: saved.Id, Params: apiv1.SaveWorkflowDefinitionVersionParams{IfMatch: ifMatch}, Body: body})
		return render(t, resp, err)
	}
	r := save(f.owner, `"1"`, doc(t, "review")).want(t, http.StatusCreated, "")
	var v2 apiv1.WorkflowDefinitionSaved
	r.decode(t, &v2)
	if v2.Version != 2 || r.header.Get("ETag") != `"2"` || !strings.HasSuffix(r.header.Get("Location"), "/versions/2") {
		t.Errorf("v2 = %s %v", r.body, r.header)
	}
	if got := eventTypes(f.mem.published()); strings.Join(got, ",") != "workflow.definition_saved,workflow.definition_saved" {
		t.Errorf("events = %v", got)
	}

	// The author edited version 1; version 2 has landed since.
	save(f.owner, `"1"`, doc(t, "review")).want(t, http.StatusPreconditionFailed, "precondition_failed")
	save(f.owner, `nonsense`, doc(t, "review")).want(t, http.StatusPreconditionFailed, "precondition_failed")
	// A new name is a new definition, and an invalid document is refused
	// whatever its ETag.
	save(f.owner, `"2"`, doc(t, "renamed")).want(t, http.StatusUnprocessableEntity, "invalid_workflow")
	save(f.owner, `"2"`, unknownGuard(t)).want(t, http.StatusUnprocessableEntity, "invalid_workflow")
	save(f.reader, `"2"`, doc(t, "review")).want(t, http.StatusForbidden, "forbidden")
	save(f.anonymous, `"2"`, doc(t, "review")).want(t, http.StatusUnauthorized, "unauthenticated")

	resp, err := f.api.ListWorkflowDefinitionVersions(f.reader, apiv1.ListWorkflowDefinitionVersionsRequestObject{
		WorkflowDefinition: saved.Id, Params: apiv1.ListWorkflowDefinitionVersionsParams{PageSize: ptr(1)}})
	var page apiv1.WorkflowDefinitionVersionList
	render(t, resp, err).want(t, http.StatusOK, "").decode(t, &page)
	if len(page.Items) != 1 || page.Items[0].Version != 2 || page.NextPageToken == nil {
		t.Fatalf("newest first = %+v", page)
	}
	if page.Items[0].Document["name"] != "review" || page.Items[0].Schema != domain.SchemaV1 {
		t.Errorf("document = %+v", page.Items[0])
	}
	resp, err = f.api.ListWorkflowDefinitionVersions(f.reader, apiv1.ListWorkflowDefinitionVersionsRequestObject{
		WorkflowDefinition: saved.Id, Params: apiv1.ListWorkflowDefinitionVersionsParams{PageSize: ptr(1), PageToken: page.NextPageToken}})
	render(t, resp, err).want(t, http.StatusOK, "").decode(t, &page)
	if len(page.Items) != 1 || page.Items[0].Version != 1 || page.NextPageToken != nil {
		t.Errorf("second page = %+v", page)
	}

	get := func(n int) response {
		resp, err := f.api.GetWorkflowDefinitionVersion(f.reader, apiv1.GetWorkflowDefinitionVersionRequestObject{
			WorkflowDefinition: saved.Id, Version: n})
		return render(t, resp, err)
	}
	var v1 apiv1.WorkflowDefinitionVersion
	get(1).want(t, http.StatusOK, "").decode(t, &v1)
	if v1.Version != 1 || v1.DefinitionId != saved.Id {
		t.Errorf("v1 = %+v", v1)
	}
	get(9).want(t, http.StatusNotFound, "not_found")
	get(0).want(t, http.StatusNotFound, "not_found")

	resp4, err := f.api.SaveWorkflowDefinitionVersion(f.owner, apiv1.SaveWorkflowDefinitionVersionRequestObject{
		WorkflowDefinition: uuid.NewString(), Params: apiv1.SaveWorkflowDefinitionVersionParams{IfMatch: `"1"`}, Body: doc(t, "review")})
	render(t, resp4, err).want(t, http.StatusNotFound, "not_found")
}

func TestDeleteDefinition(t *testing.T) {
	f := newFixture(t, false)
	saved := f.createOK(t, "review")
	bind := func() {
		resp, err := f.api.CreateWorkflowBinding(f.owner, apiv1.CreateWorkflowBindingRequestObject{
			Project: f.project.String(), Body: &apiv1.CreateWorkflowBinding{DefinitionId: saved.Id}})
		render(t, resp, err).want(t, http.StatusCreated, "")
	}
	bind()
	del := func(ctx context.Context) response {
		resp, err := f.api.DeleteWorkflowDefinition(ctx, apiv1.DeleteWorkflowDefinitionRequestObject{WorkflowDefinition: saved.Id})
		return render(t, resp, err)
	}
	del(f.reader).want(t, http.StatusForbidden, "forbidden")
	del(f.anonymous).want(t, http.StatusUnauthorized, "unauthenticated")
	del(f.owner).want(t, http.StatusNoContent, "")
	del(f.owner).want(t, http.StatusNotFound, "not_found")

	es := f.mem.published()
	last := es[len(es)-1]
	if last.Type != domain.EventDefinitionDeleted || last.Actor.Kind() != outbox.ActorKindPerson {
		t.Fatalf("events = %v", eventTypes(es))
	}
	var payload domain.DefinitionDeleted
	b, _ := json.Marshal(last.Payload)
	_ = json.Unmarshal(b, &payload)
	if payload.BindingsRemoved != 1 {
		t.Errorf("payload = %+v", payload)
	}
	// Still readable, as deleted; its binding went with it.
	resp, err := f.api.GetWorkflowDefinition(f.reader, apiv1.GetWorkflowDefinitionRequestObject{WorkflowDefinition: saved.Id})
	var d apiv1.WorkflowDefinition
	render(t, resp, err).want(t, http.StatusOK, "").decode(t, &d)
	if d.DeletedAt == nil {
		t.Errorf("deleted definition = %+v", d)
	}
	resp2, err := f.api.ListWorkflowBindings(f.reader, apiv1.ListWorkflowBindingsRequestObject{Project: f.project.String()})
	var bs apiv1.WorkflowBindingList
	render(t, resp2, err).want(t, http.StatusOK, "").decode(t, &bs)
	if len(bs.Items) != 0 {
		t.Errorf("bindings after delete = %+v", bs.Items)
	}
}

func TestLint(t *testing.T) {
	f := newFixture(t, false)
	lint := func(ctx context.Context, body *apiv1.WorkflowDocument) response {
		resp, err := f.api.LintWorkflowDefinition(ctx, apiv1.LintWorkflowDefinitionRequestObject{Body: body})
		return render(t, resp, err)
	}
	var ok apiv1.WorkflowLintResult
	lint(f.reader, doc(t, "review")).want(t, http.StatusOK, "").decode(t, &ok)
	if !ok.Valid {
		t.Errorf("the default definition lints invalid: %+v", ok)
	}
	var bad apiv1.WorkflowLintResult
	lint(f.reader, unknownGuard(t)).want(t, http.StatusOK, "").decode(t, &bad)
	if bad.Valid || len(bad.Findings) == 0 {
		t.Errorf("unknown guard lints as %+v", bad)
	}
	lint(f.anonymous, doc(t, "review")).want(t, http.StatusUnauthorized, "unauthenticated")
	lint(f.reader, nil).want(t, http.StatusBadRequest, "invalid_request")
	if es := f.mem.published(); len(es) != 0 {
		t.Errorf("lint published %v", eventTypes(es))
	}
}

// ── bindings ────────────────────────────────────────────────────────

func TestBindingsAndResolution(t *testing.T) {
	f := newFixture(t, false)
	general := f.createOK(t, "review")
	german := f.createOK(t, "german")
	bind := func(ctx context.Context, project string, body *apiv1.CreateWorkflowBinding) response {
		resp, err := f.api.CreateWorkflowBinding(ctx, apiv1.CreateWorkflowBindingRequestObject{Project: project, Body: body})
		return render(t, resp, err)
	}
	p := f.project.String()
	r := bind(f.owner, p, &apiv1.CreateWorkflowBinding{DefinitionId: general.Id}).want(t, http.StatusCreated, "")
	var b1 apiv1.WorkflowBinding
	r.decode(t, &b1)
	if !strings.HasSuffix(r.header.Get("Location"), "/projects/"+p+"/workflow-bindings/"+b1.Id) || len(b1.Locales) != 0 {
		t.Errorf("binding = %s %v", r.body, r.header)
	}
	var b2 apiv1.WorkflowBinding
	bind(f.owner, p, &apiv1.CreateWorkflowBinding{DefinitionId: german.Id, Locales: &[]string{"DE"}}).
		want(t, http.StatusCreated, "").decode(t, &b2)
	if strings.Join(b2.Locales, ",") != "de" {
		t.Errorf("locales are not canonical: %v", b2.Locales)
	}
	es := f.mem.published()
	last := es[len(es)-1]
	if last.Type != domain.EventBindingChanged || last.AggregateID != b2.Id || last.Actor.Kind() != outbox.ActorKindPerson {
		t.Errorf("events = %v", eventTypes(es))
	}

	bind(f.owner, p, &apiv1.CreateWorkflowBinding{DefinitionId: general.Id}).want(t, http.StatusConflict, "workflow_binding_exists")
	bind(f.owner, p, &apiv1.CreateWorkflowBinding{DefinitionId: general.Id, Locales: &[]string{"not a locale"}}).
		want(t, http.StatusUnprocessableEntity, "invalid_workflow_binding")
	bind(f.owner, p, &apiv1.CreateWorkflowBinding{DefinitionId: uuid.NewString()}).want(t, http.StatusNotFound, "not_found")
	bind(f.owner, uuid.NewString(), &apiv1.CreateWorkflowBinding{DefinitionId: general.Id}).want(t, http.StatusNotFound, "not_found")
	bind(f.reader, p, &apiv1.CreateWorkflowBinding{DefinitionId: general.Id, Namespace: ptr("checkout")}).want(t, http.StatusForbidden, "forbidden")
	bind(f.anonymous, p, &apiv1.CreateWorkflowBinding{DefinitionId: general.Id}).want(t, http.StatusUnauthorized, "unauthenticated")
	// A project's own definition binds only there.
	var own apiv1.WorkflowDefinitionSaved
	f.create(t, f.owner, doc(t, "own"), ptr(p)).want(t, http.StatusCreated, "").decode(t, &own)
	bind(f.owner, f.other.String(), &apiv1.CreateWorkflowBinding{DefinitionId: own.Id}).
		want(t, http.StatusUnprocessableEntity, "workflow_definition_out_of_scope")

	resp, err := f.api.ListWorkflowBindings(f.reader, apiv1.ListWorkflowBindingsRequestObject{Project: p})
	var list apiv1.WorkflowBindingList
	render(t, resp, err).want(t, http.StatusOK, "").decode(t, &list)
	if len(list.Items) != 2 || list.Items[0].Id != b1.Id || list.Items[1].Id != b2.Id {
		t.Errorf("bindings = %s", resp)
	}
	resp, err = f.api.ListWorkflowBindings(f.reader, apiv1.ListWorkflowBindingsRequestObject{Project: uuid.NewString()})
	render(t, resp, err).want(t, http.StatusNotFound, "not_found")

	resolve := func(project string, params apiv1.ResolveWorkflowParams) response {
		resp, err := f.api.ResolveWorkflow(f.reader, apiv1.ResolveWorkflowRequestObject{Project: project, Params: params})
		return render(t, resp, err)
	}
	var res apiv1.WorkflowResolution
	resolve(p, apiv1.ResolveWorkflowParams{Locale: ptr("de")}).want(t, http.StatusOK, "").decode(t, &res)
	if !res.Bound || res.Binding == nil || res.Binding.Id != b2.Id || *res.DefinitionName != "german" || *res.Version != 1 {
		t.Errorf("de resolves to %+v", res)
	}
	resolve(p, apiv1.ResolveWorkflowParams{Locale: ptr("fr")}).want(t, http.StatusOK, "").decode(t, &res)
	if !res.Bound || res.Binding.Id != b1.Id || *res.DefinitionName != "review" {
		t.Errorf("fr resolves to %+v", res)
	}
	var none apiv1.WorkflowResolution
	resolve(p, apiv1.ResolveWorkflowParams{Subject: ptr(apiv1.WorkflowSubjectReleaseRequest)}).want(t, http.StatusOK, "").decode(t, &none)
	if none.Bound || none.Binding != nil {
		t.Errorf("a release request resolves to %+v", none)
	}
	resolve(p, apiv1.ResolveWorkflowParams{Locale: ptr("not a locale")}).want(t, http.StatusBadRequest, "invalid_query")
	resolve(uuid.NewString(), apiv1.ResolveWorkflowParams{}).want(t, http.StatusNotFound, "not_found")

	unbind := func(ctx context.Context, project, id string) response {
		resp, err := f.api.DeleteWorkflowBinding(ctx, apiv1.DeleteWorkflowBindingRequestObject{Project: project, WorkflowBinding: id})
		return render(t, resp, err)
	}
	// The project is part of a binding's address.
	unbind(f.owner, f.other.String(), b2.Id).want(t, http.StatusNotFound, "not_found")
	unbind(f.reader, p, b2.Id).want(t, http.StatusForbidden, "forbidden")
	unbind(f.anonymous, p, b2.Id).want(t, http.StatusUnauthorized, "unauthenticated")
	unbind(f.owner, p, b2.Id).want(t, http.StatusNoContent, "")
	unbind(f.owner, p, b2.Id).want(t, http.StatusNotFound, "not_found")
	es = f.mem.published()
	var changed domain.BindingChanged
	raw, _ := json.Marshal(es[len(es)-1].Payload)
	_ = json.Unmarshal(raw, &changed)
	if changed.Change != domain.BindingDeleted || changed.BindingID != b2.Id {
		t.Errorf("unbind published %+v", changed)
	}
	resolve(p, apiv1.ResolveWorkflowParams{Locale: ptr("de")}).want(t, http.StatusOK, "").decode(t, &res)
	if res.Binding.Id != b1.Id {
		t.Errorf("de after unbinding resolves to %+v", res)
	}
}

// ── instances ───────────────────────────────────────────────────────

func TestInstancesUnavailableWithoutAStore(t *testing.T) {
	f := newFixture(t, false)
	p := f.project.String()
	resp, err := f.api.ListWorkflowInstances(f.reader, apiv1.ListWorkflowInstancesRequestObject{Project: p})
	render(t, resp, err).want(t, http.StatusServiceUnavailable, "workflow_instances_unavailable")
	resp2, err := f.api.GetWorkflowInstance(f.reader, apiv1.GetWorkflowInstanceRequestObject{Project: p, WorkflowInstance: uuid.NewString()})
	render(t, resp2, err).want(t, http.StatusServiceUnavailable, "workflow_instances_unavailable")
	resp3, err := f.api.ListWorkflowTransitions(f.reader, apiv1.ListWorkflowTransitionsRequestObject{Project: p, WorkflowInstance: uuid.NewString()})
	render(t, resp3, err).want(t, http.StatusServiceUnavailable, "workflow_instances_unavailable")
	// Who may ask is still checked first.
	resp, err = f.api.ListWorkflowInstances(f.anonymous, apiv1.ListWorkflowInstancesRequestObject{Project: p})
	render(t, resp, err).want(t, http.StatusUnauthorized, "unauthenticated")
}

func TestInstancesAndTransitions(t *testing.T) {
	f := newFixture(t, true)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	mine := app.InstanceView{ID: uuid.New(), Project: f.project, Definition: uuid.New(), Version: 2, Kind: domain.SubjectTranslation,
		SubjectID: f.message, Locale: "de", State: "reviewed", Status: app.InstanceFinished, CreatedAt: at, UpdatedAt: at}
	theirs := app.InstanceView{ID: uuid.New(), Project: f.other, Definition: uuid.New(), Version: 1, Kind: domain.SubjectTranslation,
		SubjectID: uuid.New(), State: "awaiting_review", Status: app.InstanceActive, CreatedAt: at, UpdatedAt: at}
	f.q.items = []app.InstanceView{mine, theirs}
	f.q.next = "cursor-from-the-store"
	event := uuid.New()
	f.q.transitions[mine.ID] = []app.TransitionView{
		{Seq: 2, From: "awaiting_review", Event: "translation.reviewed", To: "reviewed", Outcome: app.TransitionApplied,
			Guards: []app.GuardOutcome{{Guard: "two_approvals", Passed: true}}, Actions: []app.ActionOutcome{{Action: "approve", Outcome: "done"}},
			Actor: "person:0190f5a4-3c1e-7b2a-9d4e-1f2a3b4c5d6e", OutboxEventID: event, At: at},
		{Seq: 1, From: "", Event: "translation.revised", To: "awaiting_review", Outcome: app.TransitionApplied,
			Actor: "person:0190f5a4-3c1e-7b2a-9d4e-1f2a3b4c5d6e", At: at},
	}
	p := f.project.String()
	list := func(ctx context.Context, params apiv1.ListWorkflowInstancesParams) response {
		resp, err := f.api.ListWorkflowInstances(ctx, apiv1.ListWorkflowInstancesRequestObject{Project: p, Params: params})
		return render(t, resp, err)
	}
	var page apiv1.WorkflowInstanceList
	list(f.reader, apiv1.ListWorkflowInstancesParams{Message: ptr("shared.welcome"), Locale: ptr("DE"),
		Status: ptr(apiv1.WorkflowInstanceStatusFinished), PageSize: ptr(10)}).want(t, http.StatusOK, "").decode(t, &page)
	if len(page.Items) != 1 || page.Items[0].Id != mine.ID.String() || page.Items[0].DefinitionVersion != 2 ||
		page.Items[0].Status != apiv1.WorkflowInstanceStatusFinished || *page.Items[0].Locale != "de" {
		t.Fatalf("instances = %+v", page)
	}
	if got := f.q.last; got.Project != f.project || got.SubjectID != f.message || got.Locale != "de" ||
		got.Status != app.InstanceFinished || got.Limit != 10 {
		t.Errorf("filter = %+v", got)
	}
	// The store's cursor travels as an ordinary page token and comes back
	// as the store wrote it.
	if page.NextPageToken == nil {
		t.Fatal("no next_page_token")
	}
	list(f.reader, apiv1.ListWorkflowInstancesParams{PageToken: page.NextPageToken}).want(t, http.StatusOK, "")
	if f.q.last.After != "cursor-from-the-store" {
		t.Errorf("after = %q", f.q.last.After)
	}
	// A key the project does not have matches nothing; it is not an error.
	list(f.reader, apiv1.ListWorkflowInstancesParams{Message: ptr("no.such.key")}).want(t, http.StatusOK, "").decode(t, &page)
	if len(page.Items) != 0 {
		t.Errorf("unknown key = %+v", page.Items)
	}
	list(f.reader, apiv1.ListWorkflowInstancesParams{Message: ptr("shared.welcome"), SubjectId: ptr(uuid.NewString())}).
		want(t, http.StatusBadRequest, "invalid_query")
	list(f.reader, apiv1.ListWorkflowInstancesParams{Status: ptr(apiv1.WorkflowInstanceStatus("stuck"))}).want(t, http.StatusBadRequest, "invalid_query")
	list(f.reader, apiv1.ListWorkflowInstancesParams{Definition: ptr("nope")}).want(t, http.StatusBadRequest, "invalid_query")
	list(f.reader, apiv1.ListWorkflowInstancesParams{PageSize: ptr(0)}).want(t, http.StatusBadRequest, "invalid_page_size")
	list(f.anonymous, apiv1.ListWorkflowInstancesParams{}).want(t, http.StatusUnauthorized, "unauthenticated")
	resp, err := f.api.ListWorkflowInstances(f.reader, apiv1.ListWorkflowInstancesRequestObject{Project: uuid.NewString()})
	render(t, resp, err).want(t, http.StatusNotFound, "not_found")

	get := func(project, id string) response {
		resp, err := f.api.GetWorkflowInstance(f.reader, apiv1.GetWorkflowInstanceRequestObject{Project: project, WorkflowInstance: id})
		return render(t, resp, err)
	}
	var inst apiv1.WorkflowInstance
	get(p, mine.ID.String()).want(t, http.StatusOK, "").decode(t, &inst)
	if inst.SubjectId != f.message.String() || inst.Subject != apiv1.WorkflowSubjectTranslation {
		t.Errorf("instance = %+v", inst)
	}
	// Another project's instance is not found under this one.
	get(p, theirs.ID.String()).want(t, http.StatusNotFound, "not_found")
	get(p, uuid.NewString()).want(t, http.StatusNotFound, "not_found")
	get(p, "nope").want(t, http.StatusNotFound, "not_found")

	transitions := func(project, id string, params apiv1.ListWorkflowTransitionsParams) response {
		resp, err := f.api.ListWorkflowTransitions(f.reader, apiv1.ListWorkflowTransitionsRequestObject{
			Project: project, WorkflowInstance: id, Params: params})
		return render(t, resp, err)
	}
	var ts apiv1.WorkflowTransitionList
	transitions(p, mine.ID.String(), apiv1.ListWorkflowTransitionsParams{}).want(t, http.StatusOK, "").decode(t, &ts)
	if len(ts.Items) != 2 || ts.Items[0].Seq != 1 || ts.Items[1].Seq != 2 {
		t.Fatalf("transitions are not oldest first: %+v", ts.Items)
	}
	tr := ts.Items[1]
	if tr.Actor != "person:0190f5a4-3c1e-7b2a-9d4e-1f2a3b4c5d6e" || len(tr.Actions) != 1 || tr.Actions[0].Name != "approve" ||
		len(tr.Guards) != 1 || !tr.Guards[0].Passed || tr.OutboxEventId == nil || *tr.OutboxEventId != event.String() {
		t.Errorf("transition = %+v", tr)
	}
	transitions(p, mine.ID.String(), apiv1.ListWorkflowTransitionsParams{PageSize: ptr(1)}).want(t, http.StatusOK, "").decode(t, &ts)
	if len(ts.Items) != 1 || ts.NextPageToken == nil {
		t.Fatalf("first page = %+v", ts)
	}
	transitions(p, mine.ID.String(), apiv1.ListWorkflowTransitionsParams{PageSize: ptr(1), PageToken: ts.NextPageToken}).
		want(t, http.StatusOK, "").decode(t, &ts)
	if len(ts.Items) != 1 || ts.Items[0].Seq != 2 || ts.NextPageToken != nil {
		t.Errorf("second page = %+v", ts)
	}
	transitions(p, theirs.ID.String(), apiv1.ListWorkflowTransitionsParams{}).want(t, http.StatusNotFound, "not_found")
}

// TestEveryWriteNeedsManageAndEveryReadNeedsAPrincipal sweeps the
// routes: a translator (workflows.read only) reads and is refused every
// write; nobody reads anything.
func TestEveryWriteNeedsManageAndEveryReadNeedsAPrincipal(t *testing.T) {
	f := newFixture(t, true)
	saved := f.createOK(t, "review")
	p := f.project.String()
	reads := map[string]func(ctx context.Context) (any, error){
		"listWorkflowDefinitions": func(ctx context.Context) (any, error) {
			return f.api.ListWorkflowDefinitions(ctx, apiv1.ListWorkflowDefinitionsRequestObject{})
		},
		"getWorkflowDefinition": func(ctx context.Context) (any, error) {
			return f.api.GetWorkflowDefinition(ctx, apiv1.GetWorkflowDefinitionRequestObject{WorkflowDefinition: saved.Id})
		},
		"listWorkflowDefinitionVersions": func(ctx context.Context) (any, error) {
			return f.api.ListWorkflowDefinitionVersions(ctx, apiv1.ListWorkflowDefinitionVersionsRequestObject{WorkflowDefinition: saved.Id})
		},
		"getWorkflowDefinitionVersion": func(ctx context.Context) (any, error) {
			return f.api.GetWorkflowDefinitionVersion(ctx, apiv1.GetWorkflowDefinitionVersionRequestObject{WorkflowDefinition: saved.Id, Version: 1})
		},
		"lintWorkflowDefinition": func(ctx context.Context) (any, error) {
			return f.api.LintWorkflowDefinition(ctx, apiv1.LintWorkflowDefinitionRequestObject{Body: doc(t, "review")})
		},
		"listWorkflowBindings": func(ctx context.Context) (any, error) {
			return f.api.ListWorkflowBindings(ctx, apiv1.ListWorkflowBindingsRequestObject{Project: p})
		},
		"resolveWorkflow": func(ctx context.Context) (any, error) {
			return f.api.ResolveWorkflow(ctx, apiv1.ResolveWorkflowRequestObject{Project: p})
		},
		"listWorkflowInstances": func(ctx context.Context) (any, error) {
			return f.api.ListWorkflowInstances(ctx, apiv1.ListWorkflowInstancesRequestObject{Project: p})
		},
	}
	for name, call := range reads {
		resp, err := call(f.reader)
		if r := render(t, resp, err); r.status != http.StatusOK {
			t.Errorf("%s as a translator: %d %s", name, r.status, r.body)
		}
		resp, err = call(f.anonymous)
		if r := render(t, resp, err); r.status != http.StatusUnauthorized {
			t.Errorf("%s without a principal: %d", name, r.status)
		}
	}
	writes := map[string]func(ctx context.Context) (any, error){
		"createWorkflowDefinition": func(ctx context.Context) (any, error) {
			return f.api.CreateWorkflowDefinition(ctx, apiv1.CreateWorkflowDefinitionRequestObject{Body: doc(t, "another")})
		},
		"saveWorkflowDefinitionVersion": func(ctx context.Context) (any, error) {
			return f.api.SaveWorkflowDefinitionVersion(ctx, apiv1.SaveWorkflowDefinitionVersionRequestObject{
				WorkflowDefinition: saved.Id, Params: apiv1.SaveWorkflowDefinitionVersionParams{IfMatch: `"1"`}, Body: doc(t, "review")})
		},
		"deleteWorkflowDefinition": func(ctx context.Context) (any, error) {
			return f.api.DeleteWorkflowDefinition(ctx, apiv1.DeleteWorkflowDefinitionRequestObject{WorkflowDefinition: saved.Id})
		},
		"createWorkflowBinding": func(ctx context.Context) (any, error) {
			return f.api.CreateWorkflowBinding(ctx, apiv1.CreateWorkflowBindingRequestObject{
				Project: p, Body: &apiv1.CreateWorkflowBinding{DefinitionId: saved.Id}})
		},
		"deleteWorkflowBinding": func(ctx context.Context) (any, error) {
			return f.api.DeleteWorkflowBinding(ctx, apiv1.DeleteWorkflowBindingRequestObject{Project: p, WorkflowBinding: uuid.NewString()})
		},
	}
	for name, call := range writes {
		resp, err := call(f.reader)
		if r := render(t, resp, err); r.status != http.StatusForbidden {
			t.Errorf("%s as a translator: %d %s", name, r.status, r.body)
		}
		resp, err = call(f.anonymous)
		if r := render(t, resp, err); r.status != http.StatusUnauthorized {
			t.Errorf("%s without a principal: %d", name, r.status)
		}
	}
	if es := f.mem.published(); len(es) != 1 {
		t.Errorf("refused writes published: %v", eventTypes(es))
	}
}
