// Package observability wires glossa-server's logs, traces and metrics
// (RFC 0002 §11): bolt as the slog handler, OpenTelemetry for traces,
// Prometheus for metrics, and the HTTP middleware that ties a request's
// log lines, span and metrics together with one correlation ID.
package observability

import (
	"context"
	"io"
	"log/slog"

	"go.klarlabs.de/bolt"
)

// ContextAttrs extracts log attributes from a context, such as the
// tenant ID. The composition root supplies these so this package stays
// independent of the bounded contexts.
type ContextAttrs func(context.Context) []slog.Attr

// NewLogger returns a JSON logger on bolt's slog handler. Every record
// logged with a context gains trace_id/span_id (written by bolt itself
// since v1.7.0), request_id and whatever the extractors return.
func NewLogger(w io.Writer, level slog.Leveler, extractors ...ContextAttrs) *slog.Logger {
	inner := bolt.NewSlogHandler(w, &bolt.SlogHandlerOptions{Level: level})
	return slog.New(&contextHandler{inner: inner, extractors: extractors})
}

// contextHandler adds Glossa's own correlation fields from the context:
// the request ID and whatever the extractors return (tenant, principal).
// Trace correlation is bolt's job; adding it here too would duplicate
// the trace_id/span_id keys.
type contextHandler struct {
	inner      slog.Handler
	extractors []ContextAttrs
}

func (h *contextHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := correlationAttrs(ctx)
	for _, extract := range h.extractors {
		attrs = append(attrs, extract(ctx)...)
	}
	if len(attrs) > 0 {
		r = r.Clone()
		r.AddAttrs(attrs...)
	}
	return h.inner.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{inner: h.inner.WithAttrs(attrs), extractors: h.extractors}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{inner: h.inner.WithGroup(name), extractors: h.extractors}
}

func correlationAttrs(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	if id, ok := RequestIDFromContext(ctx); ok {
		return []slog.Attr{slog.String("request_id", id)}
	}
	return nil
}
