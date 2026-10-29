package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// rebaser stands in for the instance runner: what it was asked, and
// what it answers. The rebase itself — permission, If-Match, the state
// mapping — is the runner's, tested in workflow/app.
type rebaser struct {
	got  app.RebaseInput
	view app.InstanceView
	err  error
}

func (r *rebaser) Rebase(_ context.Context, in app.RebaseInput) (app.InstanceView, error) {
	r.got = in
	return r.view, r.err
}

func TestRebaseWorkflowInstance(t *testing.T) {
	f := newFixture(t, true)
	p, id := f.project.String(), uuid.New()
	rebase := func(api interface {
		RebaseWorkflowInstance(context.Context, apiv1.RebaseWorkflowInstanceRequestObject) (apiv1.RebaseWorkflowInstanceResponseObject, error)
	}, ifMatch string, body *apiv1.RebaseWorkflowInstance) response {
		resp, err := api.RebaseWorkflowInstance(f.owner, apiv1.RebaseWorkflowInstanceRequestObject{Project: p, WorkflowInstance: id.String(),
			Params: apiv1.RebaseWorkflowInstanceParams{IfMatch: ifMatch}, Body: body})
		return render(t, resp, err)
	}

	// A server without the runner says so.
	rebase(f.api, `"1"`, nil).want(t, http.StatusServiceUnavailable, "workflow_instances_unavailable")

	at := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	r := &rebaser{view: app.InstanceView{ID: id, Project: f.project, Definition: uuid.New(), Version: 3,
		Kind: domain.SubjectTranslation, SubjectID: f.message, Locale: "de", State: "reviewing", Status: app.InstanceActive,
		CreatedAt: at, UpdatedAt: at}}
	api := f.api.WithRebase(r)

	var inst apiv1.WorkflowInstance
	res := rebase(api, `"2"`, &apiv1.RebaseWorkflowInstance{Version: ptr(3)}).want(t, http.StatusOK, "")
	res.decode(t, &inst)
	if inst.DefinitionVersion != 3 || inst.State != "reviewing" || res.header.Get("ETag") != `"3"` {
		t.Errorf("answered %+v, ETag %q", inst, res.header.Get("ETag"))
	}
	if r.got != (app.RebaseInput{Project: f.project, Instance: id, IfVersion: 2, Version: 3}) {
		t.Errorf("asked %+v", r.got)
	}
	// No body: the definition's latest.
	rebase(api, `"2"`, nil).want(t, http.StatusOK, "")
	if r.got.Version != 0 {
		t.Errorf("without a body the runner was asked for version %d, not the latest", r.got.Version)
	}
	rebase(api, `nope`, nil).want(t, http.StatusPreconditionFailed, "precondition_failed")

	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: runs on 3", app.ErrConflict), http.StatusPreconditionFailed, "precondition_failed"},
		{domain.ErrInstanceFinished, http.StatusConflict, "workflow_instance_finished"},
		{domain.ErrRebaseNotNewer, http.StatusUnprocessableEntity, "invalid_workflow_rebase"},
		{app.ErrRebaseVersion, http.StatusUnprocessableEntity, "invalid_workflow_rebase"},
		{domain.ErrRebaseStateMissing, http.StatusUnprocessableEntity, "workflow_rebase_state_missing"},
		{domain.ErrRebaseStateFinal, http.StatusUnprocessableEntity, "workflow_rebase_state_final"},
		{app.ErrNotFound, http.StatusNotFound, "not_found"},
		{authz.ErrForbidden, http.StatusForbidden, "forbidden"},
	} {
		r.err = c.err
		rebase(api, `"2"`, nil).want(t, c.status, c.code)
	}

	// The instance's ETag is the version a rebase names.
	f.q.items = []app.InstanceView{r.view}
	resp, err := f.api.GetWorkflowInstance(f.reader, apiv1.GetWorkflowInstanceRequestObject{Project: p, WorkflowInstance: id.String()})
	if got := render(t, resp, err).want(t, http.StatusOK, "").header.Get("ETag"); got != `"3"` {
		t.Errorf("GET's ETag = %q", got)
	}
}
