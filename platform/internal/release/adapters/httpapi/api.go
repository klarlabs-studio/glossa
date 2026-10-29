// Package httpapi is Release's HTTP edge: its operations of the
// generated /v1 strict server (environments, releases, diffs, bundles,
// signing keys, delivery keys).
package httpapi

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// API serves Release's operations.
type API struct{ svc *app.Service }

// New returns the API.
func New(svc *app.Service) *API { return &API{svc: svc} }

func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, mapError(app.ErrNotFound)
	}
	return id, nil
}

func projectPath(ctx context.Context, project uuid.UUID, sub string) string {
	t, _ := tenancy.FromContext(ctx)
	return "/v1/tenants/" + t.String() + "/projects/" + project.String() + sub
}

func optionalID(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	return apiconv.Ptr(id.String())
}

// ── environments ────────────────────────────────────────────────────

func toPolicy(p domain.Policy) apiv1.EnvironmentPolicy {
	states := make([]apiv1.EnvironmentPolicyStates, len(p.States))
	for i, s := range p.States {
		states[i] = apiv1.EnvironmentPolicyStates(s)
	}
	return apiv1.EnvironmentPolicy{States: states, IncludeOutdated: p.IncludeOutdated}
}

func fromPolicy(p apiv1.EnvironmentPolicy) domain.Policy {
	states := make([]string, len(p.States))
	for i, s := range p.States {
		states[i] = string(s)
	}
	return domain.Policy{States: states, IncludeOutdated: p.IncludeOutdated}
}

func toEnvironment(e domain.Environment) apiv1.Environment {
	out := apiv1.Environment{
		Name: e.Name, Kind: apiv1.EnvironmentKind(e.Kind), Policy: toPolicy(e.Policy),
		CurrentReleaseId: optionalID(e.Current), CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
	if e.Branch != "" {
		out.Branch = apiconv.Ptr(e.Branch)
	}
	if e.Approval != nil {
		out.Approval = apiconv.Ptr(toApprovalPolicy(*e.Approval))
	}
	return out
}

func (a *API) ListEnvironments(ctx context.Context, req apiv1.ListEnvironmentsRequestObject) (apiv1.ListEnvironmentsResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	envs, next, err := a.svc.ListEnvironments(ctx, project, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListEnvironments200JSONResponse{Items: make([]apiv1.Environment, len(envs)), NextPageToken: next}
	for i, e := range envs {
		out.Items[i] = toEnvironment(e)
	}
	return out, nil
}

func (a *API) CreateEnvironment(ctx context.Context, req apiv1.CreateEnvironmentRequestObject) (apiv1.CreateEnvironmentResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	var policy *domain.Policy
	if req.Body.Policy != nil {
		p := fromPolicy(*req.Body.Policy)
		policy = &p
	}
	e, err := a.svc.CreateEnvironment(ctx, project, req.Body.Name, policy)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CreateEnvironment201JSONResponse{Body: toEnvironment(e), Headers: apiv1.CreateEnvironment201ResponseHeaders{
		ETag: apiconv.ETag(e.Version), Location: apiconv.Ptr(projectPath(ctx, project, "/environments/"+e.Name)),
	}}, nil
}

func (a *API) GetEnvironment(ctx context.Context, req apiv1.GetEnvironmentRequestObject) (apiv1.GetEnvironmentResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	e, err := a.svc.GetEnvironment(ctx, project, req.Environment)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetEnvironment200JSONResponse{Body: toEnvironment(e), Headers: apiv1.GetEnvironment200ResponseHeaders{ETag: apiconv.ETag(e.Version)}}, nil
}

func (a *API) UpdateEnvironment(ctx context.Context, req apiv1.UpdateEnvironmentRequestObject) (apiv1.UpdateEnvironmentResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	// The approval requirement changes beside the policy, under the same
	// If-Match and in the same transaction (RFC 0006 §5.1).
	approval, err := approvalChange(*req.Body)
	if err != nil {
		return nil, err
	}
	e, err := a.svc.ConfigureEnvironment(ctx, project, req.Environment, ifMatch, fromPolicy(req.Body.Policy), approval)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.UpdateEnvironment200JSONResponse{Body: toEnvironment(e), Headers: apiv1.UpdateEnvironment200ResponseHeaders{ETag: apiconv.ETag(e.Version)}}, nil
}

func (a *API) PromoteRelease(ctx context.Context, req apiv1.PromoteReleaseRequestObject) (apiv1.PromoteReleaseResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	release, err := uuid.Parse(req.Body.ReleaseId)
	if err != nil {
		return nil, mapError(app.ErrReleaseNotInProject)
	}
	// A promote is gated exactly as a publish is, and overridable on
	// exactly the same terms: only with a reason, which the deployment
	// records. A gate the API could refuse but nobody could override
	// gets routed around by switching the requirement off, which leaves
	// no record at all (RFC 0005 §4.1).
	in := app.PromoteInput{Force: req.Body.Force != nil && *req.Body.Force}
	if req.Body.ForceReason != nil {
		in.ForceReason = *req.Body.ForceReason
	}
	e, err := a.svc.Promote(ctx, project, req.Environment, release, in)
	if held, ok := heldRequest(err); ok {
		// Held for approval: nothing moved, and the answer says so
		// rather than showing the environment as if it had.
		return apiv1.PromoteRelease202JSONResponse{Body: releaseHeld(held),
			Headers: apiv1.PromoteRelease202ResponseHeaders{Location: apiconv.Ptr(requestPath(ctx, held))}}, nil
	}
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.PromoteRelease200JSONResponse{Body: toEnvironment(e), Headers: apiv1.PromoteRelease200ResponseHeaders{ETag: apiconv.ETag(e.Version)}}, nil
}

func (a *API) RollbackEnvironment(ctx context.Context, req apiv1.RollbackEnvironmentRequestObject) (apiv1.RollbackEnvironmentResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	var target *uuid.UUID
	if req.Body.ReleaseId != nil {
		id, err := uuid.Parse(*req.Body.ReleaseId)
		if err != nil {
			return nil, mapError(app.ErrReleaseNotInProject)
		}
		target = &id
	}
	e, err := a.svc.Rollback(ctx, project, req.Environment, target)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.RollbackEnvironment200JSONResponse{Body: toEnvironment(e), Headers: apiv1.RollbackEnvironment200ResponseHeaders{ETag: apiconv.ETag(e.Version)}}, nil
}

func (a *API) ListDeployments(ctx context.Context, req apiv1.ListDeploymentsRequestObject) (apiv1.ListDeploymentsResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	deps, next, err := a.svc.ListDeployments(ctx, project, req.Environment, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListDeployments200JSONResponse{Items: make([]apiv1.Deployment, len(deps)), NextPageToken: next}
	for i, d := range deps {
		out.Items[i] = apiv1.Deployment{
			Number: d.Number, ReleaseId: d.ReleaseID.String(), PreviousReleaseId: optionalID(d.Previous),
			Action: apiv1.DeploymentAction(d.Action), Author: d.By, CreatedAt: d.CreatedAt,
			Forced: d.Override.Forced,
		}
		// The reason is present exactly when the deployment was forced.
		// Recording an override nobody can read back would make the
		// history look policed while the exception stayed invisible,
		// which is worse than no gate at all (RFC 0005 §4.1).
		if d.Override.Forced {
			out.Items[i].ForceReason = apiconv.Ptr(d.Override.Reason)
		}
	}
	return out, nil
}

// ── releases ────────────────────────────────────────────────────────

func toLocales(c domain.Content) []apiv1.ReleaseLocale {
	locales := make([]apiv1.ReleaseLocale, len(c.Locales))
	for i, l := range c.Locales {
		locales[i] = apiv1.ReleaseLocale{Code: l.Code, Direction: apiv1.Direction(l.Direction)}
	}
	return locales
}

func toCounts(st domain.Stats) apiv1.ReleaseCounts {
	counts := apiv1.ReleaseCounts{
		Messages: st.Messages, Artifacts: st.Artifacts, Bytes: int(st.Bytes),
		NewArtifacts: st.NewArtifacts, Locales: map[string]apiv1.ReleaseLocaleCounts{},
	}
	for code, s := range st.Locales {
		counts.Locales[code] = apiv1.ReleaseLocaleCounts{Messages: s.Messages, Outdated: s.Outdated}
	}
	return counts
}

func toLocaleDiffs(ds []domain.LocaleDiff) []apiv1.LocaleDiff {
	out := make([]apiv1.LocaleDiff, len(ds))
	for i, l := range ds {
		out[i] = apiv1.LocaleDiff{Locale: l.Locale, Added: l.Added, Changed: l.Changed, Removed: l.Removed}
	}
	return out
}

func toRelease(r domain.Release) apiv1.Release {
	out := apiv1.Release{
		Id: r.ID.String(), Version: r.Version, ParentId: optionalID(r.Parent), Environment: r.Environment,
		Policy: toPolicy(r.Policy), ManifestDigest: r.Digest, SourceLocale: r.Content.SourceLocale,
		Locales: toLocales(r.Content), Counts: toCounts(r.Stats), Author: r.Author, CreatedAt: r.CreatedAt,
	}
	if r.Note != "" {
		out.Note = apiconv.Ptr(r.Note)
	}
	return out
}

func (a *API) ListReleases(ctx context.Context, req apiv1.ListReleasesRequestObject) (apiv1.ListReleasesResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	rels, next, err := a.svc.ListReleases(ctx, project, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListReleases200JSONResponse{Items: make([]apiv1.Release, len(rels)), NextPageToken: next}
	for i, r := range rels {
		out.Items[i] = toRelease(r)
	}
	return out, nil
}

func (a *API) PublishRelease(ctx context.Context, req apiv1.PublishReleaseRequestObject) (apiv1.PublishReleaseResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	var key string
	if req.Params.IdempotencyKey != nil {
		key = *req.Params.IdempotencyKey
	}
	in := app.PublishInput{Environment: req.Body.Environment,
		Force: req.Body.Force != nil && *req.Body.Force}
	if req.Body.Note != nil {
		in.Note = *req.Body.Note
	}
	if req.Body.ForceReason != nil {
		in.ForceReason = *req.Body.ForceReason
	}
	r, replayed, err := a.svc.Publish(ctx, project, in, key)
	if held, ok := heldRequest(err); ok {
		// The release is recorded and immutable; the environment still
		// serves what it served until the request is approved (§5.1).
		h := apiv1.PublishRelease202ResponseHeaders{Location: apiconv.Ptr(requestPath(ctx, held))}
		if replayed {
			h.IdempotentReplayed = apiconv.Ptr("true")
		}
		return apiv1.PublishRelease202JSONResponse{Body: releaseHeld(held), Headers: h}, nil
	}
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.PublishRelease201ResponseHeaders{Location: apiconv.Ptr(projectPath(ctx, project, "/releases/"+r.ID.String()))}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.PublishRelease201JSONResponse{Body: toRelease(r), Headers: h}, nil
}

func (a *API) GetRelease(ctx context.Context, req apiv1.GetReleaseRequestObject) (apiv1.GetReleaseResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.Release)
	if err != nil {
		return nil, err
	}
	r, err := a.svc.GetRelease(ctx, project, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetRelease200JSONResponse(toRelease(r)), nil
}

func (a *API) GetReleaseDiff(ctx context.Context, req apiv1.GetReleaseDiffRequestObject) (apiv1.GetReleaseDiffResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.Release)
	if err != nil {
		return nil, err
	}
	var base *uuid.UUID
	if req.Params.Base != nil {
		b, err := uuid.Parse(*req.Params.Base)
		if err != nil {
			return nil, mapError(app.ErrReleaseNotInProject)
		}
		base = &b
	}
	d, err := a.svc.Diff(ctx, project, id, base)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetReleaseDiff200JSONResponse{ReleaseId: d.Head.ID.String(), BaseReleaseId: optionalID(d.Base.ID),
		Locales: toLocaleDiffs(d.Locales)}, nil
}

func (a *API) PreviewRelease(ctx context.Context, req apiv1.PreviewReleaseRequestObject) (apiv1.PreviewReleaseResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	pv, err := a.svc.PreviewPublish(ctx, project, req.Environment)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.PreviewRelease200JSONResponse{
		Environment: pv.Environment.Name, Policy: toPolicy(pv.Environment.Policy), BaseReleaseId: optionalID(pv.Base.ID),
		Releasable: pv.Releasable(), Problems: make([]apiv1.ReleaseProblem, len(pv.Problems)),
	}
	for i, p := range pv.Problems {
		out.Problems[i] = apiv1.ReleaseProblem{Code: apiv1.NotReleasable, Detail: p.Detail, Key: optional(p.Key), Locale: optional(p.Locale)}
	}
	if !pv.Releasable() {
		return out, nil
	}
	counts, locales, changes := toCounts(pv.Built.Stats), toLocales(pv.Built.Content), toLocaleDiffs(pv.Changes)
	out.ManifestDigest, out.SourceLocale = &pv.Digest, &pv.Built.Content.SourceLocale
	out.Counts, out.Locales, out.Changes = &counts, &locales, &changes
	return out, nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// rawJSON answers 200 with exact bytes. The generated responses would
// re-encode the manifest and artifact through a map, and a bundle must
// hold the bytes their signature and hashes cover.
type rawJSON []byte

func (b rawJSON) write(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(b)
	return err
}

// VisitGetReleaseManifestResponse implements apiv1.GetReleaseManifestResponseObject.
func (b rawJSON) VisitGetReleaseManifestResponse(w http.ResponseWriter) error { return b.write(w) }

// VisitGetReleaseArtifactResponse implements apiv1.GetReleaseArtifactResponseObject.
func (b rawJSON) VisitGetReleaseArtifactResponse(w http.ResponseWriter) error { return b.write(w) }

func (a *API) GetReleaseManifest(ctx context.Context, req apiv1.GetReleaseManifestRequestObject) (apiv1.GetReleaseManifestResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.Release)
	if err != nil {
		return nil, err
	}
	body, err := a.svc.ReleaseManifest(ctx, project, id, req.Params.Environment)
	if err != nil {
		return nil, mapError(err)
	}
	return rawJSON(body), nil
}

func (a *API) GetReleaseArtifact(ctx context.Context, req apiv1.GetReleaseArtifactRequestObject) (apiv1.GetReleaseArtifactResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.Release)
	if err != nil {
		return nil, err
	}
	body, err := a.svc.ReleaseArtifact(ctx, project, id, req.Digest)
	if err != nil {
		return nil, mapError(err)
	}
	return rawJSON(body), nil
}

func (a *API) ListReleaseSigningKeys(ctx context.Context, req apiv1.ListReleaseSigningKeysRequestObject) (apiv1.ListReleaseSigningKeysResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	keys, err := a.svc.SigningKeys(ctx, project)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListReleaseSigningKeys200JSONResponse{Keys: make([]apiv1.SigningKey, len(keys))}
	for i, k := range keys {
		out.Keys[i] = apiv1.SigningKey{KeyId: k.ID, Algorithm: apiv1.Ed25519, PublicKey: k.Encoded(), Active: k.Active}
	}
	return out, nil
}

// ── delivery keys ───────────────────────────────────────────────────

func toDeliveryKey(k domain.DeliveryKey) apiv1.DeliveryKey {
	return apiv1.DeliveryKey{
		Id: k.ID.String(), Name: k.Name, Key: k.Key, Scope: toScope(k.Scope), CreatedBy: k.CreatedBy,
		CreatedAt: k.CreatedAt, RevokedAt: k.RevokedAt,
	}
}

func toScope(s delivery.Scope) apiv1.DeliveryKeyScope {
	envs := s.Environments
	if envs == nil {
		envs = []string{}
	}
	return apiv1.DeliveryKeyScope{Environments: envs, Branches: s.Branches}
}

func fromScope(s apiv1.DeliveryKeyScope) delivery.Scope {
	return delivery.Scope{Environments: s.Environments, Branches: s.Branches}
}

func (a *API) ListDeliveryKeys(ctx context.Context, req apiv1.ListDeliveryKeysRequestObject) (apiv1.ListDeliveryKeysResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	keys, next, err := a.svc.ListDeliveryKeys(ctx, project, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListDeliveryKeys200JSONResponse{Items: make([]apiv1.DeliveryKey, len(keys)), NextPageToken: next}
	for i, k := range keys {
		out.Items[i] = toDeliveryKey(k)
	}
	return out, nil
}

func (a *API) CreateDeliveryKey(ctx context.Context, req apiv1.CreateDeliveryKeyRequestObject) (apiv1.CreateDeliveryKeyResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	var idem string
	if req.Params.IdempotencyKey != nil {
		idem = *req.Params.IdempotencyKey
	}
	// Without a scope, a new key reads production only (RFC 0004 §4.3).
	in := app.NewDeliveryKey{Name: req.Body.Name}
	if req.Body.Scope != nil {
		in.Scope = apiconv.Ptr(fromScope(*req.Body.Scope))
	}
	k, replayed, err := a.svc.CreateDeliveryKey(ctx, project, in, idem)
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.CreateDeliveryKey201ResponseHeaders{Location: apiconv.Ptr(projectPath(ctx, project, "/delivery-keys/"+k.ID.String()))}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.CreateDeliveryKey201JSONResponse{Body: toDeliveryKey(k), Headers: h}, nil
}

func (a *API) SetDeliveryKeyScope(ctx context.Context, req apiv1.SetDeliveryKeyScopeRequestObject) (apiv1.SetDeliveryKeyScopeResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.DeliveryKey)
	if err != nil {
		return nil, err
	}
	k, err := a.svc.ChangeDeliveryKeyScope(ctx, project, id, fromScope(*req.Body))
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.SetDeliveryKeyScope200JSONResponse(toDeliveryKey(k)), nil
}

func (a *API) RevokeDeliveryKey(ctx context.Context, req apiv1.RevokeDeliveryKeyRequestObject) (apiv1.RevokeDeliveryKeyResponseObject, error) {
	project, err := parseID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := parseID(req.DeliveryKey)
	if err != nil {
		return nil, err
	}
	if err := a.svc.RevokeDeliveryKey(ctx, project, id); err != nil {
		return nil, mapError(err)
	}
	return apiv1.RevokeDeliveryKey204Response{}, nil
}
