package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// Where the two-sighting rule's state lives (RFC 0005 §5.2).
//
// The rule needs one thing: what the *previous* capture of each (route,
// viewport, locale) found. `glossa capture --check` keeps it next to the
// policy cache, in the workspace the capture runs in — which in the
// product's CI is the workspace of the job that captures, so the two
// consecutive captures the rule talks about are two consecutive runs of
// that job.
//
// It is a record, not a source of truth. Losing it costs one promotion:
// every fingerprint is a first sighting again and every visual finding
// is a warning, which is the safe direction. That is why a record that
// cannot be read is not an error — unlike the policy cache, which is a
// run's grading and whose loss must stop the run.
//
// It follows that a CI job with a throwaway workspace promotes nothing:
// every run is a first sighting, every visual finding is a warning, and
// a policy that raises the visual layer to error never bites. Caching
// `.glossa/` between runs of the capture job is what a product does
// about that locally. The other answer is the server's: uploaded
// captures carry their findings per build (RFC 0005 §13 wave 4's
// Capture API), and a check that reads them from there reads them
// already promoted. This file is what lets `glossa capture --check`
// decide the same way with nothing uploaded.

// sightingsPath is where the last capture's fingerprints are kept,
// relative to glossa.yaml.
const sightingsPath = ".glossa/visual-sightings.json"

// sightingsSchema is the record's schema.
const sightingsSchema = "glossa.visual-sightings/v1"

// sightingsFile is .glossa/visual-sightings.json.
//
// It names the server, the project and the application it was written
// for: another application's captures are not this one's sightings, and
// a record copied between checkouts is not evidence of anything.
type sightingsFile struct {
	Schema      string      `json:"schema"`
	Server      string      `json:"server"`
	Project     string      `json:"project"`
	Application string      `json:"application"`
	CapturedAt  time.Time   `json:"captured_at"`
	Scopes      layers.Seen `json:"scopes"`
}

// readSightings reads what the previous capture of this application
// found. Anything it cannot make sense of — no file, another
// application's, a schema it does not know, JSON it cannot parse — is
// no sightings, and every finding of this run is a first sighting.
func (inv *invocation) readSightings(cfg *config.Config, application string) layers.Seen {
	body, err := os.ReadFile(cfg.Resolve(sightingsPath)) //nolint:gosec // a path from the project's own config
	if err != nil {
		return layers.Seen{}
	}
	var f sightingsFile
	if err := json.Unmarshal(body, &f); err != nil || f.Schema != sightingsSchema {
		return layers.Seen{}
	}
	if f.Application != application || !sightingsFor(f, cfg) {
		return layers.Seen{}
	}
	if f.Scopes == nil {
		return layers.Seen{}
	}
	return f.Scopes
}

// sightingsFor reports whether the record is this project's on this
// server.
func sightingsFor(f sightingsFile, cfg *config.Config) bool {
	if f.Server != "" && cfg.Server != "" && f.Server != cfg.Server {
		return false
	}
	return f.Project == "" || cfg.Project == "" || f.Project == cfg.Project
}

// writeSightings leaves this capture's fingerprints where the next run
// can find them. A workspace that cannot be written to is not a reason
// to fail a check that has already been decided, so the write is best
// effort; it costs the next run its promotions and nothing else.
func (inv *invocation) writeSightings(cfg *config.Config, application string, seen layers.Seen) {
	if seen == nil {
		seen = layers.Seen{}
	}
	body, err := json.MarshalIndent(sightingsFile{
		Schema: sightingsSchema, Server: cfg.Server, Project: cfg.Project, Application: application,
		CapturedAt: time.Now().UTC(), Scopes: seen,
	}, "", "  ")
	if err != nil {
		return
	}
	path := cfg.Resolve(sightingsPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, append(body, '\n'), 0o644)
}
