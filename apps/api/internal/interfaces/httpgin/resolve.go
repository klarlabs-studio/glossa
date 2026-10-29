package httpgin

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
)

// resolveProject returns the project context for the current
// request. It supports both auth flows:
//
//   - api-key auth (CLI / SDK / consumer): [apiKeyAuth] stashed the
//     full project on the gin context. Return it directly.
//   - JWT admin auth: only the tenant_id is on the context. Read
//     the URL `:slug` parameter, validate it through the domain VO,
//     and look up the project under that tenant. RLS ensures
//     cross-tenant projects are invisible even if a slug collides.
//
// Returns an error so the handler can choose between 404 (no such
// project) and 500 (DB outage); callers should distinguish via
// errors.Is on the repo's ErrNotFound.
// errSlugMismatch is returned when the URL names a project the API key does
// not open. Callers map it to 404, the same as any other unreachable project —
// distinguishing "wrong slug" from "no such project" would let a caller probe
// which slugs exist.
var errSlugMismatch = errors.New("resolve: url slug does not match the api key's project")

func resolveProject(c *gin.Context, projects project.Repository) (project.Project, error) {
	if v, ok := c.Get(ctxKeyProject); ok {
		p, _ := v.(project.Project)

		// The key is the authority on which project this is, but the URL must
		// not contradict it. Without this the segment is decorative: a slug
		// naming another project — or naming nothing at all — still returned
		// 200 with the key's data, so a client pointed at the wrong project
		// had no way to find out. Routes that carry no :slug are unaffected.
		if slug := c.Param("slug"); slug != "" && slug != p.Slug.String() {
			return project.Project{}, errSlugMismatch
		}

		return p, nil
	}
	tenantRaw, ok := c.Get(ctxKeyTenantID)
	if !ok {
		return project.Project{}, errors.New("resolve: no tenant on context — wiring bug")
	}
	tenantID, ok := tenantRaw.(uuid.UUID)
	if !ok {
		return project.Project{}, errors.New("resolve: tenant id wrong type")
	}
	slug, err := project.NewSlug(c.Param("slug"))
	if err != nil {
		return project.Project{}, err
	}
	return projects.Find(contextOf(c), tenantID, slug)
}

// contextOf is a tiny extractor so call sites read like
// `projects.Find(contextOf(c), ...)` instead of
// `c.Request.Context()` peppered everywhere.
func contextOf(c *gin.Context) context.Context { return c.Request.Context() }
