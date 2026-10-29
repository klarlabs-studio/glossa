package httpgin

import (
	"context"
	"crypto/sha256"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apierr/ginerr"
	"go.klarlabs.de/glossa/apps/api/internal/domain/apikey"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
	"go.klarlabs.de/glossa/apps/api/internal/errs"
)

// Gin context keys for the API-key auth flow.
const (
	ctxKeyProject  = "glossa.project"
	ctxKeyTenantID = "glossa.tenant_id"
	ctxKeyScope    = "glossa.api_key_scope"
	ctxKeyKeyID    = "glossa.api_key_id"
)

// APIKeyResolver is the narrow port apiKeyAuth needs.
type APIKeyResolver interface {
	ResolveByHash(ctx context.Context, hash []byte) (apikey.Resolution, error)
	Touch(ctx context.Context, id uuid.UUID) error
}

// apiKeyAuth resolves the inbound Bearer token to an api-key
// Resolution (project + tenant + scope), stores it on the gin
// context, and best-effort updates last_used_at.
//
// Runs BEFORE [rlsTxMiddleware] so the tenant ID is available for the
// `SET LOCAL app.current_tenant = '...'` call. The API-key lookup
// itself bypasses RLS — pre-auth we don't know the tenant yet.
func apiKeyAuth(resolver APIKeyResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing or malformed Authorization header (expected: Bearer glossa_...)",
			})
			return
		}
		hash := sha256.Sum256([]byte(raw))
		res, err := resolver.ResolveByHash(c.Request.Context(), hash[:])
		if err != nil {
			// Every lookup failure is 401 — don't leak "key valid but
			// project deleted" vs "no such key" via status-code probing.
			ginerr.Send(c, errs.AuthInvalidAPIKey)
			return
		}
		// Project shim so downstream code that wants a Project value
		// (existing handlers) keeps working.
		p := project.Project{
			ID:            res.ProjectID,
			TenantID:      res.TenantID,
			Slug:          project.Slug(res.ProjectSlug),
			Name:          project.Name(res.ProjectName),
			DefaultLocale: res.DefaultLocale,
		}
		c.Set(ctxKeyProject, p)
		c.Set(ctxKeyTenantID, res.TenantID)
		c.Set(ctxKeyScope, res.Scope)
		c.Set(ctxKeyKeyID, res.KeyID)
		// Best-effort: bumping last_used_at is fire-and-forget so a
		// transient DB hiccup never fails an otherwise valid request.
		_ = resolver.Touch(c.Request.Context(), res.KeyID)
		c.Next()
	}
}

// requireScope returns a middleware that 403s unless the resolved
// API key satisfies the required scope. Mount AFTER [apiKeyAuth].
func requireScope(required apikey.Scope) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := c.Get(ctxKeyScope)
		if !ok {
			ginerr.Send(c, errs.AuthScopeRequiresAPIKey)
			return
		}
		got, _ := v.(apikey.Scope)
		if !got.Allows(required) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "api key scope '" + string(got) + "' cannot perform '" + string(required) + "' operations",
			})
			return
		}
		c.Next()
	}
}

// bearerToken pulls the token portion out of an Authorization header.
// Returns ok=false on any malformed input.
func bearerToken(h string) (string, bool) {
	if h == "" {
		return "", false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}

// authedProject returns the Project that the API-key middleware
// resolved earlier in this request. Panics if called outside a route
// that ran [apiKeyAuth] — that's a wiring bug, not a runtime
// condition we want to handle gracefully.
func authedProject(c *gin.Context) project.Project {
	v, ok := c.Get(ctxKeyProject)
	if !ok {
		panic("authedProject called without apiKeyAuth middleware in the chain")
	}
	return v.(project.Project)
}
