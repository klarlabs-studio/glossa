package main

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	auditapp "go.klarlabs.de/glossa/platform/internal/audit/app"
	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/ratelimit"
	mcpaudit "go.klarlabs.de/glossa/platform/internal/mcp/adapters/audit"
	mcpidentity "go.klarlabs.de/glossa/platform/internal/mcp/adapters/identity"
	"go.klarlabs.de/glossa/platform/internal/mcp/adapters/mcpgo"
	mcpmetrics "go.klarlabs.de/glossa/platform/internal/mcp/adapters/metrics"
	mcppg "go.klarlabs.de/glossa/platform/internal/mcp/adapters/postgres"
	mcpapp "go.klarlabs.de/glossa/platform/internal/mcp/app"
	mcpdomain "go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// mcpRateInterval is the window GLOSSA_MCP_RATE counts tool calls in.
const mcpRateInterval = time.Minute

// newMCP wires the MCP context (RFC 0005 §7) and returns the handler
// for /mcp, or nil when GLOSSA_MCP_ENABLED is off — in which case
// nothing is registered and the path 404s like any other unknown one.
//
// It is built here rather than in newContexts because its one
// collaborator is Identity: MCP mints no credential, so the whole of
// its authentication is Identity's service, and it shares no state with
// the bounded contexts whose ports its tools will call.
func newMCP(
	cfg config.MCP, identity *identityapp.Service, pool *pgxpool.Pool, tools []mcpapp.Tool,
	trail auditapp.Recorder, reg prometheus.Registerer, tp trace.TracerProvider, logger *slog.Logger,
) (http.Handler, error) {
	if !cfg.Enabled {
		logger.Info("GLOSSA_MCP_ENABLED is off: /mcp is not served")
		return nil, nil //nolint:nilnil // no MCP endpoint is a valid configuration
	}
	metrics := mcpmetrics.New(reg)
	// Every call goes to the MCP ledger and, beside it, to the tenant's
	// audit trail (RFC 0006 §6.1).
	var toTrail mcpapp.Option = func(*mcpapp.Service) {}
	if trail != nil {
		toTrail = mcpapp.WithAudit(mcpaudit.New(trail))
	}
	svc, err := mcpapp.New(
		mcpidentity.New(identity),
		mcpapp.WithTools(tools...),
		mcpapp.WithAudit(mcppg.NewAudit(db.NewUnitOfWork(pool))),
		toTrail,
		mcpapp.WithMetrics(metrics),
		mcpapp.WithLimiter(ratelimit.New(ratelimit.Config{Rate: cfg.Rate, Interval: mcpRateInterval, Burst: cfg.Burst})),
		mcpapp.WithTracerProvider(tp),
		mcpapp.WithLogger(logger),
	)
	if err != nil {
		return nil, err
	}
	h, err := mcpgo.New(svc,
		mcpgo.WithLogger(logger), mcpgo.WithVersion(version()), mcpgo.WithSessionTimeout(cfg.SessionTimeout))
	if err != nil {
		return nil, err
	}
	// The transport owns session lifetime, so it — not the service —
	// answers how many are open right now.
	metrics.TrackOpenSessions(mcpgo.Transport, h.OpenSessions)
	logger.Info("serving MCP", slog.String("path", mcpgo.Path), slog.Int("tools", len(svc.Tools(mcpdomain.ToolsetWrite))),
		slog.Duration("session_timeout", cfg.SessionTimeout), slog.Int("calls_per_minute", cfg.Rate))
	return h, nil
}
