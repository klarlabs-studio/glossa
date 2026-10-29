package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// glossa projects and glossa tenants (issues #67 and #68): the
// workspace's projects and the tenants the credential can act in,
// through listProjects, createProject, addLocale and listTenants. They
// need a server and a credential, and a tenant; not a project that
// already exists, so a new repository can create its own.

const (
	projectsListSchema   = "glossa.cli.projects.list/v1"
	projectsCreateSchema = "glossa.cli.projects.create/v1"
	tenantsSchema        = "glossa.cli.tenants/v1"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ── output shapes ───────────────────────────────────────────────────

type projectRowJSON struct {
	ID           string   `json:"id"`
	Slug         string   `json:"slug"`
	Name         string   `json:"name"`
	SourceLocale string   `json:"source_locale"`
	Locales      []string `json:"locales"`
}

type projectsListDoc struct {
	Schema   string           `json:"schema"`
	Tenant   string           `json:"tenant"`
	Projects []projectRowJSON `json:"projects"`
}

type projectsCreateDoc struct {
	Schema       string         `json:"schema"`
	Tenant       string         `json:"tenant"`
	Project      projectRowJSON `json:"project"`
	LocalesAdded []string       `json:"locales_added"`
}

type tenantJSON2 struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type tenantsDoc struct {
	Schema  string        `json:"schema"`
	Tenants []tenantJSON2 `json:"tenants"`
}

// ── projects ────────────────────────────────────────────────────────

const projectsUsage = `projects list | create --name NAME --slug SLUG --source-locale L [flags]

  glossa projects list
  glossa projects create --name "Brotwerk" --slug brotwerk --source-locale de --locales de,en

list shows the projects of the configured tenant (tenant in glossa.yaml, a slug or an ID).
create makes a project there (it needs catalog.write) and adds the extra --locales; it prints
the new project's ID. Set project in glossa.yaml to its slug afterwards.`

type projectsArgs struct {
	action, name, slug, sourceLocale, idempotencyKey string
	locales                                          string
}

func runProjects(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags(projectsUsage)
	var a projectsArgs
	fs.StringVar(&a.name, "name", "", "create: the project's display name")
	fs.StringVar(&a.slug, "slug", "", "create: the project's slug (lowercase letters, digits and -)")
	fs.StringVar(&a.sourceLocale, "source-locale", "", "create: the source locale, fixed at creation (e.g. de)")
	fs.StringVar(&a.locales, "locales", "", "create: locales to add besides the source locale, comma-separated")
	fs.StringVar(&a.idempotencyKey, "idempotency-key", "", "create: the Idempotency-Key (default: a new one per invocation)")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || (pos[0] != "list" && pos[0] != "create") {
		return usageError(inv.name, "missing or unknown action: list or create")
	}
	a.action = pos[0]
	if a.action == "list" {
		return inv.projectsList(ctx)
	}
	return inv.projectsCreate(ctx, a)
}

// tenantClient loads the config, authenticates and resolves the tenant.
func (inv *invocation) tenantClient(ctx context.Context) (*remote.Client, string, error) {
	cfg, err := inv.loadConfig()
	if err != nil {
		return nil, "", err
	}
	c, _, err := inv.client(cfg.Server)
	if err != nil {
		return nil, "", err
	}
	tenant, err := inv.resolveTenant(ctx, c, cfg)
	return c, tenant, err
}

func (inv *invocation) projectsList(ctx context.Context) error {
	c, tenant, err := inv.tenantClient(ctx)
	if err != nil {
		return err
	}
	ps, err := c.Projects(ctx, tenant)
	if err != nil {
		return inv.apiError(err, "can't list projects")
	}
	out := projectsListDoc{Schema: projectsListSchema, Tenant: tenant, Projects: []projectRowJSON{}}
	for _, p := range ps {
		ls, err := c.Locales(ctx, remote.Scope{Tenant: tenant, Project: p.Id})
		if err != nil {
			return inv.apiError(err, fmt.Sprintf("can't list the locales of %s", p.Slug))
		}
		remote.SortLocales(ls)
		out.Projects = append(out.Projects, toProjectJSON(p, ls))
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Projects) == 0 {
			pr.line("no projects yet: `glossa projects create --name NAME --slug SLUG --source-locale L`")
			return
		}
		rows := [][]string{{"ID", "SLUG", "NAME", "SOURCE", "LOCALES"}}
		for _, p := range out.Projects {
			rows = append(rows, []string{p.ID, p.Slug, p.Name, p.SourceLocale, strings.Join(p.Locales, ",")})
		}
		pr.table(rows)
	})
}

func toProjectJSON(p remote.Project, ls []remote.ProjectLocale) projectRowJSON {
	out := projectRowJSON{ID: p.Id, Slug: string(p.Slug), Name: p.Name, SourceLocale: p.SourceLocale, Locales: []string{}}
	for _, l := range ls {
		out.Locales = append(out.Locales, l.Code)
	}
	return out
}

func (inv *invocation) projectsCreate(ctx context.Context, a projectsArgs) error {
	if strings.TrimSpace(a.name) == "" || a.slug == "" || a.sourceLocale == "" {
		return usageError(inv.name, "create needs --name, --slug and --source-locale")
	}
	if !slugPattern.MatchString(a.slug) {
		return &Error{Exit: ExitUsage, Code: "invalid_slug", What: fmt.Sprintf("%q is not a slug", a.slug),
			Fix: "use lowercase letters, digits and -, e.g. brotwerk"}
	}
	source, err := normalizeLocale(inv, "--source-locale", a.sourceLocale)
	if err != nil {
		return err
	}
	locales, err := normalizeLocales(inv, "--locales", []string{a.locales})
	if err != nil {
		return err
	}
	key := a.idempotencyKey
	if key == "" {
		key = newIdempotencyKey()
	}
	c, tenant, err := inv.tenantClient(ctx)
	if err != nil {
		return err
	}
	p, err := c.CreateProject(ctx, tenant, a.slug, strings.TrimSpace(a.name), source, key)
	if err != nil {
		return inv.createProjectError(err, a)
	}
	scope := remote.Scope{Tenant: tenant, Project: p.Id}
	added := []string{}
	for _, l := range locales {
		if l == p.SourceLocale {
			continue
		}
		if _, _, err := c.AddLocale(ctx, scope, l); err != nil {
			e := asError(inv.apiError(err, fmt.Sprintf("created project %s (%s) but can't add the locale %s", p.Slug, p.Id, l)))
			e.Fix = fmt.Sprintf("the project exists; add the locales to it (addLocale, or Studio). Added so far: %s", orDefault(strings.Join(added, ", "), "none"))
			return e
		}
		added = append(added, l)
	}
	out := projectsCreateDoc{Schema: projectsCreateSchema, Tenant: tenant, LocalesAdded: added,
		Project: projectRowJSON{ID: p.Id, Slug: string(p.Slug), Name: p.Name, SourceLocale: p.SourceLocale,
			Locales: append([]string{p.SourceLocale}, added...)}}
	return inv.emit(out, func(pr *printer) {
		pr.line("%s created project %s (%s), source locale %s", pr.pass(), out.Project.Slug, out.Project.ID, out.Project.SourceLocale)
		if len(added) > 0 {
			pr.line("  locales added: %s", strings.Join(added, ", "))
		}
		pr.line("  %s", pr.dim("set project: "+out.Project.Slug+" in glossa.yaml"))
	})
}

// createProjectError explains a failed createProject by the operation's
// problem codes.
func (inv *invocation) createProjectError(err error, a projectsArgs) error {
	var ae *remote.APIError
	if !errors.As(err, &ae) {
		return asError(err)
	}
	e := asError(inv.apiError(err, fmt.Sprintf("can't create project %s", a.slug)))
	switch {
	case ae.Status == 409:
		e.What = fmt.Sprintf("a project %q already exists", a.slug)
		e.Why = "slugs are unique within a tenant (" + orDefault(ae.Code, "slug_taken") + detail(ae) + ")"
		e.Fix = "choose another --slug; `glossa projects list` shows the taken ones"
	case ae.Status == 403:
		e.Why = "the credential may not create projects (" + orDefault(ae.Code, "forbidden") + detail(ae) + ")"
		e.Fix = "creating a project needs catalog.write in the tenant: use a credential that has it, or ask an owner"
	case ae.Status == 400 || ae.Status == 422:
		e.Exit = ExitUsage
		e.Why = orDefault(ae.Detail, ae.Code)
		e.Fix = "check --name, --slug and --source-locale"
	}
	return e
}

// ── tenants ─────────────────────────────────────────────────────────

const tenantsUsage = `tenants

  glossa tenants

Lists every tenant the credential can act in: a person's active memberships (glossa login
--device), or an API token's own tenant. Put one's slug or ID in glossa.yaml as tenant.`

func runTenants(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags(tenantsUsage)
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageError(inv.name, "takes no arguments")
	}
	cfg, err := inv.loadConfig()
	if err != nil {
		return err
	}
	c, _, err := inv.client(cfg.Server)
	if err != nil {
		return err
	}
	ts, err := c.Tenants(ctx)
	if err != nil {
		return inv.apiError(err, "can't list tenants")
	}
	out := tenantsDoc{Schema: tenantsSchema, Tenants: []tenantJSON2{}}
	for _, t := range ts {
		out.Tenants = append(out.Tenants, tenantJSON2{ID: t.Id, Slug: t.Slug, Name: t.Name, Kind: string(t.Kind)})
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Tenants) == 0 {
			pr.line("the credential can't act in any tenant")
			return
		}
		rows := [][]string{{"ID", "SLUG", "NAME", "KIND"}}
		for _, t := range out.Tenants {
			rows = append(rows, []string{t.ID, t.Slug, t.Name, t.Kind})
		}
		pr.table(rows)
	})
}
