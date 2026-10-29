package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// A token's project scope reaches every tool, because every tool calls
// an application service that checks the project itself (RFC 0006
// §4.1) — and a project outside the scope answers exactly as one that
// does not exist: not found, an invalid call, never forbidden-with-
// detail. The probe tool below stands for any of them: it checks the
// project as a use case does, through authz.
func TestAProjectOutsideTheTokensScopeIsNotFound(t *testing.T) {
	tenant := tenancy.NewID()
	inScope, outOfScope := uuid.New(), uuid.New()
	caller := callerWith(t, tenant, "read")
	scope, err := identity.ParseProjectScope([]string{inScope.String()})
	if err != nil {
		t.Fatal(err)
	}
	caller.Principal.Projects = scope
	probe := app.Tool{
		Name: "project_probe", Toolset: domain.ToolsetRead, Permission: identity.PermCatalogRead,
		Handler: func(ctx context.Context, _ app.Session, args json.RawMessage) (app.Result, error) {
			var in struct {
				Project uuid.UUID `json:"project"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return app.Result{}, err
			}
			if err := authz.RequireIn(ctx, authz.CatalogRead, in.Project); err != nil {
				return app.Result{}, err
			}
			return app.Result{Explanation: "ok"}, nil
		},
	}
	audit, metrics := &recordingAudit{}, &recordingMetrics{}
	svc := newService(t, caller, audit, metrics, app.WithTools(probe))
	sess := open(t, svc, caller, domain.ToolsetRead)

	call := func(p uuid.UUID) error {
		args, _ := json.Marshal(map[string]uuid.UUID{"project": p})
		_, err := svc.Call(t.Context(), sess, "project_probe", args)
		return err
	}
	if err := call(inScope); err != nil {
		t.Errorf("in scope: %v", err)
	}
	err = call(outOfScope)
	if !errors.Is(err, domain.ErrNotFound) || errors.Is(err, authz.ErrForbidden) {
		t.Errorf("out of scope: %v, want MCP's not-found", err)
	}
}
