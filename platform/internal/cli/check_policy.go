package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// Where `glossa check` gets the policy it grades against (RFC 0005
// §4.2).
//
// The server is the source of truth. A run that reaches it fetches the
// project's document and leaves it at .glossa/policy.json; a run that
// cannot reach the server grades against that cache and says so; a run
// with neither falls back to the built-in default — every locale
// required, fail_on: error — and says *that*, loudly, because a check
// must never silently grade itself.
//
// The fetch is behind a port for two reasons. It is the seam the policy
// API of RFC 0005 §13 wave 3 slots into without touching the command:
// today the document comes from the project's settings, which is all a
// server can say, and tomorrow from `GET …/check-policy` with its
// rules, environments and version. And it is what lets the resolution
// be tested without a server.

// policyCachePath is where a fetched policy is kept, relative to
// glossa.yaml. It is a cache and not a source: what it holds came from
// the server, and a developer editing it changes only their own
// offline runs, never what CI decides.
const policyCachePath = ".glossa/policy.json"

// policyCacheSchema is the cache file's schema.
const policyCacheSchema = "glossa.check-policy-cache/v1"

// policySource fetches the project's check-policy document.
type policySource interface {
	FetchPolicy(ctx context.Context) (checkpolicy.Policy, error)
}

// policyOrigin says where the policy a run graded against came from,
// which the output prints: a run may never leave that implicit.
type policyOrigin string

const (
	// policyFromServer: fetched from the project, this run.
	policyFromServer policyOrigin = "server"
	// policyFromCache: .glossa/policy.json, because the server was out
	// of reach or the run is offline.
	policyFromCache policyOrigin = "cache"
	// policyFromDefault: no server and no cache, so the built-in
	// default stands.
	policyFromDefault policyOrigin = "default"
)

// resolvedPolicy is the document a run grades against and where it came
// from.
type resolvedPolicy struct {
	Policy checkpolicy.Policy
	Origin policyOrigin
	// FetchedAt is when the cache was written, for a cached policy.
	FetchedAt time.Time
}

// policyCache is .glossa/policy.json.
//
// It names the server and project it was fetched for: a cache copied
// between checkouts, or left behind by a project that was re-pointed,
// is not this project's policy and is ignored rather than trusted.
type policyCache struct {
	Schema    string             `json:"schema"`
	Server    string             `json:"server"`
	Project   string             `json:"project"`
	FetchedAt time.Time          `json:"fetched_at"`
	Policy    checkpolicy.Policy `json:"policy"`
}

// projectPolicySource reads the project's check-policy document from
// the server.
//
// It asks `GET …/check-policy`, which answers the whole document —
// rules, environments and the version that decided the run. A project
// that never saved one reads as version 0, whose three base fields mean
// exactly what they meant before M4, so nothing about an untouched
// project changes.
//
// A server that predates the endpoint answers 404. That is not a
// failure: the built-in default stands and the run says the policy is
// its own, the same answer this gave before the endpoint existed.
type projectPolicySource struct {
	client *remote.Client
	scope  remote.Scope
	// settings is the project's stored check_policy, read from the
	// project itself. It is the fallback for a server too old to have
	// the endpoint.
	settings *apiclient.CheckPolicy
}

func (s projectPolicySource) FetchPolicy(ctx context.Context) (checkpolicy.Policy, error) {
	p, err := s.client.CheckPolicy(ctx, s.scope)
	if err == nil {
		return p, nil
	}
	var ae *remote.APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
		return checkpolicy.Policy{}, err
	}
	return s.policyFromSettings()
}

// policyFromSettings reads the three fields of settings.check_policy,
// which is all a server without the endpoint can say.
func (s projectPolicySource) policyFromSettings() (checkpolicy.Policy, error) {
	cp := s.settings
	if cp == nil {
		// A server that predates the setting too. The built-in default
		// stands, and the run says the policy is its own.
		return checkpolicy.Policy{}, errNoStoredPolicy
	}
	p := checkpolicy.Policy{
		FailOn:              checkpolicy.Severity(cp.FailOn),
		MissingTranslations: checkpolicy.Severity(cp.MissingTranslations),
	}
	switch cp.RequireComplete {
	case apiclient.CheckPolicyRequireCompleteNone:
		p.RequireComplete = []string{}
	case apiclient.CheckPolicyRequireCompleteListed:
		p.RequireComplete = []string{}
		if cp.Locales != nil {
			p.RequireComplete = append(p.RequireComplete, *cp.Locales...)
		}
	}
	return p, nil
}

// errNoStoredPolicy is a project with no policy document, which is a
// server that predates the setting rather than a failure.
var errNoStoredPolicy = errors.New("cli: the project stores no check policy")

// fetchPolicy asks src for the project's policy and caches it.
//
// A project with no stored policy leaves the built-in default standing
// and writes no cache: caching a document nobody wrote would make the
// next offline run report a policy the project never had.
func (inv *invocation) fetchPolicy(ctx context.Context, cfg *config.Config, src policySource) (resolvedPolicy, error) {
	p, err := src.FetchPolicy(ctx)
	if errors.Is(err, errNoStoredPolicy) {
		return resolvedPolicy{Origin: policyFromDefault}, nil
	}
	if err != nil {
		return resolvedPolicy{}, err
	}
	now := time.Now().UTC()
	inv.writePolicyCache(cfg, policyCache{
		Schema: policyCacheSchema, Server: cfg.Server, Project: cfg.Project, FetchedAt: now, Policy: p,
	})
	return resolvedPolicy{Policy: p, Origin: policyFromServer, FetchedAt: now}, nil
}

// writePolicyCache leaves the fetched document where the next run can
// find it. A workspace that cannot be written to is not a reason to
// fail a check the server already graded, so the write is best effort;
// the run's own verdict never depends on it.
func (inv *invocation) writePolicyCache(cfg *config.Config, c policyCache) {
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	path := cfg.Resolve(policyCachePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, append(body, '\n'), 0o644)
}

// cachedPolicy reads .glossa/policy.json. It reports false when there
// is no cache, or none for this server and project.
//
// A cache that exists and cannot be read is an error, and a usage one
// (exit 2): the policy could not be resolved, and a run that cannot
// resolve its policy has no verdict to give. Distinguishing that from a
// failed check is the point of exit 2 — a broken policy is not a clean
// build.
func (inv *invocation) cachedPolicy(cfg *config.Config) (resolvedPolicy, bool, error) {
	path := cfg.Resolve(policyCachePath)
	body, err := os.ReadFile(path) //nolint:gosec // a path from the project's own config
	if errors.Is(err, fs.ErrNotExist) {
		return resolvedPolicy{}, false, nil
	}
	if err != nil {
		return resolvedPolicy{}, false, invalidPolicyCache(path, err.Error())
	}
	var c policyCache
	if err := json.Unmarshal(body, &c); err != nil {
		return resolvedPolicy{}, false, invalidPolicyCache(path, err.Error())
	}
	if c.Schema != policyCacheSchema {
		return resolvedPolicy{}, false, invalidPolicyCache(path,
			fmt.Sprintf("schema is %q, not %q", c.Schema, policyCacheSchema))
	}
	if !cachedFor(c, cfg) {
		// Another project's or another server's policy. Not an error —
		// the file is simply not about this run — and not used either.
		return resolvedPolicy{}, false, nil
	}
	p, err := c.Policy.Validate(nil)
	if err != nil {
		return resolvedPolicy{}, false, invalidPolicyCache(path, err.Error())
	}
	return resolvedPolicy{Policy: p, Origin: policyFromCache, FetchedAt: c.FetchedAt}, true, nil
}

// cachedFor reports whether the cache is this project's on this server.
// A cache that names neither is one an older CLI wrote, and is taken as
// this project's.
func cachedFor(c policyCache, cfg *config.Config) bool {
	if c.Server != "" && cfg.Server != "" && c.Server != cfg.Server {
		return false
	}
	return c.Project == "" || cfg.Project == "" || c.Project == cfg.Project
}

func invalidPolicyCache(path, why string) error {
	return &Error{
		Exit: ExitUsage, Code: "invalid_policy_cache",
		What: "the cached check policy can't be read", Where: path, Why: why,
		Fix: "delete " + policyCachePath + " and run `glossa check` with the server in reach to fetch it again",
	}
}
