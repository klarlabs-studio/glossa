package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/mcp/adapters/mcpgo"
)

// MCP is off by default, and a deployment that has not opted in must not
// have an agent surface at all — not a 401, not an empty tool list:
// nothing registered (RFC 0005 §15, and config.MCP's own reasoning).
func TestMCPIsAbsentUntilEnabled(t *testing.T) {
	tests := []struct {
		name       string
		cfg        config.MCP
		wantServed bool
	}{
		{name: "off by default", cfg: config.MCP{}, wantServed: false},
		{
			name:       "served when enabled",
			cfg:        config.MCP{Enabled: true, SessionTimeout: time.Minute, Rate: 10, Burst: 10},
			wantServed: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// A nil pool and a nil Identity service are enough to build
			// the endpoint: nothing here touches either until a tool runs.
			h, err := newMCP(tc.cfg, nil, nil, nil, nil, prometheus.NewRegistry(), nil, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatalf("newMCP: %v", err)
			}
			if served := h != nil; served != tc.wantServed {
				t.Fatalf("handler built = %t, want %t", served, tc.wantServed)
			}

			mux := http.NewServeMux()
			if h != nil {
				mux.Handle(mcpgo.Path, h)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, mcpgo.Path, nil))

			if !tc.wantServed {
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404 for an unregistered path", rec.Code)
				}
				return
			}
			// Served, and still closed: no credential, no session.
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 without a bearer token", rec.Code)
			}
		})
	}
}
