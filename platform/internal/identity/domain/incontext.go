package domain

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// InContextGrantPrefix starts every in-context grant secret, so a leaked
// credential's kind is obvious from the string alone and secret scanners
// can tell it from an API token: the full pattern is
// glossa_ctx_[A-Za-z0-9_-]{43}.
const InContextGrantPrefix = "glossa_ctx_"

// InContextGrantTTL is how long an in-context grant lives (RFC 0004
// §5.2). There is no refresh token: the overlay renews through the
// popup while the person's Studio session lives.
const InContextGrantTTL = 15 * time.Minute

// maxOriginLen bounds attacker-controlled input before parsing.
const maxOriginLen = 255

// maxOriginLabel bounds the human label of a preview origin.
const maxOriginLabel = 100

// MaxPreviewOrigins caps how many origins one project registers. The
// in-product editor runs against the shared preview deployment plus a
// developer's local run (RFC 0004 §5), so the list stays short on
// purpose: every entry is somewhere a person's permissions can be
// borrowed from.
const MaxPreviewOrigins = 20

// NewInContextSecret returns a fresh grant secret.
func NewInContextSecret() (TokenSecret, error) { return newSecret(InContextGrantPrefix) }

// ParseInContextSecret validates an inbound in-context grant's shape
// before anything is hashed or looked up. It refuses an API token: the
// two credentials are never interchangeable.
func ParseInContextSecret(s string) (TokenSecret, error) {
	return parseSecret(InContextGrantPrefix, s)
}

// IsInContextSecret reports whether a bearer credential claims to be an
// in-context grant, so the HTTP edge can route it to the right
// verification without trying both.
func IsInContextSecret(s string) bool { return strings.HasPrefix(s, InContextGrantPrefix) }

// InContextPermissions is the ceiling an in-context grant is cut from
// (RFC 0004 §5.2). The editor inspects a message, edits its translation
// and asks for an AI suggestion, so it needs the catalog, the
// translations and Intelligence's translate; it also shows terms and
// reads back the suggestions it asked for, which is knowledge.read and
// intelligence.read. Nothing here publishes, reviews or administers:
// the grant can never do more than this list, whatever the person may
// do in Studio.
func InContextPermissions() []Permission {
	return []Permission{
		PermCatalogRead,
		PermIntelligenceRead,
		PermIntelligenceTranslate,
		PermKnowledgeRead,
		PermTranslationsRead,
		PermTranslationsWrite,
	}
}

// Origin is a web origin the in-product editor may run on: scheme, host
// and port, with the default port dropped, and nothing else — no
// userinfo, path, query or fragment. Only https is allowed, except on
// the loopback host, where http is allowed for a developer's local run.
type Origin struct{ v string }

// ParseOrigin canonicalizes an origin, refusing anything a browser
// would not send in an Origin header.
func ParseOrigin(s string) (Origin, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxOriginLen {
		return Origin{}, fmt.Errorf("%w: %q", ErrInvalidOrigin, s)
	}
	u, err := url.Parse(s)
	if err != nil {
		return Origin{}, fmt.Errorf("%w: %q: %v", ErrInvalidOrigin, s, err)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || u.Opaque != "" {
		return Origin{}, fmt.Errorf("%w: %q is more than a scheme, host and port", ErrInvalidOrigin, s)
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname())
	if host == "" {
		return Origin{}, fmt.Errorf("%w: %q names no host", ErrInvalidOrigin, s)
	}
	switch scheme {
	case "https":
	case "http":
		if !loopback(host) {
			return Origin{}, fmt.Errorf("%w: %q: http is allowed on localhost only", ErrInvalidOrigin, s)
		}
	default:
		return Origin{}, fmt.Errorf("%w: %q: scheme must be https (or http on localhost)", ErrInvalidOrigin, s)
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return Origin{}, fmt.Errorf("%w: %q has no valid port", ErrInvalidOrigin, s)
		}
		if (scheme == "https" && n == 443) || (scheme == "http" && n == 80) {
			port = ""
		}
	}
	out := scheme + "://" + host
	if strings.Contains(host, ":") { // IPv6 literal
		out = scheme + "://[" + host + "]"
	}
	if port != "" {
		out += ":" + port
	}
	return Origin{v: out}, nil
}

// loopback reports whether host is a development machine's own name.
func loopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1" ||
		strings.HasSuffix(host, ".localhost")
}

// String returns the canonical origin.
func (o Origin) String() string { return o.v }

// IsZero reports whether the origin is unset.
func (o Origin) IsZero() bool { return o.v == "" }

// Development reports whether the origin is a developer's local run
// (plain http on loopback). Studio warns about those: they are for
// development, never for a deployment anyone else reaches.
func (o Origin) Development() bool { return strings.HasPrefix(o.v, "http://") }

// PreviewOriginID identifies a registered preview origin.
type PreviewOriginID uuid.UUID

// InContextGrantID identifies a minted in-context grant.
type InContextGrantID uuid.UUID

// PreviewOrigin is one origin the in-product editor may run on for a
// project (RFC 0004 §5.2). Registering it is the decision that a
// person's permissions may be borrowed by a page served from there, so
// it is a deliberate, listed, revocable act.
type PreviewOrigin struct {
	ID        PreviewOriginID
	TenantID  tenancy.ID
	ProjectID ProjectRef
	Origin    Origin
	// Label is what people call this deployment ("shared preview").
	Label     string
	CreatedBy Actor
	CreatedAt time.Time
}

// NewPreviewOrigin registers origin for a project.
func NewPreviewOrigin(tenant tenancy.ID, project ProjectRef, origin Origin, label string, by Actor, now time.Time) (PreviewOrigin, error) {
	label = strings.TrimSpace(label)
	if utf8.RuneCountInString(label) > maxOriginLabel {
		return PreviewOrigin{}, ErrInvalidOriginLabel
	}
	if origin.IsZero() || project.IsZero() {
		return PreviewOrigin{}, ErrInvalidOrigin
	}
	return PreviewOrigin{
		ID: NewPreviewOriginID(), TenantID: tenant, ProjectID: project, Origin: origin,
		Label: label, CreatedBy: by, CreatedAt: now,
	}, nil
}

// InContextGrant is the credential the in-product editor holds: a
// short-lived bearer token whose actor is the person, so the audit trail
// names them, cut down to InContextPermissions and bound to one project
// and one origin. Only its hash is stored; the secret is shown once, to
// the popup that minted it.
type InContextGrant struct {
	ID        InContextGrantID
	TenantID  tenancy.ID
	ProjectID ProjectRef
	Person    PersonID
	Origin    Origin
	// Permissions is the person's grant intersected with
	// InContextPermissions, locale scopes and all.
	Permissions Grant
	Hash        string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// NewInContextGrant mints a grant for person, cut from their grant in
// the tenant. It fails when the intersection allows nothing: a person
// who may not read the catalog or the translations has no editor
// session to open.
func NewInContextGrant(
	tenant tenancy.ID, project ProjectRef, person PersonID, origin Origin, of Grant, now time.Time,
) (InContextGrant, TokenSecret, error) {
	if project.IsZero() || origin.IsZero() || person.IsZero() {
		return InContextGrant{}, TokenSecret{}, ErrInvalidID
	}
	perms := of.Intersect(InContextPermissions()...)
	if len(perms.Permissions()) == 0 {
		return InContextGrant{}, TokenSecret{}, ErrNoInContextPermissions
	}
	secret, err := NewInContextSecret()
	if err != nil {
		return InContextGrant{}, TokenSecret{}, err
	}
	return InContextGrant{
		ID: NewInContextGrantID(), TenantID: tenant, ProjectID: project, Person: person, Origin: origin,
		Permissions: perms, Hash: secret.Hash(), CreatedAt: now, ExpiresAt: now.Add(InContextGrantTTL),
	}, secret, nil
}

// CheckUsable returns ErrTokenExpired past the grant's lifetime.
func (g InContextGrant) CheckUsable(now time.Time) error {
	if !now.Before(g.ExpiresAt) {
		return ErrTokenExpired
	}
	return nil
}

// BoundTo reports whether the grant may be used from origin. A grant
// carries the origin it was minted for; a request from anywhere else is
// refused even though the credential itself is valid, so a stolen token
// is useless off the page it was minted for.
func (g InContextGrant) BoundTo(origin Origin) bool {
	return !origin.IsZero() && origin.String() == g.Origin.String()
}

// ProjectRef is a project's id as Identity holds it. Catalog owns
// projects; Identity only needs to say which one a grant or a preview
// origin belongs to, so it keeps the id and nothing else.
type ProjectRef uuid.UUID

// ParseProjectRef parses a project id.
func ParseProjectRef(s string) (ProjectRef, error) {
	u, err := parseID(s)
	return ProjectRef(u), err
}

func (p ProjectRef) String() string  { return uuid.UUID(p).String() }
func (p ProjectRef) UUID() uuid.UUID { return uuid.UUID(p) }
func (p ProjectRef) IsZero() bool    { return uuid.UUID(p) == uuid.Nil }

// NewPreviewOriginID returns a fresh, time-ordered ID.
func NewPreviewOriginID() PreviewOriginID { return PreviewOriginID(newV7()) }

// NewInContextGrantID returns a fresh, time-ordered ID.
func NewInContextGrantID() InContextGrantID { return InContextGrantID(newV7()) }

// ParsePreviewOriginID parses the canonical string form.
func ParsePreviewOriginID(s string) (PreviewOriginID, error) {
	u, err := parseID(s)
	return PreviewOriginID(u), err
}

func (id PreviewOriginID) String() string   { return uuid.UUID(id).String() }
func (id PreviewOriginID) UUID() uuid.UUID  { return uuid.UUID(id) }
func (id PreviewOriginID) IsZero() bool     { return uuid.UUID(id) == uuid.Nil }
func (id InContextGrantID) String() string  { return uuid.UUID(id).String() }
func (id InContextGrantID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id InContextGrantID) IsZero() bool    { return uuid.UUID(id) == uuid.Nil }

// SortOrigins orders preview origins for display: the origin itself,
// which is stable and unique per project.
func SortOrigins(os []PreviewOrigin) {
	slices.SortFunc(os, func(a, b PreviewOrigin) int {
		return strings.Compare(a.Origin.String(), b.Origin.String())
	})
}
