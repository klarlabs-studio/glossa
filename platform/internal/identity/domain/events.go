package domain

// Domain events Identity publishes through the outbox. Names follow the
// platform convention "<context>.<aggregate>.<past-tense verb>" (see
// platform/README.md): the aggregate type is the middle segment, the
// payload is JSON with snake_case keys, and a breaking payload change
// ships as a new name with a ".v2" suffix, never in place.
const (
	EventTenantCreated      = "identity.tenant.created"
	EventMemberAdded        = "identity.member.added"
	EventMemberActivated    = "identity.member.activated"
	EventMemberAccessChange = "identity.member.access_changed"
	EventMemberRemoved      = "identity.member.removed"
	EventTokenCreated       = "identity.token.created"
	EventTokenRevoked       = "identity.token.revoked"

	// RFC 0006 §3.3, §4.
	EventMemberRestrictionChanged = "identity.member.restriction_changed"
	EventVendorCreated            = "identity.vendor.created"
	EventVendorChanged            = "identity.vendor.changed"
	EventVendorDeleted            = "identity.vendor.deleted"
	EventGroupCreated             = "identity.group.created"
	EventGroupRenamed             = "identity.group.renamed"
	EventGroupDeleted             = "identity.group.deleted"
	EventGroupMemberAdded         = "identity.group.member_added"
	EventGroupMemberRemoved       = "identity.group.member_removed"

	// Device sign-in (RFC 0006 §7.2). A device authorization belongs
	// to a person, not to a tenant, so each event is published in every
	// tenant the person belongs to: the device acts there as them, and
	// each tenant's trail says so.
	EventDeviceApproved = "identity.device_authorization.approved"
	EventDeviceDenied   = "identity.device_authorization.denied"
	EventDeviceRedeemed = "identity.device_authorization.redeemed"
)

// Aggregate types, the middle segment of the event names.
const (
	AggregateTenant = "tenant"
	AggregateMember = "member"
	AggregateToken  = "token"
	AggregateVendor = "vendor"
	AggregateGroup  = "group"
	// AggregateDeviceAuthorization is a device sign-in (RFC 0006 §7.2).
	AggregateDeviceAuthorization = "device_authorization"
)

// DeviceAuthorizationChanged is published when a person approves or
// denies a device, and when the approved device takes its session. The
// client name is what the device called itself; the audit trail
// records it only as its length.
type DeviceAuthorizationChanged struct {
	AuthorizationID string `json:"authorization_id"`
	PersonID        string `json:"person_id"`
	Status          string `json:"status"`
	ClientName      string `json:"client_name"`
	By              string `json:"by"`
}

// TenantCreated is published when an individual or organization tenant
// comes into existence.
type TenantCreated struct {
	TenantID  string `json:"tenant_id"`
	Kind      string `json:"kind"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedBy string `json:"created_by"`
}

// MemberAdded is published for every new membership, invited or active.
type MemberAdded struct {
	MemberID string   `json:"member_id"`
	PersonID string   `json:"person_id,omitempty"`
	Email    string   `json:"email"`
	Status   string   `json:"status"`
	Roles    []string `json:"roles"`
	Locales  []string `json:"locales"`
	// Projects, VendorID and Visibility are the member's restriction
	// (RFC 0006 §3.3, §4.1); omitted when unrestricted.
	Projects   []string `json:"projects,omitempty"`
	VendorID   string   `json:"vendor_id,omitempty"`
	Visibility string   `json:"visibility,omitempty"`
	AddedBy    string   `json:"added_by"`
}

// MemberActivated is published when an invitation is accepted.
type MemberActivated struct {
	MemberID string `json:"member_id"`
	PersonID string `json:"person_id"`
}

// MemberAccessChanged is published when roles or locales change.
type MemberAccessChanged struct {
	MemberID  string   `json:"member_id"`
	Roles     []string `json:"roles"`
	Locales   []string `json:"locales"`
	ChangedBy string   `json:"changed_by"`
}

// MemberRemoved is published when a membership ends.
type MemberRemoved struct {
	MemberID  string `json:"member_id"`
	PersonID  string `json:"person_id,omitempty"`
	RemovedBy string `json:"removed_by"`
}

// TokenCreated is published when an API token is issued. It never
// carries the secret or its hash.
type TokenCreated struct {
	TokenID string   `json:"token_id"`
	Name    string   `json:"name"`
	Scopes  []string `json:"scopes"`
	// Projects is omitted for a token that may act on every project.
	Projects  []string `json:"projects,omitempty"`
	CreatedBy string   `json:"created_by"`
}

// TokenRevoked is published when an API token is revoked.
type TokenRevoked struct {
	TokenID   string `json:"token_id"`
	RevokedBy string `json:"revoked_by"`
}

// MemberRestrictionChanged is published when a member's project scope,
// vendor or visibility changes. An empty Projects is every project; an
// empty VendorID is no vendor.
type MemberRestrictionChanged struct {
	MemberID   string   `json:"member_id"`
	Projects   []string `json:"projects"`
	VendorID   string   `json:"vendor_id,omitempty"`
	Visibility string   `json:"visibility"`
	ChangedBy  string   `json:"changed_by"`
}

// VendorCreated and VendorChanged carry the vendor's name and offered
// locales, never its contact.
type VendorCreated struct {
	VendorID  string   `json:"vendor_id"`
	Name      string   `json:"name"`
	Locales   []string `json:"locales"`
	CreatedBy string   `json:"created_by"`
}

// VendorChanged is published when a vendor's details change.
type VendorChanged struct {
	VendorID  string   `json:"vendor_id"`
	Name      string   `json:"name"`
	Locales   []string `json:"locales"`
	ChangedBy string   `json:"changed_by"`
}

// VendorDeleted is published when a vendor without members is deleted.
type VendorDeleted struct {
	VendorID  string `json:"vendor_id"`
	DeletedBy string `json:"deleted_by"`
}

// GroupChanged is the payload of every group event: created, renamed
// and deleted name the group; member_added and member_removed also
// name the member.
type GroupChanged struct {
	GroupID  string `json:"group_id"`
	Name     string `json:"name,omitempty"`
	MemberID string `json:"member_id,omitempty"`
	By       string `json:"by"`
}
