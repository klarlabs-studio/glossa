package main

import (
	"crypto/hkdf"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klarlabs-studio/auth-go/aesgcm"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	auditapi "go.klarlabs.de/glossa/platform/internal/audit/adapters/httpapi"
	auditapp "go.klarlabs.de/glossa/platform/internal/audit/app"
	catalogapi "go.klarlabs.de/glossa/platform/internal/catalog/adapters/httpapi"
	contextapi "go.klarlabs.de/glossa/platform/internal/context/adapters/httpapi"
	identityaudit "go.klarlabs.de/glossa/platform/internal/identity/adapters/audit"
	"go.klarlabs.de/glossa/platform/internal/identity/adapters/httpapi"
	"go.klarlabs.de/glossa/platform/internal/identity/adapters/mail"
	"go.klarlabs.de/glossa/platform/internal/identity/adapters/passkey"
	"go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	integrationapi "go.klarlabs.de/glossa/platform/internal/integration/adapters/httpapi"
	intelligenceapi "go.klarlabs.de/glossa/platform/internal/intelligence/adapters/httpapi"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/ratelimit"
	knowledgeapi "go.klarlabs.de/glossa/platform/internal/knowledge/adapters/httpapi"
	localizationapi "go.klarlabs.de/glossa/platform/internal/localization/adapters/httpapi"
	"go.klarlabs.de/glossa/platform/internal/mcp/adapters/mcpgo"
	previewapi "go.klarlabs.de/glossa/platform/internal/preview/adapters/httpapi"
	qualityapi "go.klarlabs.de/glossa/platform/internal/quality/adapters/httpapi"
	releaseapi "go.klarlabs.de/glossa/platform/internal/release/adapters/httpapi"
	workflowapi "go.klarlabs.de/glossa/platform/internal/workflow/adapters/httpapi"
)

// apiServer is the /v1 strict server: every bounded context's handler
// type is embedded here, and together they implement the generated
// apiv1.StrictServerInterface.
type apiServer struct {
	*httpapi.API
	*catalogAPI
	*localizationAPI
	*releaseAPI
	*knowledgeAPI
	*intelligenceAPI
	*integrationAPI
	*contextAPI
	*qualityAPI
	*previewAPI
	*workflowAPI
	*auditAPI
	*metaAPI
}

// Each context names its handler type API; the aliases give the
// embedded fields distinct names.
type (
	catalogAPI      = catalogapi.API
	localizationAPI = localizationapi.API
	releaseAPI      = releaseapi.API
	knowledgeAPI    = knowledgeapi.API
	intelligenceAPI = intelligenceapi.API
	integrationAPI  = integrationapi.API
	contextAPI      = contextapi.API
	qualityAPI      = qualityapi.API
	previewAPI      = previewapi.API
	workflowAPI     = workflowapi.API
	auditAPI        = auditapi.API
)

var _ apiv1.StrictServerInterface = apiServer{}

// apiRoutes mounts the generated /v1 router. Identity's Guard enforces
// each operation's security requirement for every context, and its
// error hooks render every failure as problem details. Its CORS
// middleware sits outside the Guard so a refused request still carries
// the headers the browser needs to show the problem, and its Preflight
// handler answers OPTIONS, which the generated router doesn't register
// (RFC 0004 §5.2).
//
// mcp, when the deployment enables it, is mounted beside /v1 rather
// than inside it: MCP is JSON-RPC over one streamable endpoint, not a
// REST operation, so it is not in the OpenAPI contract and does not go
// through the generated router or the Guard. It authenticates with the
// same tenant API tokens through Identity's own service, and its
// sessions carry the same principal (RFC 0005 §7.2). A nil handler
// registers nothing, so /mcp then answers like any unknown path.
func apiRoutes(identity *httpapi.API, meta *metaAPI, c contexts, mcp http.Handler) func(*http.ServeMux) {
	server := apiServer{API: identity, catalogAPI: c.catalogAPI, localizationAPI: c.localizationAPI, releaseAPI: c.releaseAPI,
		knowledgeAPI: c.knowledgeAPI, intelligenceAPI: c.intelligenceAPI, integrationAPI: c.integrationAPI,
		previewAPI: c.previewAPI, contextAPI: c.contextAPI, qualityAPI: c.qualityAPI, workflowAPI: c.workflowAPI, auditAPI: c.auditAPI,
		metaAPI: meta}
	return func(mux *http.ServeMux) {
		if mcp != nil {
			mux.Handle(mcpgo.Path, mcp)
		}
		if c.auditKeys != nil {
			mux.HandleFunc("GET "+auditKeysPath, c.auditKeys)
		}
		strict := apiv1.NewStrictHandlerWithOptions(server, nil, apiv1.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  identity.RequestError,
			ResponseErrorHandlerFunc: identity.ResponseError,
		})
		mux.HandleFunc("OPTIONS /v1/", identity.Preflight)
		apiv1.HandlerWithOptions(strict, apiv1.StdHTTPServerOptions{
			BaseRouter: mux,
			// The generated wrapper wraps in order, so the last entry is
			// the outermost: CORS runs before the Guard.
			Middlewares:      []apiv1.MiddlewareFunc{identity.Guard, identity.CORS},
			ErrorHandlerFunc: identity.ParamError,
		})
	}
}

// newIdentity wires the Identity context: auth-go's services over
// Postgres adapters, a mailer and passkeys when configured, and the HTTP
// edge.
func newIdentity(
	cfg config.Identity, logger *slog.Logger, pool *pgxpool.Pool, trail auditapp.Recorder,
) (*httpapi.API, *identityapp.Service, error) {
	root := cfg.AuthKey()
	csrfKey, err := deriveKey(root, "csrf")
	if err != nil {
		return nil, nil, err
	}
	totpKey, err := deriveKey(root, "totp-seal")
	if err != nil {
		return nil, nil, err
	}
	cipher, err := aesgcm.New(totpKey)
	if err != nil {
		return nil, nil, fmt.Errorf("identity: TOTP cipher: %w", err)
	}
	mailer, err := newMailer(cfg.Mail, logger)
	if err != nil {
		return nil, nil, err
	}

	uow := db.NewUnitOfWork(pool)
	deps := identityapp.Deps{
		Tx:       postgres.NewTransactor(uow, cipher),
		Sessions: postgres.NewSessionRepo(uow),
		// Device sign-in (RFC 0006 §7.2): the CLI's sessions, and the
		// limits on starting one (per client address) and on looking up
		// and deciding codes (per person).
		DeviceSessions: postgres.NewDeviceSessionRepo(uow),
		DeviceLimits:   ratelimit.New(deviceSignInLimit()),
		SignInLinks:    postgres.NewLinkRepo(uow, postgres.PurposeSignIn),
		ResetLinks:     postgres.NewLinkRepo(uow, postgres.PurposePasswordReset),
		TOTP:           postgres.NewTOTPRepo(uow, cipher),
		LoginAttempts:  postgres.NewLoginAttemptRepo(uow),
		Mailer:         mailer,
		Audit:          identityaudit.New(trail),
		Logger:         logger,
	}
	if cfg.WebAuthn.Enabled() {
		stateKey, err := deriveKey(root, "webauthn-state")
		if err != nil {
			return nil, nil, err
		}
		deps.Passkeys, err = passkey.New(passkey.Config{
			RPID: cfg.WebAuthn.RPID, RPName: cfg.WebAuthn.RPName, Origins: cfg.WebAuthn.Origins, StateKey: stateKey,
		}, postgres.NewPasskeyRepo(uow))
		if err != nil {
			return nil, nil, err
		}
	}
	appCfg := identityapp.DefaultConfig(cfg.StudioURL)
	appCfg.SessionTTL = cfg.SessionTTL
	svc, err := identityapp.New(appCfg, deps)
	if err != nil {
		return nil, nil, err
	}
	api, err := httpapi.New(svc, csrfKey, logger)
	if err != nil {
		return nil, nil, err
	}
	api.SetTrustedProxies(cfg.TrustedProxies)
	return api, svc, nil
}

// deviceSignInLimit bounds device sign-in per key (RFC 0006 §7.2): ten
// a minute with bursts of twenty, per client address for starts and per
// person for user-code look-ups and decisions. A person types a code
// once or twice; at this rate guessing one of 20^8 codes takes longer
// than the fifteen minutes any code lives, by nine orders of magnitude.
func deviceSignInLimit() ratelimit.Config {
	return ratelimit.Config{Rate: 10, Interval: time.Minute, Burst: 20}
}

// deriveKey gives each use of the auth secret its own key (HKDF-SHA256),
// so no two mechanisms ever share key material.
func deriveKey(root []byte, purpose string) ([]byte, error) {
	k, err := hkdf.Key(sha256.New, root, nil, "glossa "+purpose+" v1", 32)
	if err != nil {
		return nil, fmt.Errorf("identity: derive %s key: %w", purpose, err)
	}
	return k, nil
}

// newMailer returns nil when the deployment sends no email: Identity then
// refuses the email flows and lets passwords work unverified.
func newMailer(cfg config.Mail, logger *slog.Logger) (identityapp.Mailer, error) {
	switch cfg.Driver {
	case "none":
		logger.Info("GLOSSA_MAIL_DRIVER=none: no email is sent; magic links and password reset by email are off, password accounts work unverified")
		return nil, nil //nolint:nilnil // no mailer is a valid configuration
	case "log":
		logger.Warn("GLOSSA_MAIL_DRIVER=log: emails, including sign-in links, are written to the log and not sent")
		return mail.Log{Logger: logger}, nil
	}
	return mail.NewSMTP(mail.SMTPConfig{
		Addr: cfg.SMTPAddr, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword.Reveal(),
		From: cfg.From, AllowPlaintext: cfg.SMTPAllowPlaintext,
	})
}
