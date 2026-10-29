package cli

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
	"go.klarlabs.de/glossa/platform/internal/cli/credentials"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// tenantJSON is a tenant in --json output.
type tenantJSON struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// serverFor is --server, else glossa.yaml's server (if any).
func (inv *invocation) serverFor(flagValue string) (string, error) {
	if flagValue != "" {
		return strings.TrimRight(flagValue, "/"), nil
	}
	if s := inv.env.getenv("GLOSSA_SERVER"); s != "" {
		return strings.TrimRight(s, "/"), nil
	}
	cfg, err := inv.loadConfig()
	if err != nil {
		return "", &Error{Exit: ExitUsage, Code: "no_server", What: "which server?",
			Why: "no --server flag, GLOSSA_SERVER or glossa.yaml", Fix: "pass --server https://glossa.example.com"}
	}
	return strings.TrimRight(cfg.Server, "/"), nil
}

// ── login ───────────────────────────────────────────────────────────

type loginJSON struct {
	Schema   string `json:"schema"`
	Server   string `json:"server"`
	StoredIn string `json:"stored_in"`
	// Method is "api_token", "github_actions_oidc" or "device".
	Method string `json:"method"`
	// Device sign-in (RFC 0006 §7.2): the person it signed in and when
	// that session ends.
	Person *personJSON `json:"person,omitempty"`
	Tenant tenantJSON  `json:"tenant"`
	// The rest is the GitHub Actions exchange's (RFC 0004 §6.3): the
	// one project the credential may act on, what it may do there, when
	// it dies, and the repository it was minted for.
	Project     *projectJSON `json:"project,omitempty"`
	Permissions []string     `json:"permissions,omitempty"`
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"`
	Repository  string       `json:"repository,omitempty"`
}

const loginUsage = `login [--server URL] [--device [--client-name NAME]] [--token-stdin] [--project <id>] [--json]

Stores a credential for the server.

In GitHub Actions, with ` + "`permissions: { id-token: write }`" + `, login needs no
secret: it asks GitHub for an OIDC ID token and exchanges it for a
30-minute credential scoped to the connected project. Anywhere else it
takes an API token, from the terminal or --token-stdin.

With --device, login signs you in as yourself instead: it shows a code,
you approve it in Studio (the page opens at the URL it prints), and the
CLI stores your session. Sign out with ` + "`glossa logout`" + `.`

func runLogin(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags(loginUsage)
	server := fs.String("server", "", "glossa-server URL (default: glossa.yaml's server)")
	fromStdin := fs.Bool("token-stdin", false, "read an API token from stdin (for scripts)")
	project := fs.String("project", "", "in GitHub Actions: the project's ID, when the repository feeds several")
	device := fs.Bool("device", false, "sign in as yourself by approving a code in Studio")
	clientName := fs.String("client-name", "", "with --device: what to call this machine in Studio (default: glossa CLI on <hostname>)")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	srv, err := inv.serverFor(*server)
	if err != nil {
		return err
	}
	if *device {
		if *fromStdin {
			return &Error{Exit: ExitUsage, Code: "conflicting_flags", What: "--device and --token-stdin are two ways to sign in",
				Fix: "use one"}
		}
		return inv.loginWithDevice(ctx, srv, *clientName)
	}
	// An explicit API token wins: a job that still keeps GLOSSA_TOKEN
	// as a secret should go on using it, and a person piping a token in
	// never meant to authenticate as the workflow.
	if !*fromStdin && inv.inGitHubActions() {
		return inv.loginWithActionsOIDC(ctx, srv, *project)
	}
	tok, err := inv.readToken(*fromStdin)
	if err != nil {
		return err
	}
	if !tokenPattern.MatchString(tok) {
		return &Error{Exit: ExitUsage, Code: "invalid_token", What: "that isn't a Glossa API token",
			Why: "tokens look like glossa_api_ followed by 43 characters", Fix: "copy the token Studio showed when you created it"}
	}
	tenant, err := inv.verifyToken(ctx, srv, tok)
	if err != nil {
		return err
	}
	store, err := inv.store(srv, tok)
	if err != nil {
		return err
	}
	out := loginJSON{Schema: "glossa.cli.login/v1", Server: srv, StoredIn: store, Method: "api_token", Tenant: tenant}
	return inv.emit(out, func(p *printer) {
		p.line("%s Logged in to %s as tenant %s (%s)", p.pass(), srv, tenant.Name, tenant.Slug)
		p.line("  token stored in %s", store)
	})
}

// loginWithActionsOIDC authenticates this workflow run (RFC 0004 §6.3).
//
// The run asks GitHub for an ID token with Glossa's audience, and the
// server decides everything from the claims GitHub signed: which
// repository, and so which tenant and project. Nothing the job could
// have forged takes part, and the credential that comes back may only
// read and write that project's catalog, for thirty minutes.
func (inv *invocation) loginWithActionsOIDC(ctx context.Context, server, project string) error {
	idToken, err := inv.requestActionsIDToken(ctx)
	if err != nil {
		return err
	}
	// No Glossa credential: the ID token is the credential.
	c, err := newClientWithToken(inv, server, "")
	if err != nil {
		return err
	}
	minted, err := c.ExchangeGitHubOIDC(ctx, idToken, project)
	if err != nil {
		return inv.exchangeError(err)
	}
	store, err := inv.store(server, minted.Token)
	if err != nil {
		return err
	}
	out := loginJSON{
		Schema: "glossa.cli.login/v1", Server: server, StoredIn: store, Method: "github_actions_oidc",
		Tenant: tenantJSON{ID: minted.TenantId}, Permissions: permissionNames(minted.Permissions),
		ExpiresAt: &minted.ExpiresAt, Project: &projectJSON{ID: minted.ProjectId},
	}
	if minted.Repository != nil {
		out.Repository = *minted.Repository
	}
	// Name the project and the tenant rather than printing two UUIDs.
	// It is a read the new credential is allowed, so it also proves the
	// credential works; failing it is not a reason to fail the login.
	named, err := newClientWithToken(inv, server, minted.Token)
	if err == nil {
		if ts, err := named.Tenants(ctx); err == nil && len(ts) == 1 {
			out.Tenant = tenantJSON{ID: ts[0].Id, Slug: ts[0].Slug, Name: ts[0].Name, Kind: string(ts[0].Kind)}
		}
		if p, err := named.ResolveProject(ctx, minted.TenantId, minted.ProjectId); err == nil {
			out.Project = &projectJSON{ID: p.Id, Slug: p.Slug, Name: p.Name, SourceLocale: p.SourceLocale}
		}
	}
	return inv.emit(out, func(p *printer) {
		p.line("%s Authenticated %s as a GitHub Actions run of %s", p.pass(), server, orDash(out.Repository))
		p.line("  project    %s", projectLabel(out.Project))
		p.line("  may        %s", strings.Join(out.Permissions, ", "))
		p.line("  expires    %s %s", minted.ExpiresAt.Format(time.RFC3339), p.dim("(30 minutes; no refresh)"))
		p.line("  stored in  %s", store)
	})
}

// exchangeError explains a refused exchange in the CLI's own terms.
// Ambiguity is the one that can be fixed from here, so it lists the
// projects the server named.
func (inv *invocation) exchangeError(err error) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	switch ae.Code {
	case "repository_not_connected":
		return &Error{Exit: ExitUsage, Code: ae.Code, What: "this repository isn't connected to a Glossa project",
			Why: "the OIDC exchange matches GitHub's numeric repository ID against a Git connection",
			Fix: "connect it in Studio → Settings → GitHub, or authenticate with GLOSSA_TOKEN"}
	case "ambiguous_project":
		fix := "pass --project <id>"
		if len(ae.Errors) > 0 {
			ids := make([]string, len(ae.Errors))
			for i, fe := range ae.Errors {
				ids[i] = fe.Detail
			}
			fix += ": " + strings.Join(ids, ", ")
		}
		return &Error{Exit: ExitUsage, Code: ae.Code, What: "this repository feeds several projects",
			Why: "a CI credential is for exactly one project", Fix: fix}
	case "invalid_id_token":
		return &Error{Exit: ExitNetwork, Code: ae.Code, What: "the server didn't accept GitHub's ID token",
			Why: detail(ae), Fix: "check the server's clock and its GitHub configuration, or use GLOSSA_TOKEN"}
	case "github_not_configured":
		return &Error{Exit: ExitNetwork, Code: ae.Code, What: "this server has no GitHub App",
			Why: "there are no Git connections to match a repository against",
			Fix: "authenticate with GLOSSA_TOKEN (an API token created in Studio)"}
	}
	return inv.apiError(err, "can't exchange the GitHub Actions ID token")
}

// store saves tok for server and says where it went.
func (inv *invocation) store(server, tok string) (string, error) {
	s, err := inv.credentialStore()
	if err != nil {
		return "", err
	}
	if err := s.Set(server, tok); err != nil {
		return "", &Error{Exit: ExitUsage, Code: "store_failed", What: "can't store the token", Where: s.Name(),
			Why: err.Error(), Fix: "set GLOSSA_TOKEN instead"}
	}
	return s.Name(), nil
}

func permissionNames(ps []apiclient.CITokenPermissions) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p)
	}
	return out
}

func projectLabel(p *projectJSON) string {
	if p == nil {
		return "—"
	}
	if p.Slug != "" {
		return p.Name + " (" + p.Slug + ", " + p.ID + ")"
	}
	return p.ID
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func (inv *invocation) readToken(fromStdin bool) (string, error) {
	if fromStdin || !inv.env.Interactive || inv.env.ReadSecret == nil {
		if inv.env.Interactive && !fromStdin {
			inv.out.line("Paste an API token (create one in Studio → Settings → API tokens):")
		}
		line, err := bufio.NewReader(inv.env.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		if strings.TrimSpace(line) == "" {
			return "", &Error{Exit: ExitUsage, Code: "no_token", What: "no token given",
				Fix: "pipe the token in: echo \"$GLOSSA_TOKEN\" | glossa login --token-stdin"}
		}
		return strings.TrimSpace(line), nil
	}
	tok, err := inv.env.ReadSecret("API token (Studio → Settings → API tokens): ")
	return strings.TrimSpace(tok), err
}

// verifyToken asks the server which tenant the token belongs to.
func (inv *invocation) verifyToken(ctx context.Context, server, tok string) (tenantJSON, error) {
	c, err := newClientWithToken(inv, server, tok)
	if err != nil {
		return tenantJSON{}, err
	}
	tenants, err := c.Tenants(ctx)
	if err != nil {
		return tenantJSON{}, inv.apiError(err, "the server didn't accept the token")
	}
	if len(tenants) == 0 {
		return tenantJSON{}, &Error{Exit: ExitNetwork, Code: "no_tenant", What: "the token has no tenant"}
	}
	t := tenants[0]
	return tenantJSON{ID: t.Id, Slug: t.Slug, Name: t.Name, Kind: string(t.Kind)}, nil
}

// ── logout ──────────────────────────────────────────────────────────

func runLogout(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("logout [--server URL]\n\nForgets the stored credential. A device sign-in (glossa login --device) is also\nended on the server, so the session is dead and not only forgotten.")
	server := fs.String("server", "", "glossa-server URL (default: glossa.yaml's server)")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	srv, err := inv.serverFor(*server)
	if err != nil {
		return err
	}
	store, err := inv.credentialStore()
	if err != nil {
		return err
	}
	revoked, revokeErr := inv.revokeDeviceSession(ctx, store, srv)
	err = store.Delete(srv)
	removed := err == nil
	if err != nil && !errors.Is(err, credentials.ErrNotFound) {
		return &Error{Exit: ExitUsage, Code: "store_failed", What: "can't remove the token", Where: store.Name(), Why: err.Error()}
	}
	out := map[string]any{"schema": "glossa.cli.logout/v1", "server": srv, "removed": removed, "revoked": revoked}
	if revokeErr != nil {
		out["revoke_error"] = revokeErr.Error()
	}
	return inv.emit(out, func(p *printer) {
		if removed {
			p.line("%s Removed the credential for %s", p.pass(), srv)
			if revoked {
				p.line("  the device session is ended on the server")
			}
			if revokeErr != nil {
				p.line("  %s couldn't end the session on the server (%v); it lapses at its expiry, or end it with sign out everywhere in Studio", p.warn("!"), revokeErr)
			}
		} else {
			p.line("No token was stored for %s", srv)
		}
	})
}

// ── whoami ──────────────────────────────────────────────────────────

type whoamiJSON struct {
	Schema      string       `json:"schema"`
	Server      string       `json:"server"`
	Token       string       `json:"token"`
	TokenSource string       `json:"token_source"`
	Tenant      tenantJSON   `json:"tenant"`
	Project     *projectJSON `json:"project,omitempty"`
	// Kind is "api_token", "ci_token", "context_token" or
	// "device_session"; Person and ExpiresAt are a device session's.
	Kind   string      `json:"kind"`
	Person *personJSON `json:"person,omitempty"`
}

type projectJSON struct {
	ID           string `json:"id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	SourceLocale string `json:"source_locale"`
}

func runWhoami(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("whoami [--server URL]")
	server := fs.String("server", "", "glossa-server URL (default: glossa.yaml's server)")
	if _, err := inv.parse(fs, args); err != nil {
		return err
	}
	srv, err := inv.serverFor(*server)
	if err != nil {
		return err
	}
	tok, source, err := inv.token(srv)
	if err != nil {
		return err
	}
	tenant, err := inv.verifyToken(ctx, srv, tok)
	if err != nil {
		return err
	}
	out := whoamiJSON{Schema: "glossa.cli.whoami/v1", Server: srv, Token: redact(tok), TokenSource: source, Tenant: tenant, Kind: credentialKind(tok)}
	if isDeviceSession(tok) {
		out.Person = inv.personOf(ctx, srv, tok)
	}
	if cfg, err := inv.loadConfig(); err == nil && *server == "" {
		if p, err := inv.connectWith(ctx, cfg); err == nil {
			out.Project = &projectJSON{ID: p.info.Id, Slug: p.info.Slug, Name: p.info.Name, SourceLocale: p.info.SourceLocale}
		}
	}
	return inv.emit(out, func(p *printer) {
		p.line("server   %s", srv)
		p.line("tenant   %s (%s, %s)", tenant.Name, tenant.Slug, tenant.ID)
		if out.Person != nil {
			p.line("person   %s", out.Person.label())
		}
		p.line("token    %s  %s", redact(tok), p.dim("from "+source))
		if out.Kind == "device_session" {
			p.line("kind     device session %s", p.dim("(you, not an API token; glossa logout ends it)"))
		}
		if out.Project != nil {
			p.line("project  %s (%s, source %s)", out.Project.Name, out.Project.Slug, out.Project.SourceLocale)
		}
	})
}

// redact keeps a credential recognizable without revealing it: its
// prefix, which says what kind it is, and four characters.
func redact(tok string) string {
	for _, prefix := range []string{"glossa_api_", "glossa_ci_", "glossa_ctx_", devTokenPrefix} {
		if strings.HasPrefix(tok, prefix) && len(tok) > len(prefix)+4 {
			return tok[:len(prefix)+4] + "…"
		}
	}
	return "…"
}
