package cli

import (
	"strings"
	"testing"
)

// `glossa login` inside GitHub Actions (RFC 0004 §6.3): no secret in
// the repository, no token in the workflow file. The job asks GitHub
// for an ID token and the server hands back a credential for one
// project, for half an hour.

// inActions gives the workspace the two variables GitHub sets when a
// workflow grants `id-token: write`, and takes away the stored API
// token — a run that has one is not the case under test.
func (w *workspace) inActions(r *fakeRunner) *workspace {
	delete(w.env, "GLOSSA_TOKEN")
	w.env["ACTIONS_ID_TOKEN_REQUEST_URL"] = r.URL()
	w.env["ACTIONS_ID_TOKEN_REQUEST_TOKEN"] = "runner-request-token"
	w.env["GITHUB_ACTIONS"] = "true"
	return w
}

func TestLoginInActionsExchangesAnIDTokenForACredential(t *testing.T) {
	srv := newFakeServer(t)
	runner := newFakeRunner(t)
	w := newWorkspace(t).withProject(srv, nil).inActions(runner)

	var out loginJSON
	w.json(&out, "login").want(t, ExitOK)

	if out.Method != "github_actions_oidc" {
		t.Errorf("method = %q, want github_actions_oidc", out.Method)
	}
	// It asked GitHub for Glossa's own audience, with the runner's
	// request token, and sent exactly what GitHub signed.
	if len(runner.audiences) != 1 || runner.audiences[0] != "glossa" {
		t.Errorf("audiences asked for = %v, want [glossa]", runner.audiences)
	}
	if runner.bearer != "Bearer runner-request-token" {
		t.Errorf("runner authorization = %q", runner.bearer)
	}
	if srv.ci.idToken != "id-token-for-glossa" {
		t.Errorf("id token sent = %q", srv.ci.idToken)
	}
	// It says which project it authenticated for, and what it may do.
	if out.Project == nil || out.Project.Slug != "shop" || out.Project.ID != "prj_1" {
		t.Errorf("project = %+v, want the resolved project", out.Project)
	}
	if out.Tenant.Slug != "acme" {
		t.Errorf("tenant = %+v, want the resolved tenant", out.Tenant)
	}
	if strings.Join(out.Permissions, ",") != "catalog.read,catalog.write" {
		t.Errorf("permissions = %v", out.Permissions)
	}
	if out.ExpiresAt == nil || out.Repository != "acme/shop" {
		t.Errorf("expires_at = %v, repository = %q", out.ExpiresAt, out.Repository)
	}
	// The credential is stored where the CLI keeps credentials, and the
	// secret is not in the output.
	if w.store.tokens[srv.URL()] != mintedCIToken {
		t.Errorf("stored = %q, want the minted credential", w.store.tokens[srv.URL()])
	}
	if strings.Contains(strings.Join(out.Permissions, ""), mintedCIToken) {
		t.Error("the credential leaked into the permissions")
	}
}

// What login is for: the commands after it authenticate with no secret
// in the repository.
func TestTheExchangedCredentialAuthenticatesTheCIJob(t *testing.T) {
	srv := newFakeServer(t)
	runner := newFakeRunner(t)
	w := newWorkspace(t).withProject(srv, map[string]string{"en": `{"cart.checkout": "Check out"}`}).inActions(runner)

	w.run("login").want(t, ExitOK)
	// No GLOSSA_TOKEN anywhere: push uses what login stored.
	if w.env["GLOSSA_TOKEN"] != "" {
		t.Fatal("the workspace still has an API token")
	}
	w.run("push").want(t, ExitOK)
	if srv.countRequests("POST /v1/tenants/ten_1/projects/prj_1/message-upserts ") == 0 {
		t.Errorf("push made no upsert; requests %v", srv.requests)
	}
}

func TestLoginInActionsReportsWhatWentWrong(t *testing.T) {
	t.Run("the workflow forgot id-token: write", func(t *testing.T) {
		srv := newFakeServer(t)
		runner := newFakeRunner(t)
		runner.status = 403
		w := newWorkspace(t).withProject(srv, nil).inActions(runner)

		r := w.run("login")
		r.want(t, ExitNetwork)
		if !strings.Contains(r.stderr, "id-token: write") {
			t.Errorf("stderr doesn't name the missing permission:\n%s", r.stderr)
		}
	})

	t.Run("the repository is not connected", func(t *testing.T) {
		srv := newFakeServer(t)
		srv.ci.refuse = "repository_not_connected"
		w := newWorkspace(t).withProject(srv, nil).inActions(newFakeRunner(t))

		r := w.run("login")
		r.want(t, ExitUsage)
		if !strings.Contains(r.stderr, "isn't connected") || !strings.Contains(r.stderr, "GLOSSA_TOKEN") {
			t.Errorf("stderr:\n%s", r.stderr)
		}
	})

	t.Run("the server has no GitHub App", func(t *testing.T) {
		srv := newFakeServer(t)
		srv.ci.refuse = "github_not_configured"
		w := newWorkspace(t).withProject(srv, nil).inActions(newFakeRunner(t))

		r := w.run("login")
		r.want(t, ExitNetwork)
		if !strings.Contains(r.stderr, "no GitHub App") {
			t.Errorf("stderr:\n%s", r.stderr)
		}
	})

	t.Run("the ID token doesn't verify", func(t *testing.T) {
		srv := newFakeServer(t)
		srv.ci.refuse = "invalid_id_token"
		w := newWorkspace(t).withProject(srv, nil).inActions(newFakeRunner(t))

		w.run("login").want(t, ExitNetwork)
	})
}

// A monorepo: the refusal lists the projects, so the fix is one flag
// away rather than a trip to Studio.
func TestAmbiguousRepositoryNamesTheProjectsToChooseFrom(t *testing.T) {
	srv := newFakeServer(t)
	srv.ci.projects = []string{"prj_1 (apps/admin)", "prj_2 (apps/web)"}
	w := newWorkspace(t).withProject(srv, nil).inActions(newFakeRunner(t))

	r := w.run("login")
	r.want(t, ExitUsage)
	if !strings.Contains(r.stderr, "--project") ||
		!strings.Contains(r.stderr, "prj_1 (apps/admin)") || !strings.Contains(r.stderr, "prj_2 (apps/web)") {
		t.Fatalf("stderr doesn't list the projects:\n%s", r.stderr)
	}
	// Naming one works, and the server is told which.
	w.run("login", "--project", "prj_1").want(t, ExitOK)
	if srv.ci.project != "prj_1" {
		t.Errorf("project sent = %q", srv.ci.project)
	}
}

// Outside Actions nothing changes: an API token is still how a person
// (or a job that keeps a secret) signs in.
func TestLoginStillTakesAnAPIToken(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	w.stdin = testToken + "\n"

	var out loginJSON
	w.json(&out, "login", "--token-stdin").want(t, ExitOK)
	if out.Method != "api_token" || out.Tenant.Slug != "acme" {
		t.Errorf("out = %+v", out)
	}
	if w.store.tokens[srv.URL()] != testToken {
		t.Errorf("stored = %q", w.store.tokens[srv.URL()])
	}
}

// --token-stdin inside Actions means the job meant the token: a
// workflow that still keeps a secret goes on using it, and the ID token
// is never even requested.
func TestAnExplicitTokenWinsOverTheActionsEnvironment(t *testing.T) {
	srv := newFakeServer(t)
	runner := newFakeRunner(t)
	w := newWorkspace(t).withProject(srv, nil).inActions(runner)
	w.stdin = testToken + "\n"

	var out loginJSON
	w.json(&out, "login", "--token-stdin").want(t, ExitOK)
	if out.Method != "api_token" {
		t.Errorf("method = %q, want api_token", out.Method)
	}
	if len(runner.audiences) != 0 {
		t.Errorf("it asked GitHub for an ID token anyway: %v", runner.audiences)
	}
}

// Half the environment is not enough: a workflow with the URL but no
// request token cannot ask for an ID token, so login falls back to the
// API-token path rather than failing in the middle.
func TestHalfTheActionsEnvironmentIsNotActions(t *testing.T) {
	srv := newFakeServer(t)
	runner := newFakeRunner(t)
	w := newWorkspace(t).withProject(srv, nil).inActions(runner)
	delete(w.env, "ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	w.stdin = testToken + "\n"

	var out loginJSON
	w.json(&out, "login").want(t, ExitOK)
	if out.Method != "api_token" {
		t.Errorf("method = %q, want api_token", out.Method)
	}
}
