package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const probeTimeout = 2 * time.Second

// Check is one readiness dependency.
type Check struct {
	Name  string
	Probe func(context.Context) error
}

type healthBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// livez reports that the process is up. It checks nothing else, so a
// database outage never gets the pod restarted.
func (s *Server) livez(w http.ResponseWriter, _ *http.Request) {
	writeHealth(w, http.StatusOK, healthBody{Status: "ok"})
}

// readyz reports whether this replica should receive traffic: not
// draining, and every dependency answering. Failure details go to the
// log, not the response.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.draining.Load() {
		writeHealth(w, http.StatusServiceUnavailable, healthBody{Status: "draining"})
		return
	}
	body := healthBody{Status: "ready", Checks: make(map[string]string, len(s.checks))}
	status := http.StatusOK
	for _, c := range s.checks {
		if err := s.probe(r.Context(), c); err != nil {
			body.Checks[c.Name] = "failing"
			body.Status = "unavailable"
			status = http.StatusServiceUnavailable
			continue
		}
		body.Checks[c.Name] = "ok"
	}
	writeHealth(w, status, body)
}

func (s *Server) probe(ctx context.Context, c Check) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	err := c.Probe(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "readiness check failed", slog.String("check", c.Name), slog.Any("error", err))
	}
	return err
}

func writeHealth(w http.ResponseWriter, status int, body healthBody) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
