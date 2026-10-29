// Package httpapi is Identity's HTTP edge: its operations of the
// generated /v1 strict server, the Guard that enforces every operation's
// security requirement from the contract, and the tenancy.Resolver that
// turns /v1/tenants/{tenant} into a verified tenant and principal.
//
// Every bounded context's handlers join the one generated server in the
// composition root; the Guard and error hooks here serve all of them,
// because authentication is Identity's job.
package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// MinCSRFKeyLen is the shortest CSRF key accepted.
const MinCSRFKeyLen = 32

// API serves Identity's operations.
type API struct {
	svc  *app.Service
	reqs map[string]apiv1.Requirement
	cors *corsSurface
	// originLookup answers whether an origin is a registered preview
	// origin, for the CORS middleware. A field so it can be stubbed.
	originLookup func(ctx context.Context, origin string) (bool, error)
	csrfKey      []byte
	// trustedProxies are the proxies whose X-Forwarded-For names the
	// client (SetTrustedProxies).
	trustedProxies []netip.Prefix
	logger         *slog.Logger
	errs           errorWriter
}

// New returns the API. csrfKey derives CSRF tokens from sessions.
func New(svc *app.Service, csrfKey []byte, logger *slog.Logger) (*API, error) {
	if len(csrfKey) < MinCSRFKeyLen {
		return nil, fmt.Errorf("httpapi: CSRF key must be at least %d bytes", MinCSRFKeyLen)
	}
	reqs, err := apiv1.Requirements()
	if err != nil {
		return nil, err
	}
	cors, err := newCORSSurface()
	if err != nil {
		return nil, err
	}
	a := &API{
		svc: svc, reqs: reqs, cors: cors, csrfKey: csrfKey, logger: logger,
		errs: errorWriter{logger: logger},
	}
	a.originLookup = svc.PreviewOriginRegistered
	return a, nil
}

// ResponseError, RequestError and ParamError are the generated server's
// error hooks; wire them for every context's operations.
func (a *API) ResponseError(w http.ResponseWriter, r *http.Request, err error) {
	a.errs.ResponseError(w, r, err)
}

// RequestError reports an undecodable request body.
func (a *API) RequestError(w http.ResponseWriter, r *http.Request, err error) {
	a.errs.RequestError(w, r, err)
}

// ParamError reports parameters that couldn't be bound.
func (a *API) ParamError(w http.ResponseWriter, r *http.Request, err error) {
	a.errs.ParamError(w, r, err)
}

func (a *API) signedIn(in app.SignedIn) apiv1.SessionStartedJSONResponse {
	return apiv1.SessionStartedJSONResponse{
		Body: apiv1.Session{
			Person: toPerson(in.Person), CsrfToken: a.csrfToken(in.SessionToken), ExpiresAt: in.ExpiresAt.UTC(),
		},
		// The session was just issued, so it lives the full lifetime.
		Headers: apiv1.SessionStartedResponseHeaders{
			SetCookie: ptr(sessionCookie(in.SessionToken, a.svc.SessionLifetime())),
		},
	}
}

func unauthorized(code problem.Code, detail string) error {
	return problem.New(http.StatusUnauthorized, code, detail)
}

func badRequest(code problem.Code, detail string) error {
	return problem.New(http.StatusBadRequest, code, detail)
}

// person returns the signed-in person of a session route.
func person(ctx context.Context) (domain.PersonID, error) {
	p, ok := authz.From(ctx)
	if !ok {
		return domain.PersonID{}, app.ErrUnauthenticated
	}
	if p.Person.IsZero() {
		return domain.PersonID{}, app.ErrPersonOnly
	}
	return p.Person, nil
}

func tenantPath(ctx context.Context, sub string) string {
	t, _ := tenancy.FromContext(ctx)
	return "/v1/tenants/" + t.String() + sub
}

// ── auth ────────────────────────────────────────────────────────────

func (a *API) RequestMagicLink(ctx context.Context, req apiv1.RequestMagicLinkRequestObject) (apiv1.RequestMagicLinkResponseObject, error) {
	if err := a.svc.RequestSignInLink(ctx, string(req.Body.Email)); err != nil {
		return nil, err
	}
	return apiv1.RequestMagicLink202Response{}, nil
}

func (a *API) RedeemMagicLink(ctx context.Context, req apiv1.RedeemMagicLinkRequestObject) (apiv1.RedeemMagicLinkResponseObject, error) {
	in, err := a.svc.RedeemSignInLink(ctx, req.Body.Token)
	if err != nil {
		return nil, err
	}
	return apiv1.RedeemMagicLink200JSONResponse{SessionStartedJSONResponse: a.signedIn(in)}, nil
}

func (a *API) Register(ctx context.Context, req apiv1.RegisterRequestObject) (apiv1.RegisterResponseObject, error) {
	var name string
	if req.Body.DisplayName != nil {
		name = *req.Body.DisplayName
	}
	if err := a.svc.Register(ctx, string(req.Body.Email), req.Body.Password, name); err != nil {
		return nil, err
	}
	return apiv1.Register202Response{}, nil
}

func (a *API) SignInWithPassword(ctx context.Context, req apiv1.SignInWithPasswordRequestObject) (apiv1.SignInWithPasswordResponseObject, error) {
	var code string
	if req.Body.TotpCode != nil {
		code = *req.Body.TotpCode
	}
	in, err := a.svc.SignInWithPassword(ctx, string(req.Body.Email), req.Body.Password, code)
	if errors.Is(err, app.ErrTOTPInvalid) {
		return nil, unauthorized("totp_invalid", "the code is wrong or was already used")
	}
	if err != nil {
		return nil, err
	}
	return apiv1.SignInWithPassword200JSONResponse{SessionStartedJSONResponse: a.signedIn(in)}, nil
}

func (a *API) RequestPasswordReset(ctx context.Context, req apiv1.RequestPasswordResetRequestObject) (apiv1.RequestPasswordResetResponseObject, error) {
	if err := a.svc.RequestPasswordReset(ctx, string(req.Body.Email)); err != nil {
		return nil, err
	}
	return apiv1.RequestPasswordReset202Response{}, nil
}

func (a *API) ResetPassword(ctx context.Context, req apiv1.ResetPasswordRequestObject) (apiv1.ResetPasswordResponseObject, error) {
	if err := a.svc.ResetPassword(ctx, req.Body.Token, req.Body.Password); err != nil {
		return nil, err
	}
	return apiv1.ResetPassword204Response{}, nil
}

func (a *API) BeginPasskeySignIn(ctx context.Context, req apiv1.BeginPasskeySignInRequestObject) (apiv1.BeginPasskeySignInResponseObject, error) {
	ch, err := a.svc.BeginPasskeySignIn(ctx, string(req.Body.Email))
	if err != nil {
		return nil, err
	}
	opts, err := passkeyOptions(ch.Options)
	if err != nil {
		return nil, err
	}
	return apiv1.BeginPasskeySignIn200JSONResponse{
		Body:    opts,
		Headers: apiv1.BeginPasskeySignIn200ResponseHeaders{SetCookie: ptr(ceremonyCookie(ch.Key))},
	}, nil
}

func (a *API) FinishPasskeySignIn(ctx context.Context, req apiv1.FinishPasskeySignInRequestObject) (apiv1.FinishPasskeySignInResponseObject, error) {
	key, response, err := ceremonyInput(req.Params.UnderscoreUnderscoreHostGlossaWebauthn, map[string]any(*req.Body))
	if err != nil {
		return nil, err
	}
	in, err := a.svc.FinishPasskeySignIn(ctx, key, response)
	if err != nil {
		return nil, err
	}
	return apiv1.FinishPasskeySignIn200JSONResponse{SessionStartedJSONResponse: a.signedIn(in)}, nil
}

func (a *API) SignOut(ctx context.Context, _ apiv1.SignOutRequestObject) (apiv1.SignOutResponseObject, error) {
	c, ok := callerFrom(ctx)
	if ok && c.device != "" {
		// `glossa logout`: the device's session ends, the person's
		// others don't, and there is no cookie to clear.
		if err := a.svc.SignOutDevice(ctx, c.device); err != nil {
			return nil, err
		}
		return apiv1.SignOut204Response{}, nil
	}
	if !ok || c.session == "" {
		return nil, app.ErrUnauthenticated
	}
	if err := a.svc.SignOut(ctx, c.session); err != nil {
		return nil, err
	}
	return apiv1.SignOut204Response{Headers: apiv1.SignOut204ResponseHeaders{SetCookie: ptr(clearedSessionCookie())}}, nil
}

func (a *API) SignOutEverywhere(ctx context.Context, _ apiv1.SignOutEverywhereRequestObject) (apiv1.SignOutEverywhereResponseObject, error) {
	p, err := person(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.svc.SignOutEverywhere(ctx, p); err != nil {
		return nil, err
	}
	return apiv1.SignOutEverywhere204Response{
		Headers: apiv1.SignOutEverywhere204ResponseHeaders{SetCookie: ptr(clearedSessionCookie())},
	}, nil
}

// ── me ──────────────────────────────────────────────────────────────

func (a *API) GetMe(ctx context.Context, _ apiv1.GetMeRequestObject) (apiv1.GetMeResponseObject, error) {
	me, err := a.svc.GetMe(ctx)
	if err != nil {
		return nil, err
	}
	c, _ := callerFrom(ctx)
	// A device session needs no CSRF token, and there is no cookie to
	// derive one from: it gets none.
	csrf := ""
	if c.session != "" {
		csrf = a.csrfToken(c.session)
	}
	out := apiv1.GetMe200JSONResponse{
		Person: toPerson(me.Person), CsrfToken: csrf,
		Memberships: make([]apiv1.Membership, 0, len(me.Memberships)),
	}
	for _, m := range me.Memberships {
		out.Memberships = append(out.Memberships, apiv1.Membership{
			MemberId: m.Member.String(), Tenant: toTenant(m.Tenant), Roles: toRoles(m.Roles), Locales: m.Locales.Strings(),
		})
	}
	return out, nil
}

func (a *API) BeginPasskeyRegistration(ctx context.Context, _ apiv1.BeginPasskeyRegistrationRequestObject) (apiv1.BeginPasskeyRegistrationResponseObject, error) {
	p, err := person(ctx)
	if err != nil {
		return nil, err
	}
	ch, err := a.svc.BeginPasskeyRegistration(ctx, p)
	if err != nil {
		return nil, err
	}
	opts, err := passkeyOptions(ch.Options)
	if err != nil {
		return nil, err
	}
	return apiv1.BeginPasskeyRegistration200JSONResponse{
		Body:    opts,
		Headers: apiv1.BeginPasskeyRegistration200ResponseHeaders{SetCookie: ptr(ceremonyCookie(ch.Key))},
	}, nil
}

func (a *API) FinishPasskeyRegistration(ctx context.Context, req apiv1.FinishPasskeyRegistrationRequestObject) (apiv1.FinishPasskeyRegistrationResponseObject, error) {
	p, err := person(ctx)
	if err != nil {
		return nil, err
	}
	key, response, err := ceremonyInput(req.Params.UnderscoreUnderscoreHostGlossaWebauthn, map[string]any(req.Body.Credential))
	if err != nil {
		return nil, err
	}
	var name string
	if req.Body.Name != nil {
		name = *req.Body.Name
	}
	pk, err := a.svc.FinishPasskeyRegistration(ctx, p, key, response, name)
	if errors.Is(err, app.ErrPasskeyInvalid) {
		return nil, badRequest("passkey_invalid", "the passkey could not be verified")
	}
	if err != nil {
		return nil, err
	}
	return apiv1.FinishPasskeyRegistration201JSONResponse{Id: base64URL(pk.ID), Name: pk.Name, CreatedAt: pk.CreatedAt}, nil
}

func (a *API) ListPasskeys(ctx context.Context, req apiv1.ListPasskeysRequestObject) (apiv1.ListPasskeysResponseObject, error) {
	p, err := person(ctx)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	pks, next, err := a.svc.ListPasskeys(ctx, p, page)
	if err != nil {
		return nil, err
	}
	out := apiv1.ListPasskeys200JSONResponse{Items: make([]apiv1.Passkey, len(pks)), NextPageToken: next}
	for i, pk := range pks {
		out.Items[i] = apiv1.Passkey{Id: base64URL(pk.ID), Name: pk.Name, CreatedAt: pk.CreatedAt, LastUsedAt: pk.LastUsedAt}
	}
	return out, nil
}

func (a *API) DeletePasskey(ctx context.Context, req apiv1.DeletePasskeyRequestObject) (apiv1.DeletePasskeyResponseObject, error) {
	p, err := person(ctx)
	if err != nil {
		return nil, err
	}
	id, err := base64.RawURLEncoding.DecodeString(req.Passkey)
	if err != nil || len(id) == 0 {
		return nil, app.ErrNotFound
	}
	if err := a.svc.DeletePasskey(ctx, p, id); err != nil {
		return nil, err
	}
	return apiv1.DeletePasskey204Response{}, nil
}

func passkeyOptions(raw []byte) (apiv1.PasskeyOptions, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return apiv1.PasskeyOptions{}, fmt.Errorf("httpapi: WebAuthn options: %w", err)
	}
	return apiv1.PasskeyOptions{Options: m}, nil
}

func ceremonyInput(cookie *string, body map[string]any) (string, []byte, error) {
	if cookie == nil || *cookie == "" {
		return "", nil, app.ErrPasskeyInvalid
	}
	response, err := json.Marshal(body)
	if err != nil {
		return "", nil, badRequest("invalid_request", "the credential is not JSON")
	}
	return *cookie, response, nil
}

func (a *API) BeginTotpEnrollment(ctx context.Context, _ apiv1.BeginTotpEnrollmentRequestObject) (apiv1.BeginTotpEnrollmentResponseObject, error) {
	p, err := person(ctx)
	if err != nil {
		return nil, err
	}
	enr, err := a.svc.BeginTOTP(ctx, p)
	if err != nil {
		return nil, err
	}
	return apiv1.BeginTotpEnrollment200JSONResponse{Secret: enr.Secret, OtpauthUri: enr.OTPAuthURI}, nil
}

func (a *API) ConfirmTotpEnrollment(ctx context.Context, req apiv1.ConfirmTotpEnrollmentRequestObject) (apiv1.ConfirmTotpEnrollmentResponseObject, error) {
	p, err := person(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.svc.ConfirmTOTP(ctx, p, req.Body.Code); err != nil {
		return nil, err
	}
	return apiv1.ConfirmTotpEnrollment204Response{}, nil
}

func (a *API) DisableTotp(ctx context.Context, req apiv1.DisableTotpRequestObject) (apiv1.DisableTotpResponseObject, error) {
	p, err := person(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.svc.DisableTOTP(ctx, p, req.Body.Code); err != nil {
		return nil, err
	}
	return apiv1.DisableTotp204Response{}, nil
}

// ── tenants ─────────────────────────────────────────────────────────

func (a *API) ListTenants(ctx context.Context, req apiv1.ListTenantsRequestObject) (apiv1.ListTenantsResponseObject, error) {
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	items, next, err := a.svc.ListTenants(ctx, page)
	if err != nil {
		return nil, err
	}
	out := apiv1.ListTenants200JSONResponse{Items: make([]apiv1.Tenant, len(items)), NextPageToken: next}
	for i, t := range items {
		out.Items[i] = toTenant(t)
	}
	return out, nil
}

func (a *API) CreateTenant(ctx context.Context, req apiv1.CreateTenantRequestObject) (apiv1.CreateTenantResponseObject, error) {
	t, replayed, err := a.svc.CreateOrganization(ctx, req.Body.Slug, req.Body.Name, deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, err
	}
	return apiv1.CreateTenant201JSONResponse{
		Body: toTenant(t),
		Headers: apiv1.CreateTenant201ResponseHeaders{
			Location: ptr("/v1/tenants/" + t.ID.String()), IdempotentReplayed: replayedHeader(replayed),
		},
	}, nil
}

func (a *API) GetTenant(ctx context.Context, _ apiv1.GetTenantRequestObject) (apiv1.GetTenantResponseObject, error) {
	t, err := a.svc.GetTenant(ctx)
	if err != nil {
		return nil, err
	}
	return apiv1.GetTenant200JSONResponse(toTenant(t)), nil
}

// projectScope reads a project scope from a request body. A malformed
// id is the body's fault (400), not a project that is "not found".
func projectScope(ids *[]string) ([]string, error) {
	if ids == nil {
		return nil, nil
	}
	for _, id := range *ids {
		if _, err := domain.ParseProjectRef(id); err != nil {
			return nil, badRequest(codeInvalidProjectScope, fmt.Sprintf("%q is not a project id", id))
		}
	}
	return *ids, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func replayedHeader(replayed bool) *string {
	if !replayed {
		return nil
	}
	return ptr("true")
}

// ── members ─────────────────────────────────────────────────────────

func (a *API) ListMembers(ctx context.Context, req apiv1.ListMembersRequestObject) (apiv1.ListMembersResponseObject, error) {
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	items, next, err := a.svc.ListMembers(ctx, page)
	if err != nil {
		return nil, err
	}
	out := apiv1.ListMembers200JSONResponse{Items: make([]apiv1.Member, len(items)), NextPageToken: next}
	for i, m := range items {
		out.Items[i] = toMember(m)
	}
	return out, nil
}

func (a *API) AddMember(ctx context.Context, req apiv1.AddMemberRequestObject) (apiv1.AddMemberResponseObject, error) {
	var locales []string
	if req.Body.Locales != nil {
		locales = *req.Body.Locales
	}
	projects, err := projectScope(req.Body.Projects)
	if err != nil {
		return nil, err
	}
	inv := app.Invitation{
		Email: string(req.Body.Email), Roles: fromRoles(req.Body.Roles), Locales: locales, Projects: projects,
		Vendor: deref(req.Body.VendorId),
	}
	if req.Body.Visibility != nil {
		inv.Visibility = string(*req.Body.Visibility)
	}
	m, replayed, err := a.svc.InviteMember(ctx, inv, deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, err
	}
	return apiv1.AddMember201JSONResponse{
		Body: toMember(m),
		Headers: apiv1.AddMember201ResponseHeaders{
			ETag: ptr(etag(m.Version)), Location: ptr(tenantPath(ctx, "/members/"+m.ID.String())),
			IdempotentReplayed: replayedHeader(replayed),
		},
	}, nil
}

func (a *API) GetMember(ctx context.Context, req apiv1.GetMemberRequestObject) (apiv1.GetMemberResponseObject, error) {
	id, err := domain.ParseMemberID(req.Member)
	if err != nil {
		return nil, err
	}
	m, err := a.svc.GetMember(ctx, id)
	if err != nil {
		return nil, err
	}
	return apiv1.GetMember200JSONResponse{Body: toMember(m), Headers: apiv1.GetMember200ResponseHeaders{ETag: ptr(etag(m.Version))}}, nil
}

func (a *API) UpdateMember(ctx context.Context, req apiv1.UpdateMemberRequestObject) (apiv1.UpdateMemberResponseObject, error) {
	id, err := domain.ParseMemberID(req.Member)
	if err != nil {
		return nil, err
	}
	version, err := parseETag(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	access := b.Roles != nil || b.Locales != nil
	restriction := b.Projects != nil || b.VendorId != nil || b.Visibility != nil
	var m app.MemberView
	switch {
	case access && restriction:
		return nil, badRequest(codeAccessAndRestriction,
			"change roles and locales, or projects, vendor_id and visibility — one or the other per request")
	case restriction:
		m, err = a.restrictMember(ctx, id, version, b)
	default:
		var change app.MemberChange
		if b.Roles != nil {
			change.Roles = ptr(fromRoles(*b.Roles))
		}
		change.Locales = b.Locales
		m, err = a.svc.UpdateMember(ctx, id, version, change)
	}
	if err != nil {
		return nil, err
	}
	return apiv1.UpdateMember200JSONResponse{Body: toMember(m), Headers: apiv1.UpdateMember200ResponseHeaders{ETag: ptr(etag(m.Version))}}, nil
}

// restrictMember changes a member's project scope, vendor and
// visibility (RFC 0006 §3.3, §4.1).
func (a *API) restrictMember(ctx context.Context, id domain.MemberID, version int, b *apiv1.UpdateMemberJSONRequestBody) (app.MemberView, error) {
	var c app.RestrictionChange
	if b.Projects != nil {
		projects, err := projectScope(b.Projects)
		if err != nil {
			return app.MemberView{}, err
		}
		c.Projects = &projects
	}
	c.Vendor = b.VendorId
	if b.Visibility != nil {
		c.Visibility = ptr(string(*b.Visibility))
	}
	return a.svc.RestrictMember(ctx, id, version, c)
}

func (a *API) RemoveMember(ctx context.Context, req apiv1.RemoveMemberRequestObject) (apiv1.RemoveMemberResponseObject, error) {
	id, err := domain.ParseMemberID(req.Member)
	if err != nil {
		return nil, err
	}
	var ifMatch *int
	if req.Params.IfMatch != nil {
		v, err := parseETag(*req.Params.IfMatch)
		if err != nil {
			return nil, err
		}
		ifMatch = &v
	}
	if err := a.svc.RemoveMember(ctx, id, ifMatch); err != nil {
		return nil, err
	}
	return apiv1.RemoveMember204Response{}, nil
}

// ── tokens ──────────────────────────────────────────────────────────

func (a *API) ListTokens(ctx context.Context, req apiv1.ListTokensRequestObject) (apiv1.ListTokensResponseObject, error) {
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	items, next, err := a.svc.ListTokens(ctx, page)
	if err != nil {
		return nil, err
	}
	out := apiv1.ListTokens200JSONResponse{Items: make([]apiv1.Token, len(items)), NextPageToken: next}
	for i, t := range items {
		out.Items[i] = toToken(t)
	}
	return out, nil
}

func (a *API) CreateToken(ctx context.Context, req apiv1.CreateTokenRequestObject) (apiv1.CreateTokenResponseObject, error) {
	projects, err := projectScope(req.Body.Projects)
	if err != nil {
		return nil, err
	}
	created, err := a.svc.IssueToken(ctx, app.TokenRequest{
		Name: req.Body.Name, Scopes: fromScopes(req.Body.Scopes), Projects: projects, ExpiresAt: req.Body.ExpiresAt,
	}, deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, err
	}
	body := apiv1.CreatedToken{Token: toToken(created.Token)}
	if created.Secret != nil {
		body.Secret = ptr(created.Secret.String())
	}
	return apiv1.CreateToken201JSONResponse{
		Body: body,
		Headers: apiv1.CreateToken201ResponseHeaders{
			Location:           ptr(tenantPath(ctx, "/tokens/"+created.Token.ID.String())),
			IdempotentReplayed: replayedHeader(created.Replayed),
		},
	}, nil
}

func (a *API) GetToken(ctx context.Context, req apiv1.GetTokenRequestObject) (apiv1.GetTokenResponseObject, error) {
	id, err := domain.ParseTokenID(req.Token)
	if err != nil {
		return nil, err
	}
	t, err := a.svc.GetToken(ctx, id)
	if err != nil {
		return nil, err
	}
	return apiv1.GetToken200JSONResponse(toToken(t)), nil
}

func (a *API) RevokeToken(ctx context.Context, req apiv1.RevokeTokenRequestObject) (apiv1.RevokeTokenResponseObject, error) {
	id, err := domain.ParseTokenID(req.Token)
	if err != nil {
		return nil, err
	}
	if err := a.svc.RevokeToken(ctx, id); err != nil {
		return nil, err
	}
	return apiv1.RevokeToken204Response{}, nil
}

// The composition root asserts that every context's handlers together implement apiv1.StrictServerInterface.
