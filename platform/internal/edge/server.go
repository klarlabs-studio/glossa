package edge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/httpserver"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/configured"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
)

// Run is glossa-edge: it serves the delivery endpoints from the
// configured object storage until ctx is cancelled, then drains. It uses
// the kernel's HTTP server (tracing, correlation IDs, access logs, RED
// metrics, /livez, /readyz, /metrics) and nothing that touches a
// database. listening, when set, receives the bound address.
func Run(ctx context.Context, cfg config.Edge, logger *slog.Logger, version string, listening func(net.Addr)) error {
	store, err := configured.Open(cfg.Storage)
	if err != nil {
		return err
	}
	return Serve(ctx, cfg, store, logger, version, listening)
}

// Serve is Run on an already opened store.
func Serve(ctx context.Context, cfg config.Edge, store objectstore.Reader, logger *slog.Logger, version string, listening func(net.Addr)) error {
	tp, shutdownTP, err := observability.NewTracerProvider(ctx, cfg.OTel, version)
	if err != nil {
		return err
	}
	registry := observability.NewRegistry()
	handler := New(store, Config{
		CacheBytes: cfg.Cache.Bytes, KeyTTL: cfg.Cache.KeyTTL, ManifestTTL: cfg.Cache.ManifestTTL,
	}, logger, registry)
	// Readiness has no storage check on purpose: a storage outage must
	// not pull every edge out of the load balancer while they can still
	// serve what they have cached.
	server := httpserver.New(cfg.HTTP, httpserver.Deps{
		Logger: logger, TracerProvider: tp, Registry: registry, Routes: handler.Routes,
	})
	ln, err := server.Listen()
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "listening", slog.String("addr", ln.Addr().String()), slog.String("storage", cfg.Storage.Driver))
	if listening != nil {
		listening(ln.Addr())
	}
	errc := make(chan error, 1)
	go func() { errc <- server.Serve(ln) }()

	var runErr error
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case runErr = <-errc:
		logger.Error("server failed; shutting down", slog.Any("error", runErr))
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()
	var errs []error
	if err := server.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, err)
	}
	if err := shutdownTP(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("flush traces: %w", err))
	}
	logger.Info("glossa-edge stopped")
	return errors.Join(runErr, errors.Join(errs...))
}
