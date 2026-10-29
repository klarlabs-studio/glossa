// Package httpgin is the HTTP delivery layer. Each handler binds
// request shape → use case → response shape. Handlers depend on
// use cases (app/) only; never on the domain or infra directly.
//
// Three auth flows compose here:
//
//   - none           — health probes + JWT login.
//   - api-key Bearer — CLI / SDK / consumer apps. `apiKeyAuth`
//     resolves the project + tenant from the
//     hashed key.
//   - JWT Bearer     — admin SPA + translator UI. `jwtAuth` reads
//     tenant + role + locales out of the token.
//
// Both authed flows are wrapped in `rlsTxMiddleware` so every DB
// query runs in a tx with `SET LOCAL app.current_tenant`.
package httpgin

import (
	"log/slog"

	"github.com/felixgeelhaar/fortify/ratelimit"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	aitranslatorapp "go.klarlabs.de/glossa/apps/api/internal/app/aitranslator"
	apikeyapp "go.klarlabs.de/glossa/apps/api/internal/app/apikey"
	authapp "go.klarlabs.de/glossa/apps/api/internal/app/auth"
	projectapp "go.klarlabs.de/glossa/apps/api/internal/app/project"
	translationapp "go.klarlabs.de/glossa/apps/api/internal/app/translation"
	keyapp "go.klarlabs.de/glossa/apps/api/internal/app/translationkey"
	"go.klarlabs.de/glossa/apps/api/internal/domain/aitranslator"
	"go.klarlabs.de/glossa/apps/api/internal/domain/analytics"
	"go.klarlabs.de/glossa/apps/api/internal/domain/apikey"
	"go.klarlabs.de/glossa/apps/api/internal/domain/audit"
	"go.klarlabs.de/glossa/apps/api/internal/domain/locale"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
	"go.klarlabs.de/glossa/apps/api/internal/domain/tenant"
	"go.klarlabs.de/glossa/apps/api/internal/domain/translation"
	"go.klarlabs.de/glossa/apps/api/internal/domain/user"
)

// Deps carries every dependency the router needs. Constructed once
// in cmd/api/main.go and passed in.
type Deps struct {
	Logger      *slog.Logger
	GlobalLimit ratelimit.RateLimiter

	// LoginLimit is a tighter per-IP limiter applied only to
	// /api/v1/auth/login — defends bcrypt against brute force.
	// Recommended: 5 req/min, burst 10.
	LoginLimit ratelimit.RateLimiter

	// Pool drives rlsTxMiddleware (BEGIN; SET LOCAL …) for every
	// authed request.
	Pool *pgxpool.Pool

	// Use cases.
	CreateProj *projectapp.CreateProject
	IssueKey   *apikeyapp.IssueAPIKey
	RevokeKey  *apikeyapp.RevokeAPIKey
	UpsertKeys *keyapp.UpsertKeys
	UpdateTr   *translationapp.UpdateTranslation
	ListBundle *translationapp.ListBundle
	Login      *authapp.Login
	Discover   *authapp.DiscoverTenants

	// Hub fans translation.updated events from PATCH handlers to
	// SSE subscribers. Single instance per process; swap for a
	// Redis-backed Publisher when we go multi-replica.
	Hub       *translationapp.Hub
	JWTIssuer authapp.TokenIssuer

	// Repos.
	ProjectRepo  project.Repository
	APIKeys      apikey.Repository
	Tenants      tenant.Repository
	Users        user.Repository
	Locales      locale.Repository
	Keys         keysFinder
	Audits       audit.Repository
	Translations translation.Repository
	AIProviders  aitranslator.Repository
	Analytics    analytics.Repository

	// AIFanOut is the optional source-locale fan-out hook. Nil disables
	// AI translation for this process.
	AIFanOut *aitranslatorapp.FanOut

	// AITranslator drives one-shot test calls from the admin UI.
	AITranslator aitranslator.Translator

	// Sealer encrypts/decrypts at-rest provider credentials. Required
	// only when AIProviders is wired; otherwise leave nil.
	Sealer Sealer

	// CORSOrigins lists the cross-origin browser callers allowed to
	// hit the API. Empty = "*" (any origin) — fine for the API-key
	// protected surface, since auth is Bearer-only and no cookies
	// cross the boundary. Pin once you know which consumers you serve.
	CORSOrigins []string
}

// Sealer is the local view of the secrets port that AI provider
// handlers need. Defined here to keep the infra/secrets package out
// of the interfaces layer's import graph.
type Sealer interface {
	Seal(plaintext []byte) (ct, nonce []byte, err error)
	Open(ct, nonce []byte) ([]byte, error)
}

// New builds the gin engine + mounts every route.
func New(d Deps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(gin.Recovery())
	r.Use(slogMiddleware(d.Logger))
	r.Use(corsMiddleware(d.CORSOrigins))
	if d.GlobalLimit != nil {
		r.Use(rateLimitMiddleware(d.GlobalLimit))
	}

	// Probes.
	r.GET("/healthz", healthz)
	r.GET("/readyz", healthz)

	v1 := r.Group("/api/v1")

	// ── Unauthenticated ──────────────────────────────────────────
	loginHandlers := []gin.HandlerFunc{}
	if d.LoginLimit != nil {
		loginHandlers = append(loginHandlers, rateLimitMiddleware(d.LoginLimit))
	}
	loginHandlers = append(loginHandlers, handleLogin(d.Login, d.Tenants))
	v1.POST("/auth/login", loginHandlers...)

	discoverHandlers := []gin.HandlerFunc{}
	if d.LoginLimit != nil {
		discoverHandlers = append(discoverHandlers, rateLimitMiddleware(d.LoginLimit))
	}
	discoverHandlers = append(discoverHandlers, handleDiscoverTenants(d.Discover))
	v1.POST("/auth/discover", discoverHandlers...)

	// ── API-key authed (consumer / CLI / SDK) ────────────────────
	authed := v1.Group("/projects/:slug")
	authed.Use(apiKeyAuth(d.APIKeys))
	authed.Use(rlsTxMiddleware(d.Pool))
	{
		// Read endpoints: read OR write scope.
		readGuarded := authed.Group("")
		readGuarded.Use(requireScope(apikey.ScopeRead))
		readGuarded.GET("/locales", handleListLocales(d.ProjectRepo, d.Locales))
		readGuarded.GET("/locales/:locale/messages", handleListBundle(d.ListBundle, d.ProjectRepo, d.Locales, d.Analytics))
		readGuarded.GET("/sse", handleSSE(d.Hub, 0))

		// Write endpoints: write scope only.
		writeGuarded := authed.Group("")
		writeGuarded.Use(requireScope(apikey.ScopeWrite))
		writeGuarded.POST("/locales", handleCreateLocale(d.ProjectRepo, d.Locales))
		writeGuarded.POST("/keys:scan", handleScanKeys(d.ProjectRepo, d.UpsertKeys, d.Analytics))
		writeGuarded.PATCH("/locales/:locale/keys/:key",
			handlePatchTranslation(d.UpdateTr, d.Translations, d.ProjectRepo, d.Locales, d.Keys, d.Hub, d.Audits, d.AIFanOut, d.Analytics))
	}

	// ── JWT authed (admin SPA / translator UI) ───────────────────
	admin := v1.Group("/admin")
	admin.Use(jwtAuth(d.JWTIssuer))
	admin.Use(rlsTxMiddleware(d.Pool))
	{
		// Identity check used by the SPA on boot.
		admin.GET("/me", handleMe())

		// Project list + create. Create stays admin-only.
		admin.GET("/projects", handleListProjects(d.ProjectRepo))
		adminProjectsCreate := admin.Group("")
		adminProjectsCreate.Use(requireAdmin())
		adminProjectsCreate.POST("/projects", handleCreateProject(d.CreateProj))

		// Per-project admin/translator routes. Translators can hit
		// listBundle + PATCH (PATCH enforces per-locale scoping
		// internally); admin-only ops are nested under requireAdmin.
		proj := admin.Group("/projects/:slug")
		{
			proj.GET("/locales", handleListLocales(d.ProjectRepo, d.Locales))
			proj.GET("/locales/:locale/messages", handleListBundle(d.ListBundle, d.ProjectRepo, d.Locales, d.Analytics))
			proj.PATCH("/locales/:locale/keys/:key",
				handlePatchTranslation(d.UpdateTr, d.Translations, d.ProjectRepo, d.Locales, d.Keys, d.Hub, d.Audits, d.AIFanOut, d.Analytics))
			proj.GET("/sse", handleSSE(d.Hub, 0))

			adminOnly := proj.Group("")
			adminOnly.Use(requireAdmin())
			adminOnly.POST("/locales", handleCreateLocale(d.ProjectRepo, d.Locales))
			// Same wildcard name (`:locale`) as the bundle path so
			// gin's trie doesn't reject the route group. The
			// handler parses the value as a UUID.
			adminOnly.PATCH("/locales/:locale/enabled", handleSetLocaleEnabled(d.Locales))
			adminOnly.DELETE("/locales/:locale", handleDeleteLocale(d.Locales))
			adminOnly.POST("/keys:scan", handleScanKeys(d.ProjectRepo, d.UpsertKeys, d.Analytics))
			adminOnly.POST("/locales/:locale/bulk",
				handleBulkImport(d.ProjectRepo, d.Locales, d.UpsertKeys, d.Keys, d.UpdateTr, d.Translations, d.Hub, d.Audits, d.AIFanOut, d.Analytics))
			adminOnly.GET("/diff", handleBundleDiff(d.ProjectRepo, d.Locales, d.ListBundle))
			adminOnly.GET("/metrics", handleProjectMetrics(d.ProjectRepo, d.Analytics))
			adminOnly.GET("/api-keys", handleListAPIKeys(d.ProjectRepo, d.APIKeys))
			adminOnly.POST("/api-keys", handleIssueAPIKey(d.ProjectRepo, d.IssueKey))
			adminOnly.DELETE("/api-keys/:id", handleRevokeAPIKey(d.RevokeKey))
		}

		// Tenant-level admin endpoints.
		tenantAdmin := admin.Group("")
		tenantAdmin.Use(requireAdmin())
		tenantAdmin.GET("/users", handleListUsers(d.Users))
		tenantAdmin.POST("/users", handleCreateUser(d.Users))
		tenantAdmin.PATCH("/users/:id/locales", handleUpdateUserLocales(d.Users))
		tenantAdmin.DELETE("/users/:id", handleDeleteUser(d.Users))
		tenantAdmin.GET("/audit", handleListAudit(d.Audits))
		tenantAdmin.GET("/metrics", handleTenantMetrics(d.Analytics))

		// AI translator providers — credentials live here so admin-only.
		if d.AIProviders != nil {
			tenantAdmin.GET("/ai-providers", handleListAIProviders(d.AIProviders))
			tenantAdmin.POST("/ai-providers", handleCreateAIProvider(d.AIProviders, d.Sealer, d.AITranslator))
			tenantAdmin.PATCH("/ai-providers/:id", handleUpdateAIProvider(d.AIProviders, d.Sealer))
			tenantAdmin.DELETE("/ai-providers/:id", handleDeleteAIProvider(d.AIProviders))
			if d.AITranslator != nil {
				tenantAdmin.POST("/ai-providers/:id/test", handleAITestProvider(d.AIProviders, d.Sealer, d.AITranslator))
			}
		}
	}

	return r
}

func healthz(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}
