// Package main wires Glossa's API binary. Loads config, builds the
// logger, opens the DB pool, constructs the use cases, runs the
// optional admin bootstrap, and starts gin.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/felixgeelhaar/fortify/ratelimit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	aitranslatorapp "go.klarlabs.de/glossa/apps/api/internal/app/aitranslator"
	apikeyapp "go.klarlabs.de/glossa/apps/api/internal/app/apikey"
	authapp "go.klarlabs.de/glossa/apps/api/internal/app/auth"
	projectapp "go.klarlabs.de/glossa/apps/api/internal/app/project"
	translationapp "go.klarlabs.de/glossa/apps/api/internal/app/translation"
	keyapp "go.klarlabs.de/glossa/apps/api/internal/app/translationkey"
	"go.klarlabs.de/glossa/apps/api/internal/config"
	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/domain/translationkey"
	aitranslatorinfra "go.klarlabs.de/glossa/apps/api/internal/infra/aitranslator"
	"go.klarlabs.de/glossa/apps/api/internal/infra/secrets"
	"go.klarlabs.de/glossa/apps/api/internal/infra/sqlcadapter"
	"go.klarlabs.de/glossa/apps/api/internal/interfaces/httpgin"
	"go.klarlabs.de/glossa/apps/api/internal/logging"
)

func main() {
	log := logging.New()
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		log.Error("config load failed", slog.Any("err", err))
		os.Exit(1)
	}

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Error("db pool open failed", slog.Any("err", err))
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		log.Error("db ping failed", slog.Any("err", err))
		os.Exit(1)
	}

	queries := db.New(pool)

	tenantRepo := sqlcadapter.NewTenantRepo(queries)
	projectRepo := sqlcadapter.NewProjectRepo(queries)
	localeRepo := sqlcadapter.NewLocaleRepo(queries)
	keyRepo := sqlcadapter.NewKeyRepo(queries)
	translationRepo := sqlcadapter.NewTranslationRepo(queries)
	userRepo := sqlcadapter.NewUserRepo(queries)
	auditRepo := sqlcadapter.NewAuditRepo(queries)
	aiProviderRepo := sqlcadapter.NewAIProviderRepo(queries)
	apiKeyRepo := sqlcadapter.NewAPIKeyRepo(queries)
	analyticsRepo := sqlcadapter.NewAnalyticsRepo(queries)

	// AI translator wiring is optional: only active if a secrets key
	// is configured. Absence disables the feature gracefully — the
	// admin UI surfaces an explanatory message instead of crashing.
	var (
		sealer       httpgin.Sealer
		aiTranslator *aitranslatorinfra.Translator
		aiFanOut     *aitranslatorapp.FanOut
	)
	if cfg.SecretsKeyHex != "" {
		s, err := secrets.New(cfg.SecretsKeyHex)
		if err != nil {
			log.Error("secrets init failed", slog.Any("err", err))
			os.Exit(1)
		}
		sealer = s
		aiTranslator = aitranslatorinfra.New()
		aiFanOut = aitranslatorapp.New(aiProviderRepo, localeRepo, translationRepo, auditRepo, aiTranslator, s, analyticsRepo, log)
	}

	issuer, err := authapp.NewHMACIssuer([]byte(cfg.JWTSigningKey), "glossa", 24*time.Hour)
	if err != nil {
		log.Error("jwt issuer", slog.Any("err", err))
		os.Exit(1)
	}

	createProj := projectapp.NewCreateProject(projectRepo, localeRepo, apiKeyRepo, analyticsRepo)
	issueKey := apikeyapp.NewIssueAPIKey(apiKeyRepo)
	revokeKey := apikeyapp.NewRevokeAPIKey(apiKeyRepo)
	upsertKeys := keyapp.NewUpsertKeys(keyRepo)
	updateTr := translationapp.NewUpdateTranslation(translationRepo)
	listBundle := translationapp.NewListBundle(translationRepo)
	login := authapp.NewLogin(userRepo, issuer)
	discover := authapp.NewDiscoverTenants(userRepo)

	if cfg.BootstrapTenantSlug != "" {
		action, err := authapp.Bootstrap(context.Background(), tenantRepo, userRepo, authapp.BootstrapInput{
			TenantSlug:    cfg.BootstrapTenantSlug,
			TenantName:    nonEmpty(cfg.BootstrapTenantName, cfg.BootstrapTenantSlug),
			AdminEmail:    cfg.BootstrapAdminEmail,
			AdminPassword: cfg.BootstrapAdminPassword,
		})
		if err != nil {
			log.Error("admin bootstrap failed", slog.Any("err", err))
			os.Exit(1)
		}
		log.Info("admin bootstrap", slog.String("action", string(action)))
	}

	hub := translationapp.NewHubRateLimited(translationapp.HubLimits{
		PerTenantPerSecond: 10,
		PerTenantBurst:     20,
	})

	limiter := ratelimit.New(ratelimit.Config{
		Rate:     60,
		Burst:    60,
		Interval: time.Minute,
	})

	// Tighter limiter on /auth/login defends bcrypt against
	// brute force. 5/min per IP with a burst of 10 absorbs the
	// occasional typo without locking the user out.
	loginLimiter := ratelimit.New(ratelimit.Config{
		Rate:     5,
		Burst:    10,
		Interval: time.Minute,
	})

	router := httpgin.New(httpgin.Deps{
		Logger:      log,
		GlobalLimit: limiter,
		LoginLimit:  loginLimiter,
		Pool:        pool,

		CreateProj: createProj,
		IssueKey:   issueKey,
		RevokeKey:  revokeKey,
		UpsertKeys: upsertKeys,
		UpdateTr:   updateTr,
		ListBundle: listBundle,
		Login:      login,
		Discover:   discover,
		Hub:        hub,
		JWTIssuer:  issuer,

		ProjectRepo:  projectRepo,
		APIKeys:      apiKeyRepo,
		Analytics:    analyticsRepo,
		Tenants:      tenantRepo,
		Users:        userRepo,
		Locales:      localeRepo,
		Keys:         keysFinderAdapter{keyRepo},
		Audits:       auditRepo,
		Translations: translationRepo,
		AIProviders:  aiProviderRepo,
		AIFanOut:     aiFanOut,
		AITranslator: aiTranslator,
		Sealer:       sealer,
		CORSOrigins:  cfg.CORSOrigins,
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("glossa-api listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", slog.Any("err", err))
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("shutdown error", slog.Any("err", err))
	}
}

// keysFinderAdapter bridges translationkey.Repository to the
// narrow `keysFinder` port the HTTP translation-update handler
// needs (just the UUID). Wiring concern; lives in main.go.
type keysFinderAdapter struct {
	repo translationkey.Repository
}

func (a keysFinderAdapter) FindByName(ctx context.Context, projectID uuid.UUID, name string) (uuid.UUID, error) {
	n, err := translationkey.NewName(name)
	if err != nil {
		return uuid.Nil, err
	}
	k, err := a.repo.Find(ctx, projectID, n)
	if err != nil {
		return uuid.Nil, err
	}
	return k.ID, nil
}

func nonEmpty(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}
