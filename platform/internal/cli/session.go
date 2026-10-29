package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/cli/credentials"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// tokenPattern is the shape of a Glossa API token (platform/README.md).
var tokenPattern = regexp.MustCompile(`^glossa_api_[A-Za-z0-9_-]{43}$`)

// loadConfig finds and reads glossa.yaml.
func (inv *invocation) loadConfig() (*config.Config, error) {
	path := inv.configPath
	if path == "" {
		var err error
		path, err = config.Find(inv.env.Dir)
		if errors.Is(err, config.ErrNotFound) {
			return nil, &Error{Exit: ExitUsage, Code: "config_not_found",
				What:  "no glossa.yaml found",
				Where: inv.env.Dir + " and its parent directories",
				Why:   "every command except init, login and version needs the project file",
				Fix:   "run `glossa init` in the project root, or pass --config <path>"}
		}
		if err != nil {
			return nil, err
		}
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(inv.env.Dir, path)
	}
	cfg, err := config.Load(path, inv.env.Getenv)
	var inv2 *config.InvalidError
	switch {
	case errors.As(err, &inv2):
		where := inv2.Path
		if inv2.Field != "" {
			where += " (" + inv2.Field + ")"
		}
		return nil, &Error{Exit: ExitUsage, Code: "invalid_config", What: "glossa.yaml is invalid",
			Where: where, Why: inv2.Problem, Fix: "edit the file, or re-create it with `glossa init --force`"}
	case errors.Is(err, fs.ErrNotExist):
		return nil, &Error{Exit: ExitUsage, Code: "config_not_found", What: "config file not found",
			Where: path, Fix: "check --config, or run `glossa init`"}
	case err != nil:
		return nil, &Error{Exit: ExitUsage, Code: "invalid_config", What: "can't read glossa.yaml", Where: path, Why: err.Error()}
	}
	return cfg, nil
}

func (inv *invocation) credentialStore() (credentials.Store, error) {
	if inv.env.Credentials != nil {
		return inv.env.Credentials, nil
	}
	p, err := credentials.DefaultPath()
	if err != nil {
		return nil, err
	}
	return credentials.Default(p), nil
}

// token returns the API token for server and where it came from.
func (inv *invocation) token(server string) (tok, source string, err error) {
	if t := strings.TrimSpace(inv.env.getenv("GLOSSA_TOKEN")); t != "" {
		return t, "GLOSSA_TOKEN", nil
	}
	store, err := inv.credentialStore()
	if err != nil {
		return "", "", err
	}
	t, err := store.Get(server)
	if err == nil {
		return t, store.Name(), nil
	}
	return "", "", &Error{Exit: ExitNetwork, Code: "no_token",
		What: "no credential for " + server,
		Why:  "GLOSSA_TOKEN is unset and `glossa login` stored nothing for this server",
		Fix: "run `glossa login` — in GitHub Actions, with `permissions: { id-token: write }`, it needs no secret; " +
			"elsewhere set GLOSSA_TOKEN to a token created in Studio",
		Where: server}
}

func (inv *invocation) userAgent() string {
	return "glossa-cli/" + versionOr(inv.env.Version)
}

// client returns an authenticated client for server.
func (inv *invocation) client(server string) (*remote.Client, string, error) {
	if server == "" {
		return nil, "", &Error{Exit: ExitUsage, Code: "no_server", What: "no server configured",
			Where: "glossa.yaml (server)", Fix: "set server in glossa.yaml, or GLOSSA_SERVER"}
	}
	tok, source, err := inv.token(server)
	if err != nil {
		return nil, "", err
	}
	c, err := newClientWithToken(inv, server, tok)
	return c, source, err
}

// newClientWithToken is a client for server with an explicit token.
func newClientWithToken(inv *invocation, server, tok string) (*remote.Client, error) {
	c, err := remote.New(server, tok, remote.Options{HTTP: inv.env.HTTP, UserAgent: inv.userAgent()})
	if err != nil {
		return nil, &Error{Exit: ExitUsage, Code: "invalid_server", What: "invalid server URL", Where: server, Why: err.Error()}
	}
	return c, nil
}

// project is the configured project on the server.
type project struct {
	cfg    *config.Config
	client *remote.Client
	scope  remote.Scope
	info   remote.Project
}

// connect loads the config, authenticates and resolves the tenant and
// project.
func (inv *invocation) connect(ctx context.Context) (*project, error) {
	cfg, err := inv.loadConfig()
	if err != nil {
		return nil, err
	}
	return inv.connectWith(ctx, cfg)
}

func (inv *invocation) connectWith(ctx context.Context, cfg *config.Config) (*project, error) {
	c, _, err := inv.client(cfg.Server)
	if err != nil {
		return nil, err
	}
	tenant, err := inv.resolveTenant(ctx, c, cfg)
	if err != nil {
		return nil, err
	}
	p, err := c.ResolveProject(ctx, tenant, cfg.Project)
	if err != nil {
		return nil, inv.projectError(err, cfg)
	}
	return &project{cfg: cfg, client: c, scope: remote.Scope{Tenant: tenant, Project: p.Id}, info: p}, nil
}

var tenantIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resolveTenant is the ID of the tenant the config names: a UUID is used
// as written, a slug (or any other ID) is looked up among the tenants the
// credential can act in. No tenant in the config: the credential's only
// tenant.
func (inv *invocation) resolveTenant(ctx context.Context, c *remote.Client, cfg *config.Config) (string, error) {
	if tenantIDPattern.MatchString(cfg.Tenant) {
		return cfg.Tenant, nil
	}
	tenants, err := c.Tenants(ctx)
	if err != nil {
		return "", inv.apiError(err, "can't find the credential's tenants")
	}
	if cfg.Tenant == "" {
		if len(tenants) == 1 {
			return tenants[0].Id, nil
		}
		return "", &Error{Exit: ExitUsage, Code: "tenant_ambiguous", What: "which tenant?",
			Where: cfg.Path + " (tenant)", Why: fmt.Sprintf("the credentials can act in %d tenants", len(tenants)),
			Fix: "set tenant in glossa.yaml (or GLOSSA_TENANT) to a tenant's slug or ID" + tenantChoices(tenants)}
	}
	for _, t := range tenants {
		if t.Id == cfg.Tenant || t.Slug == cfg.Tenant {
			return t.Id, nil
		}
	}
	return "", &Error{Exit: ExitUsage, Code: "tenant_not_found", What: fmt.Sprintf("tenant %q not found", cfg.Tenant),
		Where: cfg.Path + " (tenant)", Why: "the credentials can't act in a tenant with that slug or ID",
		Fix: "set tenant in glossa.yaml (or GLOSSA_TENANT) to a tenant's slug or ID" + tenantChoices(tenants)}
}

const maxTenantChoices = 10

// tenantChoices lists tenants for an error's fix: "; one of: acme (id), …".
func tenantChoices(ts []remote.Tenant) string {
	if len(ts) == 0 {
		return "; the credentials can act in none"
	}
	var parts []string
	for i, t := range ts {
		if i == maxTenantChoices {
			parts = append(parts, fmt.Sprintf("… %d more (`glossa tenants`)", len(ts)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", t.Slug, t.Id))
	}
	return "; one of: " + strings.Join(parts, ", ")
}

func (inv *invocation) projectError(err error, cfg *config.Config) error {
	var nf *remote.ErrProjectNotFound
	if errors.As(err, &nf) {
		return &Error{Exit: ExitUsage, Code: "project_not_found",
			What:  fmt.Sprintf("project %q not found", cfg.Project),
			Where: cfg.Path + " (project)", Why: nf.Error(),
			Fix: "set project to the project's slug or ID (Studio lists them), or GLOSSA_PROJECT"}
	}
	return inv.apiError(err, "can't read the project")
}

// apiError explains a failed request.
func (inv *invocation) apiError(err error, what string) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return err
	}
	e := &Error{Exit: ExitNetwork, Code: ae.Code, What: what, Where: ae.Method + " " + ae.URL, Err: err}
	switch {
	case ae.Status == 0:
		e.Code = "network"
		e.Why = fmt.Sprintf("the server didn't answer: %v", ae.Err)
		e.Fix = "check server in glossa.yaml (or GLOSSA_SERVER) and that glossa-server is running"
	case ae.Status == 401:
		e.Why = "the server refused the API token (" + ae.Code + detail(ae) + ")"
		e.Fix = "run `glossa login` with a valid token, or set GLOSSA_TOKEN"
	case ae.Status == 403 && ae.Code == "grant_project_mismatch":
		e.Why = "the credential is bound to another project (" + detail(ae) + ")"
		e.Fix = "a GitHub Actions credential acts on the project its repository is connected to; " +
			"run `glossa login --project <id>` for the right one, or use an API token"
	case ae.Status == 403:
		e.Why = "the credential may not do this (" + ae.Code + detail(ae) + ")"
		e.Fix = "use a token with the needed scope (write for push and import), created in Studio. " +
			"A GitHub Actions credential only reads and writes its project's catalog"
	case ae.Status == 404:
		e.Why = "not found (" + ae.Code + detail(ae) + ")"
		e.Fix = "check tenant and project in glossa.yaml"
	case ae.Status >= 500:
		e.Why = fmt.Sprintf("the server failed (%d %s%s)", ae.Status, ae.Code, detail(ae))
		e.Fix = "retry; if it persists, check the server's logs"
	default:
		e.Why = fmt.Sprintf("%d %s%s", ae.Status, ae.Code, detail(ae))
	}
	if e.Code == "" {
		e.Code = "request_failed"
	}
	return e
}

func detail(ae *remote.APIError) string {
	if ae.Detail != "" {
		return ": " + ae.Detail
	}
	return ""
}

// prompt asks for a value on an interactive terminal; def is used when
// the answer is empty.
func (inv *invocation) prompt(r *bufio.Reader, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(inv.env.Stderr, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(inv.env.Stderr, "%s: ", label)
	}
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	if v := strings.TrimSpace(line); v != "" {
		return v, nil
	}
	return def, nil
}
