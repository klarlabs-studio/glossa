package glossa

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
)

// Staged rollout (runtimes/SPEC.md §1.4): a signed manifest may carry a
// candidate release for a share of installations. The runtime decides the
// side from a cohort key — the process's installation id, or a request's
// key from WithCohortKey — and activates the candidate view or the stable
// view of the same manifest.

const cohortBuckets = 10000

var (
	saltPattern           = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	percentPattern        = regexp.MustCompile(`^(0|[1-9][0-9]{0,2})$`)
	installationIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// Side is the release view a cohort key selects under a rollout.
type Side string

// Sides of a rollout.
const (
	// SideStable is the manifest's top-level release.
	SideStable Side = "stable"
	// SideCandidate is the rollout's candidate release.
	SideCandidate Side = "candidate"
)

// RolloutInfo is what Explain reports about a rollout (SPEC §6).
type RolloutInfo struct {
	// ID names the rollout.
	ID string `json:"id"`
	// Percent is the share of cohorts in the candidate, 0–100.
	Percent int `json:"percent"`
	// Cohort is the cohort key's cohort, 0–9999: the installation's, or
	// the request's when it carries a key (WithCohortKey).
	Cohort int `json:"cohort"`
	// Side is the view actually rendered from. SideStable with
	// Cohort < Percent×100 is a candidate that failed to activate.
	Side Side `json:"side"`
}

// rollout is a manifest's valid `rollout` member.
type rollout struct {
	id      string
	percent int
	salt    string
	// candidate is the candidate view: the manifest with the candidate's
	// release, locales, fallback and artifacts. Not yet validated.
	candidate *manifest
}

// in reports whether cohort is in the candidate.
func (r *rollout) in(cohort int) bool { return cohort < r.percent*100 }

// rolloutViews are the views a client loaded under one rollout. Either may
// be nil: a view that failed to load, or one nothing selects.
type rolloutViews struct {
	spec              *rollout
	stable, candidate *release
}

// cohortOf computes the SPEC §1.4 cohort, 0–9999, of key under a rollout's
// salt: the first four bytes of SHA-256(UTF-8(salt) ‖ UTF-8(key)) as a
// big-endian unsigned integer, mod 10000. The key is hashed as given,
// without case folding or Unicode normalization.
func cohortOf(salt, key string) int {
	sum := sha256.Sum256([]byte(salt + key))
	return int(binary.BigEndian.Uint32(sum[:4]) % cohortBuckets)
}

type cohortKey struct{}

// WithCohortKey returns a copy of ctx carrying the cohort key that decides
// a request's side of a staged rollout (SPEC §1.4), typically a stable user
// or account ID. A server process serves many users, so without one a
// whole process would be in or out of a rollout. Client.T, Client.Explain
// and Client.Localizer read it; an empty key means the process's
// installation id. Set Config.PerRequestCohorts so the client loads both
// sides of a rollout.
func WithCohortKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, cohortKey{}, key)
}

// CohortKeyFrom returns the cohort key on ctx, or "".
func CohortKeyFrom(ctx context.Context) string {
	key, _ := ctx.Value(cohortKey{}).(string)
	return key
}

// parseRollout reads a manifest's `rollout` member. It returns nil when
// there is none, and an errSchema when it doesn't match the schema: an
// invalid rollout is ignored (SPEC §1.4).
func parseRollout(m *manifest) (*rollout, error) {
	if len(m.Rollout) == 0 || string(m.Rollout) == "null" {
		return nil, nil
	}
	var raw struct {
		ID        string                     `json:"id"`
		Percent   json.RawMessage            `json:"percent"`
		Salt      string                     `json:"salt"`
		Candidate map[string]json.RawMessage `json:"candidate"`
	}
	if err := json.Unmarshal(m.Rollout, &raw); err != nil {
		return nil, fmt.Errorf("%w: rollout: %v", errSchema, err)
	}
	percent, err := strconv.Atoi(string(raw.Percent))
	switch {
	case raw.ID == "":
		return nil, fmt.Errorf("%w: rollout: id is required", errSchema)
	case !percentPattern.Match(raw.Percent) || err != nil || percent > 100:
		return nil, fmt.Errorf("%w: rollout %s: percent %s is not an integer from 0 to 100", errSchema, raw.ID, raw.Percent)
	case !saltPattern.MatchString(raw.Salt):
		return nil, fmt.Errorf("%w: rollout %s: salt is not 22 base64url characters", errSchema, raw.ID)
	}
	// The candidate's members start empty: unmarshalling into the maps and
	// slices the copy shares with m would merge into, or overwrite, the
	// stable release's.
	view := *m
	view.Rollout, view.Release, view.Locales, view.Fallback, view.Artifacts = nil, ReleaseRef{}, nil, nil, nil
	members := []struct {
		name string
		into any
	}{{"release", &view.Release}, {"locales", &view.Locales}, {"fallback", &view.Fallback}, {"artifacts", &view.Artifacts}}
	for _, mem := range members {
		b, ok := raw.Candidate[mem.name]
		if !ok {
			return nil, fmt.Errorf("%w: rollout %s: candidate has no %s", errSchema, raw.ID, mem.name)
		}
		if err := json.Unmarshal(b, mem.into); err != nil {
			return nil, fmt.Errorf("%w: rollout %s: candidate %s: %v", errSchema, raw.ID, mem.name, err)
		}
	}
	return &rollout{id: raw.ID, percent: percent, salt: raw.Salt, candidate: &view}, nil
}

// rolloutOf is m's rollout as this client sees it: nil when rollout
// support is off, when there is none, or when it is invalid (reported).
func (c *Client) rolloutOf(m *manifest) *rollout {
	if c.cfg.DisableRollout {
		return nil
	}
	ro, err := parseRollout(m)
	if err != nil {
		c.reporter.report(Error{Type: ErrorSchema, Detail: err.Error(), ReleaseID: m.Release.ID})
	}
	return ro
}

// assembleRollout loads the views of a manifest under rollout ro: the side
// the installation id selects, and with PerRequestCohorts both. A
// candidate that can't be activated is reported, and the installation
// gets the stable view of the same manifest (SPEC §1.4). The stable side
// never fetches a candidate artifact.
func (c *Client) assembleRollout(ctx context.Context, m *manifest, ro *rollout, raw []byte, etag string,
	origin Source, lend map[string]catalog,
) (*snapshot, error) {
	cohort := cohortOf(ro.salt, c.installationID())
	inCandidate := ro.in(cohort)
	views := &rolloutViews{spec: ro}
	if inCandidate || c.cfg.PerRequestCohorts {
		cand, err := c.candidateView(ctx, ro, raw, etag, origin, lend)
		if err != nil {
			c.reportLoad(err)
		} else {
			views.candidate = cand
			lend = mergeCatalogs(lend, cand.bySHA)
		}
	}
	var stableErr error
	if !inCandidate || views.candidate == nil || c.cfg.PerRequestCohorts {
		views.stable, stableErr = c.assembleView(ctx, m, raw, etag, origin, lend)
	}
	rel := views.stable
	if inCandidate && views.candidate != nil {
		rel = views.candidate
	}
	if rel == nil {
		return nil, stableErr
	}
	if stableErr != nil {
		c.reportLoad(stableErr) // only per-request keys on the stable side miss it
	}
	return &snapshot{rel: rel, source: origin, ro: views, cohort: cohort}, nil
}

func (c *Client) candidateView(ctx context.Context, ro *rollout, raw []byte, etag string, origin Source,
	lend map[string]catalog,
) (*release, error) {
	if err := ro.candidate.validate(); err != nil {
		err = fmt.Errorf("%w: rollout %s: candidate: %v", errSchema, ro.id, err)
		return nil, &loadError{releaseID: ro.candidate.Release.ID, err: err}
	}
	return c.assembleView(ctx, ro.candidate, raw, etag, origin, lend)
}

func mergeCatalogs(a, b map[string]catalog) map[string]catalog {
	out := make(map[string]catalog, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// pick returns the view key selects in s and key's cohort. A side whose
// view didn't load falls back to the view the installation has.
func (s *snapshot) pick(key string) (*release, int) {
	cohort := cohortOf(s.ro.spec.salt, key)
	want := s.ro.stable
	if s.ro.spec.in(cohort) && s.ro.candidate != nil {
		want = s.ro.candidate
	}
	if want == nil {
		want = s.rel
	}
	return want, cohort
}

// rolloutInfo is what Explain reports for s: nil without a valid rollout.
func (s *snapshot) rolloutInfo() *RolloutInfo {
	if s.ro == nil {
		return nil
	}
	side := SideStable
	if s.rel == s.ro.candidate {
		side = SideCandidate
	}
	return &RolloutInfo{ID: s.ro.spec.id, Percent: s.ro.spec.percent, Cohort: s.cohort, Side: side}
}

// installationID is the process's cohort key: Config.InstallationID, or a
// random 128-bit id created the first time a rollout is read and kept in
// the cache directory for as long as it survives (SPEC §1.4). Without a
// cache directory it lives as long as the process.
func (c *Client) installationID() string {
	c.idOnce.Do(func() {
		if c.cfg.InstallationID != "" {
			c.id = c.cfg.InstallationID
			return
		}
		id, err := c.store.installationID()
		if err == nil {
			c.id = id
			return
		}
		if !errors.Is(err, fs.ErrNotExist) {
			c.cfg.Logger.Warn("glossa: the persisted installation id is unusable; creating a new one", "error", err)
		}
		c.id = newInstallationID()
		if err := c.store.saveInstallationID(c.id); err != nil {
			c.cfg.Logger.Warn("glossa: persisting the installation id failed", "error", err)
		}
	})
	return c.id
}

func newInstallationID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go 1.24+)
	return hex.EncodeToString(b[:])
}
