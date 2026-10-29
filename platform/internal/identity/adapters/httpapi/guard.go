package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Cookie and header names (api/openapi.yaml, securitySchemes).
const (
	// SessionCookie holds the session. The __Host- prefix makes browsers
	// insist on Secure, Path=/ and no Domain, so no subdomain can set it.
	SessionCookie = "__Host-glossa_session"
	// CeremonyCookie holds the key of an in-flight passkey ceremony.
	CeremonyCookie = "__Host-glossa_webauthn"
	// CSRFHeader carries the session's CSRF token on unsafe requests.
	CSRFHeader = "X-CSRF-Token"
)

// caller is what the guard established about the request.
type caller struct {
	authn app.Authn
	// session is the raw session cookie, when the caller used one; it's
	// needed to sign out and to derive the CSRF token.
	session string
	// device is the glossa_dev_ bearer, when the caller is a device the
	// person signed in (RFC 0006 §7.2): the person's session, but never
	// a browser's — it decides no device approvals, and signing out
	// with it ends only it.
	device string
}

type callerKey struct{}

func callerFrom(ctx context.Context) (caller, bool) {
	c, ok := ctx.Value(callerKey{}).(caller)
	return c, ok
}

// Guard enforces each operation's security requirement from the
// contract. It runs inside the router (so r.Pattern and r.PathValue are
// set): it authenticates the session cookie (+ CSRF header on unsafe
// methods) or bearer token the operation accepts, and on
// /v1/tenants/{tenant}/… hands over to tenancy.Middleware with this
// package's Resolver, which checks the tenant against the caller.
func (a *API) Guard(next http.Handler) http.Handler {
	tenantScoped := tenancy.Middleware(a, a.logger)(a.projectScoped(next))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, ok := a.reqs[r.Pattern]
		if !ok {
			a.errs.write(w, r, fmt.Errorf("httpapi: route %q is not in the contract", r.Pattern))
			return
		}
		r = r.WithContext(app.WithClientAddress(r.Context(), a.clientAddress(r)))
		if req.Public {
			if unsafeMethod(r.Method) && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				problem.WriteDetails(w, problem.New(http.StatusForbidden, "cross_site_request",
					"sign-in requests must come from Glossa's own pages"))
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		c, err := a.authenticate(r, req)
		if err != nil {
			a.errs.write(w, r, err)
			return
		}
		if err := boundToRoute(c, r); err != nil {
			a.errs.write(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), callerKey{}, c)
		if r.PathValue("tenant") != "" {
			tenantScoped.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		next.ServeHTTP(w, r.WithContext(authz.WithPrincipal(ctx, c.authn.Principal())))
	})
}

var errCSRF = problem.New(http.StatusForbidden, "csrf_invalid",
	"send the session's csrf_token in the X-CSRF-Token header")

func (a *API) authenticate(r *http.Request, req apiv1.Requirement) (caller, error) {
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, cred, _ := strings.Cut(h, " ")
		if !strings.EqualFold(scheme, "Bearer") {
			return caller{}, app.ErrUnauthenticated
		}
		cred = strings.TrimSpace(cred)
		// The prefix says which credential this is, so the two are never
		// checked against the wrong table and an operation that takes
		// only one never accidentally takes the other.
		if domain.IsInContextSecret(cred) {
			if !req.InContext {
				return caller{}, app.ErrUnauthenticated
			}
			authn, err := a.svc.AuthenticateInContextGrant(r.Context(), cred, r.Header.Get("Origin"))
			return caller{authn: authn}, err
		}
		// A device session is the person's session presented as a
		// bearer (RFC 0006 §7.2): accepted wherever a session is and
		// nowhere else — never on an operation that takes only API
		// tokens. It needs no CSRF token, because nothing sends it
		// ambiently: a page on another origin can't make a browser
		// attach it.
		if domain.IsDeviceSessionSecret(cred) {
			if !req.Session {
				return caller{}, app.ErrUnauthenticated
			}
			authn, err := a.svc.AuthenticateDeviceSession(r.Context(), cred)
			return caller{authn: authn, device: cred}, err
		}
		if !req.Bearer {
			return caller{}, app.ErrUnauthenticated
		}
		// A CI token is an API token's narrower cousin (RFC 0004 §6.3):
		// it is accepted wherever an API token is, and what keeps it
		// small is its permissions and its project binding, not a
		// shorter list of operations. So the operations need no second
		// security scheme, and a CI run can use the CLI unchanged.
		if domain.IsCISecret(cred) {
			authn, err := a.svc.AuthenticateCIToken(r.Context(), cred)
			return caller{authn: authn}, err
		}
		authn, err := a.svc.AuthenticateToken(r.Context(), cred)
		return caller{authn: authn}, err
	}
	cookie, err := r.Cookie(SessionCookie)
	if err != nil || !req.Session {
		return caller{}, app.ErrUnauthenticated
	}
	authn, err := a.svc.AuthenticateSession(r.Context(), cookie.Value)
	if err != nil {
		return caller{}, err
	}
	if req.CSRF && unsafeMethod(r.Method) && !a.validCSRF(cookie.Value, r.Header.Get(CSRFHeader)) {
		return caller{}, errCSRF
	}
	return caller{authn: authn, session: cookie.Value}, nil
}

// boundToRoute enforces a project-bound credential's binding: a grant
// minted for one project never reaches another's data, even though both
// live in the same tenant, and neither does a CI token minted from one
// repository's Git connection. For a grant the origin binding is
// checked when the credential is resolved; this is the other half.
//
// Routes with no {project} in the path — the message preview, the
// tenant's AI suggestions — are bounded instead by what the credential
// may do: an in-context grant by the small set of operations that
// accept one at all (the same set CORS answers a preview origin on),
// and a CI token by its two permissions, which no tenant-level
// operation asks for.
func boundToRoute(c caller, r *http.Request) error {
	var bound string
	switch {
	case c.authn.Grant != nil:
		bound = c.authn.Grant.Project.String()
	case c.authn.CI != nil:
		bound = c.authn.CI.Project.String()
	default:
		return nil
	}
	if project := r.PathValue("project"); project != "" && project != bound {
		return domain.ErrGrantProjectMismatch
	}
	return nil
}

// projectScoped is the edge's backstop for project scope (RFC 0006
// §4.1): an operation whose path names a {project} outside the
// principal's scope answers 404 before any handler runs, exactly as a
// project that does not exist. Every application service checks the
// same scope itself (authz.RequireIn and its siblings) — that is the
// enforcement, and what MCP and every other caller rely on; this makes
// an operation that forgot to unable to leak through HTTP.
func (a *API) projectScoped(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw := r.PathValue("project"); raw != "" {
			p, _ := authz.From(r.Context())
			if project, err := uuid.Parse(raw); err == nil && !p.InProject(project) {
				a.errs.write(w, r, authz.ErrNotVisible)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ResolveTenant implements tenancy.Resolver. The tenant named in the
// path is only a request: it becomes the request's tenant once the
// session's person is an active member of it, or it is the API token's
// own tenant. The principal and its grant ride on the returned context.
func (a *API) ResolveTenant(r *http.Request) (context.Context, tenancy.ID, error) {
	c, ok := callerFrom(r.Context())
	if !ok {
		return nil, tenancy.ID{}, tenancy.ErrUnauthenticated
	}
	id, err := tenancy.ParseID(r.PathValue("tenant"))
	if err != nil {
		return nil, tenancy.ID{}, tenancy.ErrForbidden
	}
	p, err := a.svc.Authorize(r.Context(), c.authn, id)
	if errors.Is(err, app.ErrForbidden) {
		return nil, tenancy.ID{}, tenancy.ErrForbidden
	}
	if err != nil {
		return nil, tenancy.ID{}, err
	}
	return authz.WithPrincipal(r.Context(), p), id, nil
}

var _ tenancy.Resolver = (*API)(nil)

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// csrfToken derives the session's CSRF token: an HMAC of the session
// cookie under a server key. Nothing is stored, it dies with the
// session, and a page on another origin can't compute or read it.
func (a *API) csrfToken(session string) string {
	mac := hmac.New(sha256.New, a.csrfKey)
	mac.Write([]byte("glossa-csrf\x00" + session))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *API) validCSRF(session, got string) bool {
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(a.csrfToken(session))) == 1
}

func sessionCookie(value string, ttl time.Duration) string {
	return (&http.Cookie{
		Name: SessionCookie, Value: value, Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	}).String()
}

func clearedSessionCookie() string {
	return (&http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	}).String()
}

func ceremonyCookie(key string) string {
	return (&http.Cookie{
		Name: CeremonyCookie, Value: key, Path: "/", MaxAge: int(app.CeremonyTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	}).String()
}
