package cli

import (
	"net/http"
	"strconv"
	"strings"
)

// The fake server's GitHub endpoints (RFC 0004 §6.1): the workspace's
// installations with the repositories each sees, and its Git
// connections, with the contract's shapes and problem codes. Both lists
// serve one item per page, so a command that doesn't follow
// next_page_token sees only the first.

type fakeInstallation struct {
	id, account, accountType, state string
	number                          int64
	repos                           []fakeRepository
	unavailable                     bool
}

type fakeRepository struct {
	id     int64
	full   string
	branch string
}

type fakeConnection struct {
	id, installation, project, application, branch, path string
	repository                                           int64
	repositoryName                                       string
}

type fakeGitHub struct {
	seq           int
	installations []*fakeInstallation
	connections   []*fakeConnection
	// notConfigured makes every route answer 503, as a deployment
	// without a GitHub App does.
	notConfigured bool
	// applications are the application IDs the fake project has.
	applications []string
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{applications: []string{"app_web", "app_admin"}}
}

func (g *fakeGitHub) nextID(prefix string) string {
	g.seq++
	return prefix + strconv.Itoa(g.seq)
}

func (f *fakeServer) routeGitHub(mux *http.ServeMux) {
	t := "/v1/tenants/ten_1"
	mux.HandleFunc("GET "+t+"/github/installations", f.listInstallations)
	mux.HandleFunc("GET "+t+"/github/connections", f.listConnections)
	mux.HandleFunc("POST "+t+"/github/connections", f.createConnection)
	mux.HandleFunc("DELETE "+t+"/github/connections/{id}", f.deleteConnection)
}

// addInstallation gives the workspace an active installation of account
// with those repositories.
func (f *fakeServer) addInstallation(account string, repos ...fakeRepository) *fakeInstallation {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := &fakeInstallation{id: f.gh.nextID("ins_"), account: account, accountType: "Organization",
		state: "active", number: int64(1000 + len(f.gh.installations)), repos: repos}
	f.gh.installations = append(f.gh.installations, i)
	return i
}

// addConnection links a repository without going through the API, for
// connections of other projects.
func (f *fakeServer) addConnection(install *fakeInstallation, repo fakeRepository, project, application, path string) *fakeConnection {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &fakeConnection{id: f.gh.nextID("gcn_"), installation: install.id, project: project, application: application,
		branch: repo.branch, path: path, repository: repo.id, repositoryName: repo.full}
	f.gh.connections = append(f.gh.connections, c)
	return c
}

// setNotConfigured makes the GitHub routes answer 503
// github_not_configured, as a deployment without a GitHub App does.
func (f *fakeServer) setNotConfigured(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gh.notConfigured = v
}

// page serves items one at a time, with the next index as the token.
func page(r *http.Request, total int) (from int, next any) {
	from, _ = strconv.Atoi(r.URL.Query().Get("page_token"))
	if from >= total {
		return total, nil
	}
	if from+1 < total {
		return from, strconv.Itoa(from + 1)
	}
	return from, nil
}

// unconfigured answers as a deployment without a GitHub App.
func (f *fakeServer) unconfigured(w http.ResponseWriter) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.gh.notConfigured {
		return false
	}
	problemResp(w, 503, "github_not_configured", "this deployment has no GitHub App configured")
	return true
}

func installationJSON(i *fakeInstallation) map[string]any {
	repos := []map[string]any{}
	for _, r := range i.repos {
		owner, name, _ := strings.Cut(r.full, "/")
		repos = append(repos, map[string]any{"repository_id": r.id, "name": name, "full_name": r.full,
			"private": true, "default_branch": r.branch, "owner": owner})
	}
	return map[string]any{"id": i.id, "installation_id": i.number, "account_login": i.account,
		"account_type": i.accountType, "state": i.state, "repositories": repos,
		"repositories_unavailable": i.unavailable, "connected_at": fakeTime, "connected_by": "token:1"}
}

func connJSON(c *fakeConnection) map[string]any {
	return map[string]any{"id": c.id, "installation_id": c.installation, "repository_id": c.repository,
		"repository_name": c.repositoryName, "project_id": c.project, "application_id": c.application,
		"default_branch": c.branch, "path": c.path, "created_by": "token:1", "created_at": fakeTime,
		"updated_at": fakeTime, "version": 0}
}

func (f *fakeServer) listInstallations(w http.ResponseWriter, r *http.Request) {
	if f.unconfigured(w) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	from, next := page(r, len(f.gh.installations))
	items := []map[string]any{}
	if from < len(f.gh.installations) {
		items = append(items, installationJSON(f.gh.installations[from]))
	}
	out := map[string]any{"items": items}
	if next != nil {
		out["next_page_token"] = next
	}
	writeJSONResp(w, 200, out)
}

func (f *fakeServer) listConnections(w http.ResponseWriter, r *http.Request) {
	if f.unconfigured(w) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var matching []*fakeConnection
	for _, c := range f.gh.connections {
		if p := r.URL.Query().Get("project"); p != "" && c.project != p {
			continue
		}
		if i := r.URL.Query().Get("installation"); i != "" && c.installation != i {
			continue
		}
		matching = append(matching, c)
	}
	from, next := page(r, len(matching))
	items := []map[string]any{}
	if from < len(matching) {
		items = append(items, connJSON(matching[from]))
	}
	out := map[string]any{"items": items}
	if next != nil {
		out["next_page_token"] = next
	}
	writeJSONResp(w, 200, out)
}

func (f *fakeServer) createConnection(w http.ResponseWriter, r *http.Request) {
	if f.unconfigured(w) {
		return
	}
	var body struct {
		InstallationID string  `json:"installation_id"`
		RepositoryID   int64   `json:"repository_id"`
		ProjectID      string  `json:"project_id"`
		ApplicationID  string  `json:"application_id"`
		DefaultBranch  *string `json:"default_branch"`
		Path           *string `json:"path"`
	}
	decodeBody(r, &body)
	f.mu.Lock()
	defer f.mu.Unlock()
	var install *fakeInstallation
	for _, i := range f.gh.installations {
		if i.id == body.InstallationID {
			install = i
		}
	}
	if install == nil {
		problemResp(w, 404, "repository_not_visible", "unknown installation")
		return
	}
	if install.state == "revoked" {
		problemResp(w, 409, "installation_revoked", "the App was uninstalled on GitHub")
		return
	}
	var repo *fakeRepository
	for i := range install.repos {
		if install.repos[i].id == body.RepositoryID {
			repo = &install.repos[i]
		}
	}
	if repo == nil {
		problemResp(w, 404, "repository_not_visible", "the installation can't see that repository")
		return
	}
	if !contains(f.gh.applications, body.ApplicationID) || body.ProjectID != "prj_1" {
		problemResp(w, 404, "application_not_found", "the project has no such application")
		return
	}
	path := derefStr(body.Path)
	if strings.HasPrefix(path, "/") {
		problemResp(w, 400, "invalid_connection", "path must not start with a slash")
		return
	}
	for _, c := range f.gh.connections {
		if c.repository == body.RepositoryID && c.path == path {
			problemResp(w, 409, "connection_exists", "that repository and path are already connected")
			return
		}
	}
	c := &fakeConnection{id: f.gh.nextID("gcn_"), installation: install.id, project: body.ProjectID,
		application: body.ApplicationID, branch: orDefault(derefStr(body.DefaultBranch), repo.branch),
		path: path, repository: repo.id, repositoryName: repo.full}
	f.gh.connections = append(f.gh.connections, c)
	w.Header().Set("Location", "/v1/tenants/ten_1/github/connections/"+c.id)
	writeJSONResp(w, 201, connJSON(c))
}

func (f *fakeServer) deleteConnection(w http.ResponseWriter, r *http.Request) {
	if f.unconfigured(w) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := r.PathValue("id")
	for i, c := range f.gh.connections {
		if c.id == id {
			f.gh.connections = append(f.gh.connections[:i], f.gh.connections[i+1:]...)
			w.WriteHeader(204)
			return
		}
	}
	problemResp(w, 404, "not_found", "no connection "+id)
}
