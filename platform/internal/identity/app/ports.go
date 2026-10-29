package app

import (
	"context"
	"errors"
	"time"

	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Store errors. Adapters translate their storage errors into these.
var (
	ErrNotFound     = errors.New("identity: not found")
	ErrEmailTaken   = errors.New("identity: email already registered")
	ErrSlugTaken    = errors.New("identity: slug taken")
	ErrDuplicate    = errors.New("identity: already a member")
	ErrStaleVersion = errors.New("identity: version changed")
	ErrNameTaken    = errors.New("identity: name already used in this tenant")
	// ErrUserCodeTaken means a fresh user code is already held by a
	// pending device authorization.
	ErrUserCodeTaken = errors.New("identity: user code already pending")
)

// Transactor runs units of work in the kernel's two scopes. Calls don't
// nest: never call the Transactor, or an auth-go service backed by it,
// from inside fn.
type Transactor interface {
	// InSystem runs fn in system scope. ctx must not carry a tenant.
	InSystem(ctx context.Context, fn func(context.Context, SystemStore) error) error
	// InTenant runs fn scoped to the tenant on ctx.
	InTenant(ctx context.Context, fn func(context.Context, TenantStore) error) error
}

// PersonRecord is a person as stored, with the credential facts the
// sign-in flows need.
type PersonRecord struct {
	domain.Person
	PasswordHash *authgo.PasswordHash
	TOTPEnabled  bool
}

// MembershipGrant is a person's active membership in one tenant.
type MembershipGrant struct {
	Member  domain.MemberID
	Roles   domain.Roles
	Locales domain.LocaleScope
	// Projects and Visibility are the membership's restriction (RFC
	// 0006 §3.3, §4.1), which the principal carries beside its grant.
	Projects   domain.ProjectScope
	Visibility domain.Visibility
}

// MembershipView is one of a person's tenants, for GET /v1/me.
type MembershipView struct {
	Member  domain.MemberID
	Tenant  tenancy.Tenant
	Roles   domain.Roles
	Locales domain.LocaleScope
}

// TokenRecord is what bearer authentication needs about a token.
type TokenRecord struct {
	ID     domain.TokenID
	Tenant tenancy.ID
	Scopes domain.Scopes
	// Projects is the token's project scope (RFC 0006 §4.1).
	Projects   domain.ProjectScope
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
}

// GrantRecord is what bearer authentication needs about an in-context
// grant (RFC 0004 §5.2): the person it acts as, where it may act, and
// what it may do there. Unlike an API token it is never revoked — it
// expires in fifteen minutes — and it is refused off its origin.
type GrantRecord struct {
	ID          domain.InContextGrantID
	Tenant      tenancy.ID
	Project     domain.ProjectRef
	Person      domain.PersonID
	Origin      domain.Origin
	Permissions domain.Grant
	ExpiresAt   time.Time
}

// CITokenRecord is what bearer authentication needs about a CI token
// (RFC 0004 §6.3): the project it may act on and what it may do there.
// Like an in-context grant it is never revoked — it expires in thirty
// minutes — and it acts as no person, so there is nothing to look up
// about a member.
type CITokenRecord struct {
	ID          domain.CITokenID
	Tenant      tenancy.ID
	Project     domain.ProjectRef
	Permissions domain.Grant
	ExpiresAt   time.Time
}

// TOTPRecord is a person's stored authenticator secret.
type TOTPRecord struct {
	Confirmed bool
}

// SystemStore is persistence in system scope: global data, and the
// narrow cross-tenant lookups that happen before a tenant is chosen.
type SystemStore interface {
	CreatePerson(ctx context.Context, p domain.Person, password *authgo.PasswordHash) error
	PersonByEmail(ctx context.Context, email authgo.Email) (PersonRecord, error)
	PersonByID(ctx context.Context, id domain.PersonID) (PersonRecord, error)
	MarkEmailVerified(ctx context.Context, id domain.PersonID, at time.Time) error
	SetPassword(ctx context.Context, id domain.PersonID, hash authgo.PasswordHash, at time.Time) error

	ActiveMembership(ctx context.Context, person domain.PersonID, tenant tenancy.ID) (MembershipGrant, error)
	MembershipsOf(ctx context.Context, person domain.PersonID, after tenancy.ID, limit int) ([]MembershipView, error)
	OpenInvitations(ctx context.Context, email authgo.Email) ([]InvitationRef, error)
	Tenant(ctx context.Context, id tenancy.ID) (tenancy.Tenant, error)

	TokenByHash(ctx context.Context, hash string) (TokenRecord, error)
	TouchToken(ctx context.Context, id domain.TokenID, at time.Time) error

	// GrantByHash resolves an in-context grant before its tenant is
	// known, like TokenByHash (RFC 0004 §5.2).
	GrantByHash(ctx context.Context, hash string) (GrantRecord, error)
	TouchGrant(ctx context.Context, id domain.InContextGrantID, at time.Time) error
	// OriginRegistered reports whether origin is a preview origin of any
	// project. A CORS preflight carries no credentials, so the answer
	// has to come before any tenant is known.
	OriginRegistered(ctx context.Context, origin string) (bool, error)
	// PurgeExpiredGrants drops grants that expired before at.
	PurgeExpiredGrants(ctx context.Context, at time.Time) (int64, error)

	// CITokenByHash resolves a CI token before its tenant is known,
	// like TokenByHash (RFC 0004 §6.3).
	CITokenByHash(ctx context.Context, hash string) (CITokenRecord, error)
	TouchCIToken(ctx context.Context, id domain.CITokenID, at time.Time) error
	// PurgeExpiredCITokens drops CI tokens that expired before at.
	PurgeExpiredCITokens(ctx context.Context, at time.Time) (int64, error)

	PendingTOTP(ctx context.Context, person domain.PersonID, secret authgo.TOTPSecret, at time.Time) error
	TOTP(ctx context.Context, person domain.PersonID) (TOTPRecord, error)
	ConfirmTOTP(ctx context.Context, person domain.PersonID, at time.Time) error
	DeleteTOTP(ctx context.Context, person domain.PersonID) error

	AddPasskey(ctx context.Context, person domain.PersonID, c authgo.PasskeyCredential, at time.Time) error
	PersonOfPasskey(ctx context.Context, credentialID []byte) (domain.PersonID, error)
	// PasskeysOf lists a person's passkeys after the cursor, oldest first.
	PasskeysOf(ctx context.Context, person domain.PersonID, after PasskeyCursor, limit int) ([]Passkey, error)
	// DeletePasskeyOf removes the person's passkey (ErrNotFound if they
	// have no such passkey).
	DeletePasskeyOf(ctx context.Context, person domain.PersonID, credentialID []byte) error

	// Device sign-in (RFC 0006 §7.2). Authorizations are found by the
	// hash of a code, never the code; Lock… holds the row until the
	// transaction ends, so a decision and a poll never interleave.
	InsertDeviceAuthorization(ctx context.Context, d domain.DeviceAuthorization) error
	// PurgeDeviceAuthorizations drops authorizations that expired
	// before the time given.
	PurgeDeviceAuthorizations(ctx context.Context, before time.Time) (int64, error)
	// LockPendingDeviceAuthorization finds the pending, unexpired
	// authorization a user code names (ErrNotFound otherwise).
	LockPendingDeviceAuthorization(ctx context.Context, userCodeHash string, now time.Time) (domain.DeviceAuthorization, error)
	// LockDeviceAuthorization finds the authorization a device code
	// names, in whatever state (ErrNotFound when there is none).
	LockDeviceAuthorization(ctx context.Context, deviceCodeHash string) (domain.DeviceAuthorization, error)
	UpdateDeviceAuthorization(ctx context.Context, d domain.DeviceAuthorization) error
	// WithdrawDeviceApprovals denies every device the person approved
	// that has not yet taken its session (sign out everywhere).
	WithdrawDeviceApprovals(ctx context.Context, person domain.PersonID) error

	SaveCeremony(ctx context.Context, c Ceremony) error
	// TakeCeremony deletes and returns a ceremony (ErrNotFound if absent).
	TakeCeremony(ctx context.Context, keyHash, purpose string) (Ceremony, error)
}

// Ceremony is WebAuthn ceremony state held server-side between the
// challenge and the response, keyed by the hash of a random key the
// browser keeps in a cookie.
type Ceremony struct {
	KeyHash   string
	Purpose   string
	Person    domain.PersonID // zero for sign-in
	State     []byte
	ExpiresAt time.Time
}

// InvitationRef locates an open invitation.
type InvitationRef struct {
	Member domain.MemberID
	Tenant tenancy.ID
}

// MemberView is a member plus the display name of the person, if any.
type MemberView struct {
	domain.Member
	DisplayName string
}

// TenantStore is persistence scoped to the context's tenant. Row-level
// security, not these methods, keeps it inside that tenant.
type TenantStore interface {
	CreateTenant(ctx context.Context, t tenancy.Tenant) (tenancy.Tenant, error)
	CurrentTenant(ctx context.Context) (tenancy.Tenant, error)

	// InsertMember stores m; inserted is false when a member with m.ID
	// already exists (an idempotent retry).
	InsertMember(ctx context.Context, m domain.Member, by domain.Actor) (inserted bool, err error)
	Member(ctx context.Context, id domain.MemberID) (MemberView, error)
	// LockMember loads m for update.
	LockMember(ctx context.Context, id domain.MemberID) (domain.Member, error)
	// LockActiveOwners locks and counts the tenant's active owners.
	LockActiveOwners(ctx context.Context) (int, error)
	Members(ctx context.Context, after domain.MemberID, limit int) ([]MemberView, error)
	// UpdateMemberAccess saves roles, locales and version; it fails with
	// ErrStaleVersion unless the stored version is m.Version-1.
	UpdateMemberAccess(ctx context.Context, m domain.Member) error
	// UpdateMemberRestriction saves project scope, vendor, visibility and
	// version, like UpdateMemberAccess.
	UpdateMemberRestriction(ctx context.Context, m domain.Member) error
	ActivateMember(ctx context.Context, m domain.Member) error
	DeleteMember(ctx context.Context, id domain.MemberID) error

	// Vendors (RFC 0006 §3.3). InsertVendor's inserted is false on an
	// idempotent retry; DeleteVendor fails with domain.ErrVendorHasMembers
	// while any member names the vendor.
	InsertVendor(ctx context.Context, v domain.Vendor, by domain.Actor) (inserted bool, err error)
	Vendor(ctx context.Context, id domain.VendorID) (domain.Vendor, error)
	LockVendor(ctx context.Context, id domain.VendorID) (domain.Vendor, error)
	Vendors(ctx context.Context, after domain.VendorID, limit int) ([]domain.Vendor, error)
	UpdateVendor(ctx context.Context, v domain.Vendor) error
	DeleteVendor(ctx context.Context, id domain.VendorID) error
	CountVendorMembers(ctx context.Context, id domain.VendorID) (int, error)

	// Groups (RFC 0006 §4.3), loaded with their members. AddGroupMember
	// and RemoveGroupMember save the group's new version with the change.
	InsertGroup(ctx context.Context, g domain.Group, by domain.Actor) (inserted bool, err error)
	Group(ctx context.Context, id domain.GroupID) (domain.Group, error)
	LockGroup(ctx context.Context, id domain.GroupID) (domain.Group, error)
	Groups(ctx context.Context, after domain.GroupID, limit int) ([]domain.Group, error)
	UpdateGroup(ctx context.Context, g domain.Group) error
	AddGroupMember(ctx context.Context, g domain.Group, member domain.MemberID, by domain.Actor) error
	RemoveGroupMember(ctx context.Context, g domain.Group, member domain.MemberID) error
	DeleteGroup(ctx context.Context, id domain.GroupID) error

	InsertToken(ctx context.Context, t domain.APIToken) (inserted bool, err error)
	Token(ctx context.Context, id domain.TokenID) (domain.APIToken, error)
	Tokens(ctx context.Context, after domain.TokenID, limit int) ([]domain.APIToken, error)
	RevokeToken(ctx context.Context, t domain.APIToken, by domain.Actor) error

	// InsertPreviewOrigin registers o; inserted is false when the same
	// origin is already registered for the project.
	InsertPreviewOrigin(ctx context.Context, o domain.PreviewOrigin) (inserted bool, err error)
	PreviewOrigins(ctx context.Context, project domain.ProjectRef) ([]domain.PreviewOrigin, error)
	PreviewOrigin(ctx context.Context, id domain.PreviewOriginID) (domain.PreviewOrigin, error)
	// DeletePreviewOrigin removes the registration and, with it, every
	// in-context grant minted for that origin, so access ends at once
	// rather than when the last grant expires.
	DeletePreviewOrigin(ctx context.Context, o domain.PreviewOrigin) error
	// PreviewOriginFor finds a project's registration of origin
	// (ErrNotFound when it has none).
	PreviewOriginFor(ctx context.Context, project domain.ProjectRef, origin domain.Origin) (domain.PreviewOrigin, error)

	InsertGrant(ctx context.Context, g domain.InContextGrant) error

	// InsertCIToken stores a token minted for a GitHub Actions run
	// (RFC 0004 §6.3), with the run recorded beside it.
	InsertCIToken(ctx context.Context, t domain.CIToken) error

	Publish(ctx context.Context, e outbox.Event) error
}

// Message is an email to send.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Mailer delivers email. Adapters: a log mailer for development, SMTP.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// PasskeyStore is the passkey repository auth-go's WebAuthn adapter reads.
type PasskeyStore = authgo.PasskeyRepository
