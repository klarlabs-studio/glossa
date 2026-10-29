package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	auditapi "go.klarlabs.de/glossa/platform/internal/audit/adapters/httpapi"
	auditmetrics "go.klarlabs.de/glossa/platform/internal/audit/adapters/metrics"
	auditpg "go.klarlabs.de/glossa/platform/internal/audit/adapters/postgres"
	auditapp "go.klarlabs.de/glossa/platform/internal/audit/app"
	auditdomain "go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	releasedomain "go.klarlabs.de/glossa/platform/internal/release/domain"
)

// newAudit wires the Audit context (RFC 0006 §6) and subscribes its
// projection to every event type. It is built before Identity and MCP,
// which record sign-ins and tool calls through it.
func newAudit(
	pool *pgxpool.Pool, events *outbox.Registry, keys *auditdomain.KeySet, logger *slog.Logger, reg prometheus.Registerer,
) (*auditapp.Service, error) {
	uow := db.NewUnitOfWork(pool)
	svc := auditapp.New(auditpg.NewStore(uow),
		auditapp.WithHistory(outbox.NewHistory(uow)), auditapp.WithExportKeys(keys), auditapp.WithLogger(logger),
		// RFC 0006 §10.1: entries appended, broken chains, export jobs.
		auditapp.WithMetrics(auditmetrics.New(reg)))
	if err := svc.Subscribe(events); err != nil {
		return nil, err
	}
	return svc, nil
}

// newAuditKeys builds the audit export key set from GLOSSA_AUDIT_SIGNING_KEY
// and _RETIRED_KEYS (RFC 0006 §6.2). It runs before anything connects,
// so a key that can't be used stops the server at once. No key is nil:
// config.Load has already refused exports enabled without one, and
// unlike the release key there is no derived development key.
//
// The audit key must not be a release signing key: one key, one
// purpose. A deployment that pasted the same seed into both would let
// whoever holds the delivery plane's key sign audit evidence.
func newAuditKeys(cfg config.Config) (*auditdomain.KeySet, error) {
	if cfg.Audit.SigningKey.IsZero() {
		return nil, nil
	}
	pairs := config.KeyList(cfg.Audit.SigningKey.Reveal())
	active, err := auditdomain.ParseSigningKey(pairs[0][0], pairs[0][1])
	if err != nil {
		return nil, fmt.Errorf("GLOSSA_AUDIT_SIGNING_KEY: %w", err)
	}
	var retired []auditdomain.PublicKey
	for _, kv := range config.KeyList(cfg.Audit.RetiredKeys) {
		k, err := auditdomain.ParsePublicKey(kv[0], kv[1])
		if err != nil {
			return nil, fmt.Errorf("GLOSSA_AUDIT_RETIRED_KEYS: %w", err)
		}
		retired = append(retired, k)
	}
	pub := active.Public().Key
	for _, kv := range config.KeyList(cfg.Release.SigningKeys.Reveal()) {
		rk, err := releasedomain.ParseSigningKey(kv[0], kv[1])
		if err != nil {
			continue // newSigner reports it
		}
		if pub.Equal(rk.Key.Public()) {
			return nil, fmt.Errorf("GLOSSA_AUDIT_SIGNING_KEY: %w: it is the release signing key %q; audit exports need a key of their own",
				auditdomain.ErrInvalidKey, rk.ID)
		}
	}
	keys, err := auditdomain.NewKeySet(active, retired)
	if err != nil {
		return nil, fmt.Errorf("GLOSSA_AUDIT_SIGNING_KEY: %w", err)
	}
	return keys, nil
}

// v0HistoryImporter is Audit's import of v0.3's history (RFC 0006 §7.2).
// The route and the use case were built in parallel against
// app.V0HistoryImporter and meet here; the assertion below makes a
// service that stops implementing it a build failure, not a route that
// quietly answers `audit_import_unavailable`. Nil only without Audit.
var _ auditapp.V0HistoryImporter = (*auditapp.Service)(nil)

func v0HistoryImporter(svc *auditapp.Service) auditapp.V0HistoryImporter {
	if svc == nil {
		return nil
	}
	return svc
}

// auditBackfillRetry is how long the audit backfill waits after a
// failure.
const auditBackfillRetry = time.Minute

// startAuditBackfill projects the outbox's history into the audit trail
// in the background, until it succeeds once (RFC 0006 §6.1, "History").
// Like the key index task it runs on every replica: it is idempotent,
// appends under the same per-tenant lock as live delivery, and skips a
// tenant whose history is already recorded, so after the first run a
// restart costs two counts per tenant.
func (a *app) startAuditBackfill(ctx context.Context) {
	if a.audit == nil {
		return
	}
	go func() {
		for {
			r, err := a.audit.Backfill(ctx)
			if err == nil {
				if r.Recorded > 0 {
					a.logger.InfoContext(ctx, "audit: outbox history backfilled",
						slog.Int("tenants", r.Tenants), slog.Int("events", r.Events), slog.Int("recorded", r.Recorded),
						slog.Int("actor_from_payload", r.ActorFromPayload), slog.Int("actor_unknown", r.Unknown),
						slog.Int("retired_types", r.Retired))
				}
				return
			}
			if ctx.Err() != nil {
				return
			}
			a.logger.WarnContext(ctx, "audit: outbox history not backfilled yet; retrying",
				slog.Any("error", err), slog.Int("recorded_so_far", r.Recorded), slog.Duration("retry_in", auditBackfillRetry))
			select {
			case <-ctx.Done():
				return
			case <-time.After(auditBackfillRetry):
			}
		}
	}()
}

// newAuditAPI wires Audit's HTTP edge (RFC 0006 §6.2): the trail's
// entries, the export jobs and the v0.3 history import. The export jobs
// run only with GLOSSA_AUDIT_EXPORTS_ENABLED and a key (config refuses
// the one without the other); otherwise the routes answer
// `audit_export_unavailable` and worker is nil.
func newAuditAPI(cfg config.Audit, pool *pgxpool.Pool, svc *auditapp.Service, objects objectstore.StreamStore,
	logger *slog.Logger,
) (api *auditapi.API, worker *auditapp.ExportWorker) {
	uow := db.NewUnitOfWork(pool)
	store := auditpg.NewStore(uow)
	keys := svc.ExportKeys()
	exports := auditapp.NewExportService(auditapp.ExportConfig{Enabled: cfg.ExportsEnabled, Retention: cfg.ExportRetention},
		store, auditpg.NewExportJobs(uow), objects, keys, auditapp.WithExportLogger(logger),
		auditapp.WithExportMetrics(svc.Metrics()))
	if cfg.ExportsEnabled && keys != nil {
		worker = auditapp.NewExportWorker(exports, auditpg.NewExportClaimer(uow), auditapp.ExportWorkerConfig{})
	}
	return auditapi.New(v0HistoryImporter(svc), auditapi.WithReads(auditapp.NewReadService(store)),
		auditapi.WithExports(exports)), worker
}

// auditKeysPath is where the deployment publishes the public halves of
// its audit keys (RFC 0006 §6.2). It is outside /v1 and the Guard: no
// tenant, no token, public keys only, and not in openapi.yaml.
const auditKeysPath = "/.well-known/glossa-audit-keys.json"

// auditKeysHandler serves the glossa.audit.keys/1 document: the active
// key first, every retired key after it, so an export signed before a
// rotation still finds its key. A deployment without an audit key
// answers 404 — it has never signed an export.
func auditKeysHandler(keys *auditdomain.KeySet) (http.HandlerFunc, error) {
	if keys == nil {
		return func(w http.ResponseWriter, _ *http.Request) {
			problem.Write(w, http.StatusNotFound, "this deployment has no audit key")
		}, nil
	}
	doc, err := auditdomain.KeyDocument(keys.PublicKeys())
	if err != nil {
		return nil, err
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(doc)
	}, nil
}

// startAuditExports runs the audit export worker until ctx ends; a job
// in progress finishes its current attempt first (bounded by its
// timeout), and an unfinished one is claimed again when its lease ends.
func (a *app) startAuditExports(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if a.auditExports == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		a.auditExports.Run(ctx)
	}()
	return done
}
