package cli

import (
	"strings"
	"testing"
)

// withGitHub gives the workspace three installations: acme, which sees
// two repositories, contoso, and mirror, which sees a repository of the
// same name as acme's — the ambiguity a name has to be refused for.
func withGitHub(t *testing.T) (*fakeServer, *workspace) {
	t.Helper()
	srv, w := seeded(t)
	srv.addInstallation("acme", fakeRepository{id: 4242, full: "acme/shop", branch: "main"},
		fakeRepository{id: 77, full: "acme/site", branch: "trunk"})
	srv.addInstallation("contoso", fakeRepository{id: 99, full: "contoso/shop", branch: "main"})
	srv.addInstallation("mirror", fakeRepository{id: 5555, full: "acme/shop", branch: "main"})
	return srv, w
}

func TestGitHubConnectionsAddListAndRemove(t *testing.T) {
	srv, w := withGitHub(t)

	var added githubConnectionJSONDoc
	w.json(&added, "github", "connections", "add", "--repository", "acme/site",
		"--application", "web", "--path", "apps/web").want(t, ExitOK)
	c := added.Connection
	if added.Schema != "glossa.cli.github.connections.add/v1" || c.RepositoryID != 77 || c.RepositoryName != "acme/site" ||
		c.Path != "apps/web" || c.DefaultBranch != "trunk" || c.Project != "shop" || c.Application != "web" ||
		c.ApplicationID != "app_web" || c.InstallationID == "" {
		t.Fatalf("add = %+v", added)
	}

	// The numeric id is the other way in, and --branch overrides the
	// repository's default; the same repository connects twice as long
	// as the paths differ.
	var second githubConnectionJSONDoc
	w.json(&second, "github", "connections", "add", "--repository", "77", "--application", "admin",
		"--path", "apps/admin", "--branch", "release").want(t, ExitOK)
	if second.Connection.DefaultBranch != "release" || second.Connection.Application != "admin" {
		t.Fatalf("second add = %+v", second.Connection)
	}

	// A name several installations see needs --installation.
	var disambiguated githubConnectionJSONDoc
	w.json(&disambiguated, "github", "connections", "add", "--repository", "acme/shop",
		"--application", "web", "--installation", "mirror").want(t, ExitOK)
	if disambiguated.Connection.RepositoryID != 5555 || disambiguated.Connection.Path != "" {
		t.Fatalf("disambiguated add = %+v", disambiguated.Connection)
	}

	// Another project's connection: in the workspace, not in the
	// project's list.
	srv.addConnection(srv.gh.installations[0], fakeRepository{id: 4242, full: "acme/shop", branch: "main"},
		"prj_2", "app_other", "")

	var list githubConnectionsJSON
	w.json(&list, "github", "connections", "list").want(t, ExitOK)
	if list.Schema != "glossa.cli.github.connections.list/v1" || list.ProjectID == nil || *list.ProjectID != "prj_1" ||
		len(list.Connections) != 3 {
		t.Fatalf("list = %+v", list)
	}
	var all githubConnectionsJSON
	w.json(&all, "github", "connections", "list", "--all-projects").want(t, ExitOK)
	if all.ProjectID != nil || len(all.Connections) != 4 {
		t.Fatalf("list --all-projects = %+v", all) // every page, not only the first
	}
	var narrowed githubConnectionsJSON
	w.json(&narrowed, "github", "connections", "list", "--all-projects", "--installation", "mirror").want(t, ExitOK)
	if len(narrowed.Connections) != 1 || narrowed.Connections[0].RepositoryID != 5555 {
		t.Fatalf("list --installation = %+v", narrowed)
	}

	human := w.run("github", "connections", "list")
	for _, want := range []string{"REPOSITORY", "acme/site", "apps/web", "shop", "web", "trunk"} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("human list lacks %q:\n%s", want, human.stdout)
		}
	}

	var removed githubRemoveJSON
	w.json(&removed, "github", "connections", "remove", c.ID).want(t, ExitOK)
	if removed.Schema != "glossa.cli.github.connections.remove/v1" || removed.ConnectionID != c.ID {
		t.Fatalf("remove = %+v", removed)
	}
	if out := w.run("github", "connections", "remove", second.Connection.ID); !strings.Contains(out.stdout, "stays installed on GitHub") {
		t.Errorf("remove says nothing about the App:\n%s", out.stdout)
	}
	w.json(&list, "github", "connections", "list").want(t, ExitOK)
	if len(list.Connections) != 1 {
		t.Errorf("after two removals = %+v", list.Connections)
	}
}

func TestGitHubConnectionsListSaysWhatItLookedAt(t *testing.T) {
	_, w := withGitHub(t)
	out := w.run("github", "connections", "list")
	out.want(t, ExitOK)
	if !strings.Contains(out.stdout, "No repository is connected to shop") || !strings.Contains(out.stdout, "--all-projects") {
		t.Errorf("empty list:\n%s", out.stdout)
	}
}

func TestGitHubConnectionsRefusesBadInvocations(t *testing.T) {
	srv, w := withGitHub(t)
	w.run("github", "connections", "add", "--repository", "acme/site", "--application", "web").want(t, ExitOK)
	for _, c := range []struct {
		name          string
		args          []string
		exit          ExitCode
		code          string
		notConfigured bool
	}{
		{"no group", []string{"github"}, ExitUsage, "invalid_usage", false},
		{"unknown group", []string{"github", "repos", "list"}, ExitUsage, "invalid_usage", false},
		{"no action", []string{"github", "connections"}, ExitUsage, "invalid_usage", false},
		{"unknown action", []string{"github", "connections", "explode"}, ExitUsage, "invalid_usage", false},
		{"extra argument", []string{"github", "connections", "list", "extra"}, ExitUsage, "invalid_usage", false},
		{"both scopes", []string{"github", "connections", "list", "--project", "shop", "--all-projects"}, ExitUsage, "invalid_usage", false},
		{"no repository", []string{"github", "connections", "add", "--application", "web"}, ExitUsage, "invalid_usage", false},
		{"no application", []string{"github", "connections", "add", "--repository", "acme/site"}, ExitUsage, "invalid_usage", false},
		{"no connection id", []string{"github", "connections", "remove"}, ExitUsage, "invalid_usage", false},
		{"ambiguous repository", []string{"github", "connections", "add", "--repository", "acme/shop", "--application", "web"},
			ExitUsage, "repository_ambiguous", false},
		{"unknown repository", []string{"github", "connections", "add", "--repository", "acme/ghost", "--application", "web"},
			ExitUsage, "unknown_repository", false},
		{"unknown repository id", []string{"github", "connections", "add", "--repository", "12345", "--application", "web"},
			ExitUsage, "unknown_repository", false},
		{"unknown application", []string{"github", "connections", "add", "--repository", "acme/site", "--application", "ghost"},
			ExitUsage, "unknown_application", false},
		{"unknown installation", []string{"github", "connections", "add", "--repository", "acme/site", "--application", "web",
			"--installation", "nobody"}, ExitUsage, "unknown_installation", false},
		{"unknown project", []string{"github", "connections", "list", "--project", "ghost"}, ExitUsage, "project_not_found", false},
		{"already connected", []string{"github", "connections", "add", "--repository", "acme/site", "--application", "web"},
			ExitNetwork, "connection_exists", false},
		{"no App configured", []string{"github", "connections", "list"}, ExitNetwork, "github_not_configured", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv.setNotConfigured(c.notConfigured)
			defer srv.setNotConfigured(false)
			var doc errorDoc
			w.json(&doc, c.args...).want(t, c.exit)
			if doc.Error.Code != c.code || doc.Error.Fix == "" || doc.Error.Message == "" {
				t.Fatalf("glossa %s = %+v", strings.Join(c.args, " "), doc.Error)
			}
		})
	}
}
