package httpserver

import (
	"log/slog"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/config"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
)

func TestTimeoutsComeFromConfig(t *testing.T) {
	cfg := config.HTTP{
		Addr: ":0", ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 3 * time.Second, IdleTimeout: 4 * time.Second, MaxBodyBytes: 1,
	}
	s := New(cfg, Deps{Logger: slog.New(slog.DiscardHandler), Registry: observability.NewRegistry()})
	h := s.http
	if h.ReadHeaderTimeout != time.Second || h.ReadTimeout != 2*time.Second ||
		h.WriteTimeout != 3*time.Second || h.IdleTimeout != 4*time.Second {
		t.Errorf("timeouts = %v/%v/%v/%v", h.ReadHeaderTimeout, h.ReadTimeout, h.WriteTimeout, h.IdleTimeout)
	}
	if h.MaxHeaderBytes != maxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d", h.MaxHeaderBytes)
	}
}
