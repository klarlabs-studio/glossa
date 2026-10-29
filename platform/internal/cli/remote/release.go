package remote

import (
	"context"
	"net/http"
	"net/url"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
	"go.klarlabs.de/glossa/platform/internal/cli/release"
)

// ReleaseService is release.Service over the generated /v1 client:
// environments, releases, bundles and delivery keys.
type ReleaseService struct{ c *Client }

var _ release.Service = (*ReleaseService)(nil)

// NewReleaseService returns the Release API of c's server.
func NewReleaseService(c *Client) *ReleaseService { return &ReleaseService{c: c} }

func (r *ReleaseService) project(s release.Scope, sub string, args ...any) string {
	return r.c.path("/v1/tenants/%s/projects/%s"+sub, append([]any{s.Tenant, s.Project}, args...)...)
}

// ── conversions ─────────────────────────────────────────────────────

func toPolicy(p apiclient.EnvironmentPolicy) release.Policy {
	states := make([]string, len(p.States))
	for i, s := range p.States {
		states[i] = string(s)
	}
	return release.Policy{States: states, IncludeOutdated: p.IncludeOutdated}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toEnvironment(e apiclient.Environment) release.Environment {
	return release.Environment{Name: e.Name, CurrentReleaseID: deref(e.CurrentReleaseId), Policy: toPolicy(e.Policy), UpdatedAt: e.UpdatedAt}
}

func toCounts(c apiclient.ReleaseCounts) release.Counts {
	out := release.Counts{Messages: c.Messages, Artifacts: c.Artifacts, NewArtifacts: c.NewArtifacts,
		Bytes: c.Bytes, Locales: make(map[string]release.LocaleCounts, len(c.Locales))}
	for code, lc := range c.Locales {
		out.Locales[code] = release.LocaleCounts{Messages: lc.Messages, Outdated: lc.Outdated}
	}
	return out
}

func toLocaleDiff(l apiclient.LocaleDiff) release.LocaleDiff {
	return release.LocaleDiff{Locale: l.Locale, Added: orEmpty(l.Added), Changed: orEmpty(l.Changed), Removed: orEmpty(l.Removed)}
}

func toRelease(r apiclient.Release) release.Release {
	out := release.Release{
		ID: r.Id, Version: r.Version, Environment: r.Environment, ParentID: deref(r.ParentId), Note: deref(r.Note),
		Author: r.Author, CreatedAt: r.CreatedAt, SourceLocale: r.SourceLocale, ManifestDigest: r.ManifestDigest,
		Policy: toPolicy(r.Policy), Locales: make([]string, len(r.Locales)), Counts: toCounts(r.Counts),
	}
	for i, l := range r.Locales {
		out.Locales[i] = l.Code
	}
	return out
}

func toDeliveryKey(k apiclient.DeliveryKey) release.DeliveryKey {
	return release.DeliveryKey{
		ID: k.Id, Name: k.Name, Key: k.Key, CreatedAt: k.CreatedAt, RevokedAt: k.RevokedAt,
		Scope: release.KeyScope{Environments: k.Scope.Environments, Branches: k.Scope.Branches},
	}
}

func fromKeyScope(s release.KeyScope) apiclient.DeliveryKeyScope {
	envs := s.Environments
	if envs == nil {
		envs = []string{}
	}
	return apiclient.DeliveryKeyScope{Environments: envs, Branches: s.Branches}
}

func mapAll[A, B any](in []A, f func(A) B) []B {
	out := make([]B, len(in))
	for i, a := range in {
		out[i] = f(a)
	}
	return out
}

// ── environments ────────────────────────────────────────────────────

// Environments implements release.Service.
func (r *ReleaseService) Environments(ctx context.Context, s release.Scope) ([]release.Environment, error) {
	size := pageSize
	envs, err := collect(func(tok *string) ([]apiclient.Environment, *string, error) {
		resp, err := r.c.api.ListEnvironmentsWithResponse(ctx, s.Tenant, s.Project, &apiclient.ListEnvironmentsParams{PageSize: &size, PageToken: tok})
		if err := check(resp, err, http.MethodGet, r.project(s, "/environments")); err != nil {
			return nil, nil, err
		}
		return resp.JSON200.Items, resp.JSON200.NextPageToken, nil
	})
	return mapAll(envs, toEnvironment), err
}

// Environment implements release.Service.
func (r *ReleaseService) Environment(ctx context.Context, s release.Scope, name string) (release.Environment, error) {
	resp, err := r.c.api.GetEnvironmentWithResponse(ctx, s.Tenant, s.Project, name)
	if err := check(resp, err, http.MethodGet, r.project(s, "/environments/%s", name)); err != nil {
		return release.Environment{}, err
	}
	return toEnvironment(*resp.JSON200), nil
}

// Promote implements release.Service. Promoting the release already
// served changes nothing, so the request is retried like a read. An
// environment that requires approvals answers 202: the promote is held
// as a release request and the environment is not returned.
func (r *ReleaseService) Promote(ctx context.Context, s release.Scope, releaseID, environment string) (release.Environment, *release.Held, error) {
	resp, err := r.c.api.PromoteReleaseWithResponse(idempotent(ctx), s.Tenant, s.Project, environment, apiclient.Promotion{ReleaseId: releaseID})
	if err := check(resp, err, http.MethodPost, r.project(s, "/environments/%s/promotions", environment)); err != nil {
		return release.Environment{}, nil, err
	}
	if resp.StatusCode() == http.StatusAccepted {
		h, err := toHeld(resp.JSON202, http.MethodPost, r.project(s, "/environments/%s/promotions", environment))
		return release.Environment{}, h, err
	}
	return toEnvironment(*resp.JSON200), nil, nil
}

// Rollback implements release.Service. Without a target a repeated
// rollback goes back further, so only a targeted one is retried.
func (r *ReleaseService) Rollback(ctx context.Context, s release.Scope, environment, toRelease string) (release.Environment, error) {
	body := apiclient.Rollback{}
	if toRelease != "" {
		body.ReleaseId = &toRelease
		ctx = idempotent(ctx)
	}
	resp, err := r.c.api.RollbackEnvironmentWithResponse(ctx, s.Tenant, s.Project, environment, body)
	if err := check(resp, err, http.MethodPost, r.project(s, "/environments/%s/rollbacks", environment)); err != nil {
		return release.Environment{}, err
	}
	return toEnvironment(*resp.JSON200), nil
}

// ── releases ────────────────────────────────────────────────────────

// Releases implements release.Service.
func (r *ReleaseService) Releases(ctx context.Context, s release.Scope, limit int) ([]release.Release, error) {
	size := pageSize
	if limit > 0 && limit < size {
		size = limit
	}
	var out []release.Release
	var tok *string
	for {
		resp, err := r.c.api.ListReleasesWithResponse(ctx, s.Tenant, s.Project, &apiclient.ListReleasesParams{PageSize: &size, PageToken: tok})
		if err := check(resp, err, http.MethodGet, r.project(s, "/releases")); err != nil {
			return nil, err
		}
		out = append(out, mapAll(resp.JSON200.Items, toRelease)...)
		if limit > 0 && len(out) >= limit {
			return out[:limit], nil
		}
		tok = resp.JSON200.NextPageToken
		if tok == nil || *tok == "" {
			return out, nil
		}
	}
}

// Release implements release.Service.
func (r *ReleaseService) Release(ctx context.Context, s release.Scope, id string) (release.Release, error) {
	resp, err := r.c.api.GetReleaseWithResponse(ctx, s.Tenant, s.Project, id)
	if err := check(resp, err, http.MethodGet, r.project(s, "/releases/%s", id)); err != nil {
		return release.Release{}, err
	}
	return toRelease(*resp.JSON200), nil
}

// Diff implements release.Service.
func (r *ReleaseService) Diff(ctx context.Context, s release.Scope, id, base string) (release.Diff, error) {
	params := &apiclient.GetReleaseDiffParams{}
	where := r.project(s, "/releases/%s/diff", id)
	if base != "" {
		params.Base = &base
		where += "?base=" + url.QueryEscape(base)
	}
	resp, err := r.c.api.GetReleaseDiffWithResponse(ctx, s.Tenant, s.Project, id, params)
	if err := check(resp, err, http.MethodGet, where); err != nil {
		return release.Diff{}, err
	}
	d := resp.JSON200
	return release.Diff{ReleaseID: d.ReleaseId, BaseReleaseID: deref(d.BaseReleaseId), Locales: mapAll(d.Locales, toLocaleDiff)}, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Publish implements release.Service. With an idempotency key a retry
// returns the first release, so the request is retried.
func (r *ReleaseService) Publish(ctx context.Context, s release.Scope, req release.PublishRequest) (release.Published, error) {
	params := &apiclient.PublishReleaseParams{}
	if req.IdempotencyKey != "" {
		params.IdempotencyKey = &req.IdempotencyKey
		ctx = idempotent(ctx)
	}
	body := apiclient.PublishRelease{Environment: req.Environment}
	if req.Note != "" {
		body.Note = &req.Note
	}
	resp, err := r.c.api.PublishReleaseWithResponse(ctx, s.Tenant, s.Project, params, body)
	if err := check(resp, err, http.MethodPost, r.project(s, "/releases")); err != nil {
		return release.Published{}, err
	}
	replayed := resp.HTTPResponse.Header.Get("Idempotent-Replayed") == "true"
	if resp.StatusCode() == http.StatusAccepted {
		h, err := toHeld(resp.JSON202, http.MethodPost, r.project(s, "/releases"))
		return release.Published{Held: h, Replayed: replayed}, err
	}
	return release.Published{Release: toRelease(*resp.JSON201), Replayed: replayed}, nil
}

// PreviewPublish implements release.Service. It stores nothing, so it
// is retried like a read.
func (r *ReleaseService) PreviewPublish(ctx context.Context, s release.Scope, environment string) (release.Preview, error) {
	resp, err := r.c.api.PreviewReleaseWithResponse(idempotent(ctx), s.Tenant, s.Project, environment)
	if err := check(resp, err, http.MethodPost, r.project(s, "/environments/%s/release-previews", environment)); err != nil {
		return release.Preview{}, err
	}
	p := resp.JSON200
	out := release.Preview{Environment: p.Environment, Policy: toPolicy(p.Policy), BaseReleaseID: deref(p.BaseReleaseId),
		Releasable: p.Releasable, Problems: mapAll(p.Problems, func(pr apiclient.ReleaseProblem) release.Problem {
			return release.Problem{Code: string(pr.Code), Detail: pr.Detail, Key: deref(pr.Key), Locale: deref(pr.Locale)}
		})}
	if p.Counts != nil && p.Locales != nil {
		out.Release = &release.PreviewRelease{SourceLocale: deref(p.SourceLocale), ManifestDigest: deref(p.ManifestDigest),
			Locales: mapAll(*p.Locales, func(l apiclient.ReleaseLocale) string { return l.Code }), Counts: toCounts(*p.Counts)}
	}
	if p.Changes != nil {
		out.Changes = mapAll(*p.Changes, toLocaleDiff)
	}
	return out, nil
}

// ── delivery keys ───────────────────────────────────────────────────

// DeliveryKeys implements release.Service.
func (r *ReleaseService) DeliveryKeys(ctx context.Context, s release.Scope) ([]release.DeliveryKey, error) {
	size := pageSize
	keys, err := collect(func(tok *string) ([]apiclient.DeliveryKey, *string, error) {
		resp, err := r.c.api.ListDeliveryKeysWithResponse(ctx, s.Tenant, s.Project, &apiclient.ListDeliveryKeysParams{PageSize: &size, PageToken: tok})
		if err := check(resp, err, http.MethodGet, r.project(s, "/delivery-keys")); err != nil {
			return nil, nil, err
		}
		return resp.JSON200.Items, resp.JSON200.NextPageToken, nil
	})
	return mapAll(keys, toDeliveryKey), err
}

// CreateDeliveryKey implements release.Service.
func (r *ReleaseService) CreateDeliveryKey(ctx context.Context, s release.Scope, name string, scope *release.KeyScope, idempotencyKey string) (release.DeliveryKey, error) {
	params := &apiclient.CreateDeliveryKeyParams{}
	if idempotencyKey != "" {
		params.IdempotencyKey = &idempotencyKey
		ctx = idempotent(ctx)
	}
	body := apiclient.CreateDeliveryKey{Name: name}
	if scope != nil {
		sc := fromKeyScope(*scope)
		body.Scope = &sc
	}
	resp, err := r.c.api.CreateDeliveryKeyWithResponse(ctx, s.Tenant, s.Project, params, body)
	if err := check(resp, err, http.MethodPost, r.project(s, "/delivery-keys")); err != nil {
		return release.DeliveryKey{}, err
	}
	return toDeliveryKey(*resp.JSON201), nil
}

// SetDeliveryKeyScope implements release.Service.
func (r *ReleaseService) SetDeliveryKeyScope(ctx context.Context, s release.Scope, id string, scope release.KeyScope) (release.DeliveryKey, error) {
	resp, err := r.c.api.SetDeliveryKeyScopeWithResponse(ctx, s.Tenant, s.Project, id, fromKeyScope(scope))
	if err := check(resp, err, http.MethodPut, r.project(s, "/delivery-keys/%s/scope", id)); err != nil {
		return release.DeliveryKey{}, err
	}
	return toDeliveryKey(*resp.JSON200), nil
}

// RevokeDeliveryKey implements release.Service.
func (r *ReleaseService) RevokeDeliveryKey(ctx context.Context, s release.Scope, id string) error {
	resp, err := r.c.api.RevokeDeliveryKeyWithResponse(ctx, s.Tenant, s.Project, id)
	return check(resp, err, http.MethodDelete, r.project(s, "/delivery-keys/%s", id))
}

// ── bundles ─────────────────────────────────────────────────────────

// BundleSource implements release.Service.
func (r *ReleaseService) BundleSource(s release.Scope, releaseID, environment string) release.BundleSource {
	return bundleSource{r: r, s: s, id: releaseID, env: environment}
}

// bundleSource reads a release's manifest and artifacts as the exact
// bytes the server sent: their hashes and signatures cover those bytes.
type bundleSource struct {
	r       *ReleaseService
	s       release.Scope
	id, env string
}

func (b bundleSource) Manifest(ctx context.Context) ([]byte, error) {
	resp, err := b.r.c.api.GetReleaseManifestWithResponse(ctx, b.s.Tenant, b.s.Project, b.id, &apiclient.GetReleaseManifestParams{Environment: b.env})
	if err := check(resp, err, http.MethodGet, b.r.project(b.s, "/releases/%s/manifest", b.id)+"?environment="+url.QueryEscape(b.env)); err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (b bundleSource) Artifact(ctx context.Context, sha string) ([]byte, error) {
	resp, err := b.r.c.api.GetReleaseArtifactWithResponse(ctx, b.s.Tenant, b.s.Project, b.id, sha)
	if err := check(resp, err, http.MethodGet, b.r.project(b.s, "/releases/%s/artifacts/%s", b.id, sha)); err != nil {
		return nil, err
	}
	return resp.Body, nil
}
