package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// Git connections (RFC 0004 §6.1): which repository feeds which project
// and application, so Glossa can check that repository's pull requests
// and comment on them. Connecting one normally happens in Studio, which
// walks through installing the App; this group is the scripting path,
// for a workspace that provisions its projects from CI.

// ── output shapes (cmd/glossa/README.md, "JSON output") ─────────────

// githubConnectionJSON is one Git connection. The IDs are the API's;
// Project and Application are their slugs when they could be read, so a
// script can match on either.
type githubConnectionJSON struct {
	ID             string     `json:"id"`
	InstallationID string     `json:"installation_id"`
	RepositoryID   int64      `json:"repository_id"`
	RepositoryName string     `json:"repository_name"`
	ProjectID      string     `json:"project_id"`
	Project        string     `json:"project,omitempty"`
	ApplicationID  string     `json:"application_id"`
	Application    string     `json:"application,omitempty"`
	DefaultBranch  string     `json:"default_branch"`
	Path           string     `json:"path"`
	CreatedBy      string     `json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
	Version        int        `json:"version"`
}

type githubConnectionsJSON struct {
	Schema string `json:"schema"`
	// ProjectID is null when the list is the whole workspace's.
	ProjectID   *string                `json:"project_id"`
	Connections []githubConnectionJSON `json:"connections"`
}

type githubConnectionJSONDoc struct {
	Schema     string               `json:"schema"`
	Connection githubConnectionJSON `json:"connection"`
}

type githubRemoveJSON struct {
	Schema       string `json:"schema"`
	ConnectionID string `json:"connection_id"`
}

const githubUsage = `github connections <action> [flags]

Actions:
  connections list [--project P | --all-projects] [--installation I]
                  the Git connections: which repository feeds which project and application
  connections add --repository <id|owner/name> --application A [--project P] [--path apps/web]
                  [--branch main] [--installation I]
                  connect a repository to a project and one of its applications
  connections remove <connection-id>
                  unlink a repository; the App stays installed on GitHub

A connection belongs to the workspace, not to one project, so list shows the configured
project's connections unless --all-projects widens it to every project.

--repository takes GitHub's numeric repository id or owner/name; a name is resolved
through the installations, and --installation (an ID or the account's login) is only
needed when several installations see a repository of that name.

Connecting a repository normally happens in Studio, which walks through installing the
GitHub App. This group is for scripting (RFC 0004 §6.1).`

type githubArgs struct {
	action, project, application, repository string
	path, branch, installation, connection   string
	allProjects                              bool
}

func parseGitHubArgs(inv *invocation, args []string) (githubArgs, error) {
	fs := inv.flags(githubUsage)
	var a githubArgs
	fs.StringVar(&a.project, "project", "", "list, add: the project, by slug or ID (default: glossa.yaml's)")
	fs.StringVar(&a.application, "application", "", "add: the project's application, by slug or ID")
	fs.StringVar(&a.repository, "repository", "", "add: the repository, by GitHub's numeric id or owner/name")
	fs.StringVar(&a.path, "path", "", "add: the monorepo subdirectory it covers (default: the whole repository)")
	fs.StringVar(&a.branch, "branch", "", "add: the default branch (default: the repository's on GitHub)")
	fs.StringVar(&a.installation, "installation", "", "the installation, by ID or account login")
	fs.BoolVar(&a.allProjects, "all-projects", false, "list: every project's connections, not only this project's")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if len(pos) == 0 || pos[0] != "connections" {
		return a, usageError(inv.name, "github takes one group: connections (`glossa github connections list`)")
	}
	pos = pos[1:]
	if len(pos) == 0 {
		return a, usageError(inv.name, "missing action: list, add or remove")
	}
	a.action, pos = pos[0], pos[1:]
	switch a.action {
	case "list":
		if a.project != "" && a.allProjects {
			return a, usageError(inv.name, "--project names one project and --all-projects every one: pass one of them")
		}
		return a, noMore(inv, pos)
	case "add":
		if a.repository == "" {
			return a, usageError(inv.name, "add needs --repository <id|owner/name>, a repository an installation can see")
		}
		if a.application == "" {
			return a, usageError(inv.name, "add needs --application <slug|id>, the project's application the repository builds")
		}
		a.path = strings.Trim(strings.TrimSpace(a.path), "/")
		return a, noMore(inv, pos)
	case "remove":
		if len(pos) == 0 {
			return a, usageError(inv.name, "remove takes the connection's ID (`glossa github connections list` shows it)")
		}
		a.connection = pos[0]
		return a, noMore(inv, pos[1:])
	}
	return a, usageError(inv.name, "unknown action %q (list, add, remove)", a.action)
}

func runGitHub(ctx context.Context, inv *invocation, args []string) error {
	a, err := parseGitHubArgs(inv, args)
	if err != nil {
		return err
	}
	// Connections are tenant-scoped, but the CLI always has a project:
	// it is what --project defaults to, and what an application is
	// looked up in.
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	switch a.action {
	case "list":
		return inv.githubList(ctx, p, a)
	case "add":
		return inv.githubAdd(ctx, p, a)
	}
	return inv.githubRemove(ctx, p, a.connection)
}

func (inv *invocation) githubList(ctx context.Context, p *project, a githubArgs) error {
	filter := remote.ConnectionFilter{}
	var scope *string
	if !a.allProjects {
		target, err := inv.targetProject(ctx, p, a.project)
		if err != nil {
			return err
		}
		filter.Project, scope = target.Id, &target.Id
	}
	if a.installation != "" {
		ins, err := inv.installations(ctx, p)
		if err != nil {
			return err
		}
		one, err := installationNamed(ins, a.installation)
		if err != nil {
			return err
		}
		filter.Installation = one.Id
	}
	conns, err := p.client.GitConnections(ctx, p.scope.Tenant, filter)
	if err != nil {
		return inv.githubError(err, "can't list the Git connections")
	}
	labels := inv.connectionLabels(ctx, p, conns)
	out := githubConnectionsJSON{Schema: "glossa.cli.github.connections.list/v1", ProjectID: scope,
		Connections: []githubConnectionJSON{}}
	for _, c := range conns {
		out.Connections = append(out.Connections, connectionJSON(c, labels))
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Connections) == 0 {
			pr.line("No repository is connected%s.", scopeSuffix(p, a))
			pr.line("  %s", pr.dim("connect one in Studio, or with `glossa github connections add --repository owner/name --application web`"))
			return
		}
		rows := [][]string{{"REPOSITORY", "PATH", "PROJECT", "APPLICATION", "BRANCH", "ID"}}
		for _, c := range out.Connections {
			rows = append(rows, []string{c.RepositoryName, dash(c.Path), orDefault(c.Project, shortID(c.ProjectID)),
				orDefault(c.Application, shortID(c.ApplicationID)), c.DefaultBranch, c.ID})
		}
		pr.table(rows)
	})
}

// scopeSuffix says which connections were looked at, so an empty list
// doesn't read as "the workspace has none".
func scopeSuffix(p *project, a githubArgs) string {
	if a.allProjects {
		return " in this workspace"
	}
	return " to " + orDefault(a.project, string(p.info.Slug)) + " (--all-projects widens it to the workspace)"
}

func (inv *invocation) githubAdd(ctx context.Context, p *project, a githubArgs) error {
	target, err := inv.targetProject(ctx, p, a.project)
	if err != nil {
		return err
	}
	appID, appSlug, err := inv.githubApplication(ctx, p, target, a.application)
	if err != nil {
		return err
	}
	install, repoID, err := inv.githubRepository(ctx, p, a)
	if err != nil {
		return err
	}
	body := remote.GitConnectionRequest{InstallationId: install, RepositoryId: repoID,
		ProjectId: target.Id, ApplicationId: appID}
	if a.branch != "" {
		body.DefaultBranch = &a.branch
	}
	if a.path != "" {
		body.Path = &a.path
	}
	conn, err := p.client.CreateGitConnection(ctx, p.scope.Tenant, body, newIdempotencyKey())
	if err != nil {
		return inv.githubError(err, "can't connect the repository")
	}
	labels := map[string]string{target.Id: string(target.Slug), appID: appSlug}
	out := githubConnectionJSONDoc{Schema: "glossa.cli.github.connections.add/v1", Connection: connectionJSON(conn, labels)}
	return inv.emit(out, func(pr *printer) {
		c := out.Connection
		pr.line("%s Connected %s%s to %s · %s, following %s", pr.pass(), c.RepositoryName, pathSuffix(c.Path),
			c.Project, c.Application, c.DefaultBranch)
		pr.line("  %s", pr.dim("Glossa now checks that repository's pull requests; `glossa github connections remove "+c.ID+"` unlinks it"))
	})
}

func pathSuffix(path string) string {
	if path == "" {
		return ""
	}
	return "/" + path
}

func (inv *invocation) githubRemove(ctx context.Context, p *project, id string) error {
	if err := p.client.DeleteGitConnection(ctx, p.scope.Tenant, id); err != nil {
		return inv.githubError(err, "can't remove connection "+id)
	}
	out := githubRemoveJSON{Schema: "glossa.cli.github.connections.remove/v1", ConnectionID: id}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s Unlinked %s: Glossa no longer checks that repository's pull requests", pr.pass(), id)
		pr.line("  %s", pr.dim("the App stays installed on GitHub — only GitHub can uninstall it, on the account's Applications settings"))
	})
}

// ── resolving what the flags name ───────────────────────────────────

// targetProject is --project, or the configured project when it is
// empty.
func (inv *invocation) targetProject(ctx context.Context, p *project, ref string) (remote.Project, error) {
	if ref == "" || ref == p.scope.Project || ref == string(p.info.Slug) {
		return p.info, nil
	}
	target, err := p.client.ResolveProject(ctx, p.scope.Tenant, ref)
	if err != nil {
		var nf *remote.ErrProjectNotFound
		if errors.As(err, &nf) {
			return remote.Project{}, &Error{Exit: ExitUsage, Code: "project_not_found",
				What: fmt.Sprintf("no project %q in this workspace", ref), Why: nf.Error(),
				Fix: "pass --project with a project's slug or ID (Studio lists them), or leave it out for glossa.yaml's"}
		}
		return remote.Project{}, inv.githubError(err, "can't read the project")
	}
	return target, nil
}

// githubApplication resolves --application in target, by slug or ID: a
// connection names the application whose messages the repository
// builds, and one the project doesn't have could never work.
func (inv *invocation) githubApplication(ctx context.Context, p *project, target remote.Project, ref string) (id, slug string, err error) {
	apps, err := p.client.Applications(ctx, remote.Scope{Tenant: p.scope.Tenant, Project: target.Id})
	if err != nil {
		return "", "", inv.githubError(err, "can't read the project's applications")
	}
	have := make([]string, 0, len(apps))
	for _, app := range apps {
		if app.Id == ref || string(app.Slug) == ref {
			return app.Id, string(app.Slug), nil
		}
		have = append(have, string(app.Slug))
	}
	fix := "create the application in Studio first"
	if len(have) > 0 {
		fix = "pass --application with one of: " + strings.Join(have, ", ")
	}
	return "", "", &Error{Exit: ExitUsage, Code: "unknown_application",
		What:  fmt.Sprintf("project %s has no application %q", target.Slug, ref),
		Where: "--application", Why: "a connection names the application the repository builds", Fix: fix}
}

func (inv *invocation) installations(ctx context.Context, p *project) ([]remote.GitHubInstallation, error) {
	ins, err := p.client.GitHubInstallations(ctx, p.scope.Tenant)
	if err != nil {
		return nil, inv.githubError(err, "can't read the workspace's GitHub installations")
	}
	return ins, nil
}

// installationNamed finds the installation --installation names, by
// Glossa's ID or by the account it belongs to. An account has at most
// one installation, so the first match is the only one.
func installationNamed(ins []remote.GitHubInstallation, ref string) (remote.GitHubInstallation, error) {
	for _, i := range ins {
		if i.Id == ref || strings.EqualFold(i.AccountLogin, ref) {
			return i, nil
		}
	}
	accounts := make([]string, 0, len(ins))
	for _, i := range ins {
		accounts = append(accounts, i.AccountLogin)
	}
	fix := "install the Glossa App on the account from Studio first"
	if len(accounts) > 0 {
		fix = "pass --installation with one of: " + strings.Join(accounts, ", ")
	}
	return remote.GitHubInstallation{}, &Error{Exit: ExitUsage, Code: "unknown_installation",
		What: fmt.Sprintf("no GitHub installation %q in this workspace", ref), Where: "--installation",
		Why: "--installation takes the installation's ID or the account it belongs to", Fix: fix}
}

// githubRepository finds the repository --repository names among the
// installations' repositories — GitHub's numeric id, or owner/name,
// which is what makes the command usable by hand — and the installation
// that sees it, since a connection needs both.
func (inv *invocation) githubRepository(ctx context.Context, p *project, a githubArgs) (installation string, repository int64, err error) {
	ins, err := inv.installations(ctx, p)
	if err != nil {
		return "", 0, err
	}
	if a.installation != "" {
		one, err := installationNamed(ins, a.installation)
		if err != nil {
			return "", 0, err
		}
		ins = []remote.GitHubInstallation{one}
	}
	num, numeric := repositoryID(a.repository)
	type candidate struct {
		account, name string
		installation  string
		repository    int64
	}
	var found []candidate
	unavailable := false
	for _, i := range ins {
		unavailable = unavailable || i.RepositoriesUnavailable
		for _, r := range remote.Repositories(i) {
			if (numeric && r.RepositoryId == num) || (!numeric && strings.EqualFold(r.FullName, a.repository)) {
				found = append(found, candidate{account: i.AccountLogin, name: r.FullName, installation: i.Id, repository: r.RepositoryId})
			}
		}
	}
	switch {
	case len(found) == 1:
		return found[0].installation, found[0].repository, nil
	case len(found) > 1:
		seen := make([]string, 0, len(found))
		for _, c := range found {
			seen = append(seen, c.account+" ("+c.name+")")
		}
		return "", 0, &Error{Exit: ExitUsage, Code: "repository_ambiguous",
			What:  fmt.Sprintf("several installations see a repository %q", a.repository),
			Where: "--repository", Why: strings.Join(seen, " and ") + " each have one",
			Fix: "pass --installation with the account it belongs to, e.g. --installation " + found[0].account +
				", or --repository with GitHub's numeric id"}
	case numeric && a.installation != "":
		// A numeric id needs no lookup, so an installation whose
		// repositories can't be read right now (GitHub unreachable, the
		// installation suspended) is still usable: let the server, which
		// asks GitHub itself, have the last word.
		return ins[0].Id, num, nil
	}
	why := "no installation of this workspace sees it"
	switch {
	case len(ins) == 0:
		why = "this workspace has no GitHub installation yet"
	case unavailable:
		why = "GitHub couldn't be reached for at least one installation, so what it covers is unknown"
	}
	return "", 0, &Error{Exit: ExitUsage, Code: "unknown_repository",
		What: fmt.Sprintf("no repository %q", a.repository), Where: "--repository", Why: why,
		Fix: "install the Glossa App on the account and give it access to the repository (Studio walks through it); " +
			"with GitHub unreachable, --repository <numeric id> and --installation get through anyway"}
}

// repositoryID reads --repository as GitHub's numeric id; owner/name
// isn't one.
func repositoryID(ref string) (int64, bool) {
	n, err := strconv.ParseInt(ref, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// connectionLabels names the projects and applications the connections
// point at. It is best effort: a workspace-wide list crosses projects,
// and a token that may read connections needn't be able to read every
// project, so an ID that can't be resolved stays an ID.
func (inv *invocation) connectionLabels(ctx context.Context, p *project, conns []remote.GitConnection) map[string]string {
	labels := map[string]string{}
	if len(conns) == 0 {
		return labels
	}
	wanted := map[string]bool{}
	for _, c := range conns {
		wanted[c.ProjectId] = true
	}
	projects, err := p.client.Projects(ctx, p.scope.Tenant)
	if err != nil {
		return labels
	}
	for _, pr := range projects {
		if !wanted[pr.Id] {
			continue
		}
		labels[pr.Id] = string(pr.Slug)
		apps, err := p.client.Applications(ctx, remote.Scope{Tenant: p.scope.Tenant, Project: pr.Id})
		if err != nil {
			continue
		}
		for _, app := range apps {
			labels[app.Id] = string(app.Slug)
		}
	}
	return labels
}

func connectionJSON(c remote.GitConnection, labels map[string]string) githubConnectionJSON {
	return githubConnectionJSON{ID: c.Id, InstallationID: c.InstallationId, RepositoryID: c.RepositoryId,
		RepositoryName: c.RepositoryName, ProjectID: c.ProjectId, Project: labels[c.ProjectId],
		ApplicationID: c.ApplicationId, Application: labels[c.ApplicationId], DefaultBranch: c.DefaultBranch,
		Path: c.Path, CreatedBy: derefStr(c.CreatedBy), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Version: c.Version}
}

// ── errors ──────────────────────────────────────────────────────────

// githubFixes explains the GitHub API's problem codes (RFC 0004 §6.1).
var githubFixes = map[string]string{
	"github_not_configured":  "this deployment has no GitHub App configured: ask the operator to set GLOSSA_GITHUB_APP_ID and its private key, webhook secret and OAuth client, or connect the repository in Studio",
	"github_unavailable":     "GitHub didn't answer; run the command again in a moment",
	"repository_not_visible": "the installation can't see that repository: give the App access to it on GitHub, then connect it again",
	"application_not_found":  "pass --application with an application the project has (Studio lists them)",
	"connection_exists":      "that repository and path are already connected; `glossa github connections list` shows it, and `remove` unlinks it first",
	"installation_revoked":   "the App was uninstalled on GitHub: install it again from Studio, then connect the repository",
	"invalid_connection":     "check --path (a subdirectory without leading or trailing slashes) and --branch",
	"invalid_query":          "check --project and --installation: they take an ID, and --project also a slug",
}

// githubError explains a failed GitHub request: input the server
// refuses is a usage error (exit 2), the server refusing the operation
// a network error (exit 3), both with the server's detail as the
// reason.
func (inv *invocation) githubError(err error, what string) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	e := asError(inv.apiError(err, what))
	if fix, ok := githubFixes[ae.Code]; ok {
		e.Fix = fix
	}
	if ae.Status == 400 {
		e.Exit = ExitUsage
	}
	switch ae.Status {
	case 400, 404, 409, 503:
		if ae.Detail != "" {
			e.Why = ae.Detail
		}
	}
	return e
}
