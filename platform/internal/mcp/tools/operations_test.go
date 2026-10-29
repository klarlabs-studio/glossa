package tools_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// fakeOps stands in for Workflow and Release's read sides. A project
// other than its own is not found, as the services answer for one
// outside the session's scope.
type fakeOps struct {
	project uuid.UUID
	seen    []string
}

func (f *fakeOps) guard(p uuid.UUID) error {
	if p != uuid.Nil && p != f.project {
		return tools.ErrNotFound
	}
	return nil
}

func (f *fakeOps) Assignments(_ context.Context, q tools.AssignmentQuery) ([]tools.Assignment, string, error) {
	f.seen = append(f.seen, "assignments")
	if err := f.guard(q.Project); err != nil {
		return nil, "", err
	}
	return []tools.Assignment{{ID: "a1", Assignee: "vendor:v", State: "open"}}, "", nil
}

func (f *fakeOps) State(_ context.Context, p uuid.UUID, _ tools.WorkflowStateQuery) (tools.WorkflowState, error) {
	if err := f.guard(p); err != nil {
		return tools.WorkflowState{}, err
	}
	return tools.WorkflowState{Bindings: []tools.Binding{{ID: "b1"}}, Instances: []tools.Instance{}}, nil
}

func (f *fakeOps) Report(_ context.Context, q tools.QualityQuery) (tools.QualityReport, error) {
	if err := f.guard(q.Project); err != nil {
		return tools.QualityReport{}, err
	}
	return tools.QualityReport{Rows: []tools.QualityRow{{Assignee: "vendor:v", Locale: "de", Units: 3}}}, nil
}

func (f *fakeOps) ReleaseRequests(_ context.Context, p uuid.UUID, _, _, _ string, _ int) ([]tools.ReleaseRequest, string, error) {
	if err := f.guard(p); err != nil {
		return nil, "", err
	}
	return []tools.ReleaseRequest{{ID: "r1", State: "pending"}}, "", nil
}

func (f *fakeOps) Rollouts(_ context.Context, p uuid.UUID, env, _ string, _ int) ([]tools.Rollout, string, error) {
	if err := f.guard(p); err != nil {
		return nil, "", err
	}
	return []tools.Rollout{{ID: "o1", Environment: env, Status: "active", Percent: 10, Candidate: "rel"}}, "", nil
}

var opsTools = []string{
	tools.AssignmentsListName, tools.AssignmentsReportName, tools.WorkflowStateName,
	tools.ReleaseRequestsListName, tools.RolloutsListName,
}

func opsWorld() (*world, *fakeOps) {
	w := seed()
	f := &fakeOps{project: w.project}
	w.sources.Workflow, w.sources.ReleaseReads = f, f
	return w, f
}

func TestOperationsToolsAreReadOnlyAndHaveNoDecisionTool(t *testing.T) {
	w, _ := opsWorld()
	got := tools.Operations(w.sources)
	var names []string
	for _, tl := range got {
		names = append(names, tl.Name)
		if tl.Toolset != domain.ToolsetRead || !tl.ReadOnly || tl.Permission == "" {
			t.Errorf("%s: toolset %s, read-only %v, permission %q", tl.Name, tl.Toolset, tl.ReadOnly, tl.Permission)
		}
	}
	if !slices.Equal(names, []string{
		tools.AssignmentsListName, tools.AssignmentsReportName, tools.WorkflowStateName,
		tools.ReleaseRequestsListName, tools.RolloutsListName,
	}) {
		t.Fatalf("operations tools = %v", names)
	}
	for _, tl := range tools.All(w.sources) {
		for _, bad := range []string{"approve", "deny", "decide", "approval"} {
			if strings.Contains(tl.Name, bad) {
				t.Errorf("%s decides an approval", tl.Name)
			}
		}
	}
	if len(tools.Operations(tools.Sources{})) != 0 {
		t.Error("tools without their ports are registered")
	}
}

func TestOperationsToolsAnswerAndHideOtherProjects(t *testing.T) {
	w, _ := opsWorld()
	svc, sess := session(t, w, nil)
	for _, name := range opsTools {
		t.Run(name, func(t *testing.T) {
			res, err := call(t, svc, sess, name, map[string]any{"project": w.project.String()})
			if err != nil || res.Explanation == "" {
				t.Fatalf("own project: %v / %q", err, res.Explanation)
			}
			_, err = call(t, svc, sess, name, map[string]any{"project": w.otherProject.String()})
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("another project: err = %v, want not found", err)
			}
		})
	}
}

func TestOperationsToolsRefuseBadArguments(t *testing.T) {
	w, _ := opsWorld()
	svc, sess := session(t, w, nil)
	p := w.project.String()
	for name, args := range map[string]map[string]any{
		tools.AssignmentsListName:     {"state": "bogus"},
		tools.AssignmentsReportName:   {"since": "yesterday"},
		tools.WorkflowStateName:       {"project": p, "status": "bogus"},
		tools.ReleaseRequestsListName: {"project": p, "state": "bogus"},
	} {
		_, err := call(t, svc, sess, name, args)
		var inv *app.InvalidArgumentError
		if !errors.As(err, &inv) {
			t.Errorf("%s: err = %v, want an invalid argument", name, err)
		}
	}
	// A key names a message, which needs a project.
	if _, err := call(t, svc, sess, tools.AssignmentsListName, map[string]any{"key": "k"}); err == nil {
		t.Error("assignments_list took a key without a project")
	}
}

func TestRolloutsListDefaultsToProductionAndSaysWhatRolls(t *testing.T) {
	w, _ := opsWorld()
	svc, sess := session(t, w, nil)
	res, err := call(t, svc, sess, tools.RolloutsListName, map[string]any{"project": w.project.String()})
	if err != nil || !strings.Contains(res.Explanation, "production is rolling release rel out to 10%") {
		t.Fatalf("explanation = %q, err = %v", res.Explanation, err)
	}
}
