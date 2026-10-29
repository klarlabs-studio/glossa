package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	authgo "github.com/klarlabs-studio/auth-go/domain"
)

// Device sign-in (RFC 0006 §7.2, the OAuth 2.0 device authorization
// grant of RFC 8628): a device that has no browser — the CLI — asks for
// a device code and a user code, shows the user code, and polls with the
// device code while the person approves the user code in Studio, signed
// in as themselves. Approved, the device receives the person's own
// session as a bearer.

// DeviceSessionPrefix starts the bearer an approved device receives:
// the person's session presented as `Authorization: Bearer glossa_dev_…`.
// Like every Glossa credential the prefix says what it opens, so the
// guard never checks it against the wrong table and a secret scanner
// recognises it.
const DeviceSessionPrefix = "glossa_dev_"

// IsDeviceSessionSecret reports whether a bearer credential claims to be
// a device session. The claim is only a routing decision: the session
// behind it is validated like any other.
func IsDeviceSessionSecret(s string) bool { return strings.HasPrefix(s, DeviceSessionPrefix) }

// DeviceSessionBearer is the bearer for a device session's raw token.
func DeviceSessionBearer(rawSession string) string { return DeviceSessionPrefix + rawSession }

// DeviceSessionToken is the raw session token behind a device bearer,
// and false for anything else.
func DeviceSessionToken(bearer string) (string, bool) {
	raw, ok := strings.CutPrefix(bearer, DeviceSessionPrefix)
	if !ok || len(raw) != tokenBodyLen || strings.IndexFunc(raw, notBase64URL) >= 0 {
		return "", false
	}
	return raw, true
}

// The flow's fixed parameters (RFC 0006 §7.2).
const (
	// DeviceAuthorizationTTL is how long both codes live.
	DeviceAuthorizationTTL = 15 * time.Minute
	// DevicePollInterval is the least time between two polls.
	DevicePollInterval = 5 * time.Second
	// DeviceSlowDownStep is what a poll that came too early adds to the
	// interval, for that poll and every later one (RFC 8628 §3.5).
	DeviceSlowDownStep = 5 * time.Second
	// DevicePollSlack forgives a poll that arrives this much before the
	// interval is up: a client that sleeps exactly the interval sees its
	// request arrive a little early or late with network jitter, and
	// should not be slowed for it.
	DevicePollSlack = 500 * time.Millisecond
	// maxDevicePollInterval bounds how far slow_down can push the
	// interval, so a client that ignores it cannot overflow anything.
	maxDevicePollInterval = 5 * time.Minute
	// MaxDeviceClientName bounds what a device may call itself.
	MaxDeviceClientName = 100
)

// UserCodeAlphabet is the user code's alphabet: twenty consonants, no
// vowels (so no word is spelled by accident) and no digits (so nothing
// looks like O/0 or I/1), as RFC 8628 §6.1 recommends. Eight of them
// carry about 34.6 bits.
const UserCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

// UserCodeLength is the user code's length without its hyphen.
const UserCodeLength = 8

// Device-flow errors.
var (
	ErrInvalidUserCode   = errors.New("identity: a user code is eight letters, as XXXX-XXXX")
	ErrInvalidClientName = errors.New("identity: a device names itself in 1-100 characters")
	// ErrDeviceNotPending means the authorization was already decided or
	// has expired, so it can no longer be approved or denied.
	ErrDeviceNotPending = errors.New("identity: the device authorization is no longer pending")
	// The poll's outcomes before a session, RFC 8628 §3.5.
	ErrAuthorizationPending = errors.New("identity: the person has not decided yet")
	ErrSlowDown             = errors.New("identity: polling faster than the interval")
	ErrAccessDenied         = errors.New("identity: the person denied the device")
	ErrExpiredToken         = errors.New("identity: the device code is unknown, expired or spent")
)

// UserCode is a normalized user code: UserCodeLength letters of the
// alphabet, without the hyphen.
type UserCode struct{ v string }

// NewUserCode draws a fresh user code. Rejection sampling keeps every
// letter equally likely.
func NewUserCode() (UserCode, error) {
	const n = len(UserCodeAlphabet)
	limit := byte(256 - 256%n) // 240: the largest multiple of 20 below 256
	out := make([]byte, 0, UserCodeLength)
	buf := make([]byte, 2*UserCodeLength)
	for len(out) < UserCodeLength {
		if _, err := rand.Read(buf); err != nil {
			return UserCode{}, err
		}
		for _, b := range buf {
			if b < limit && len(out) < UserCodeLength {
				out = append(out, UserCodeAlphabet[int(b)%n])
			}
		}
	}
	return UserCode{v: string(out)}, nil
}

// ParseUserCode normalizes what a person typed: any case, with or
// without the hyphen, surrounding and inner spaces ignored.
func ParseUserCode(s string) (UserCode, error) {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch {
		case r == '-' || r == ' ' || r == '\t':
			continue
		case r > 127 || !strings.ContainsRune(UserCodeAlphabet, r):
			return UserCode{}, ErrInvalidUserCode
		}
		b.WriteRune(r)
	}
	if b.Len() != UserCodeLength {
		return UserCode{}, ErrInvalidUserCode
	}
	return UserCode{v: b.String()}, nil
}

// String is the code as shown: XXXX-XXXX.
func (c UserCode) String() string {
	if c.v == "" {
		return ""
	}
	return c.v[:UserCodeLength/2] + "-" + c.v[UserCodeLength/2:]
}

// Hash is the at-rest lookup key: the hex SHA-256 of the normalized
// code. A pending user code is never stored as it was shown.
func (c UserCode) Hash() string { return sha256Hex(c.v) }

// DeviceCode is the device's polling secret: 256 random bits,
// base64url. Only its SHA-256 is stored.
type DeviceCode struct{ v string }

// NewDeviceCode draws a fresh device code.
func NewDeviceCode() (DeviceCode, error) {
	raw, err := authgo.NewToken()
	if err != nil {
		return DeviceCode{}, err
	}
	return DeviceCode{v: raw.String()}, nil
}

// ParseDeviceCode checks a presented device code's shape. A code of any
// other shape is unknown, which the poll answers as expired_token.
func ParseDeviceCode(s string) (DeviceCode, error) {
	if len(s) != tokenBodyLen || strings.IndexFunc(s, notBase64URL) >= 0 {
		return DeviceCode{}, ErrExpiredToken
	}
	return DeviceCode{v: s}, nil
}

func (c DeviceCode) String() string { return c.v }

// Hash is the at-rest lookup key.
func (c DeviceCode) Hash() string { return sha256Hex(c.v) }

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// DeviceAuthorizationID identifies a device authorization.
type DeviceAuthorizationID uuid.UUID

func (id DeviceAuthorizationID) String() string  { return uuid.UUID(id).String() }
func (id DeviceAuthorizationID) UUID() uuid.UUID { return uuid.UUID(id) }

// DeviceStatus is where an authorization stands.
type DeviceStatus string

const (
	DevicePending  DeviceStatus = "pending"
	DeviceApproved DeviceStatus = "approved"
	DeviceDenied   DeviceStatus = "denied"
	// DeviceRedeemed: the device has received its session; the code is
	// spent.
	DeviceRedeemed DeviceStatus = "redeemed"
)

// DeviceAuthorization is the aggregate: one device asking to be signed
// in, from its request until its session is handed over, denied or
// expired. It holds the hashes of its two codes, never the codes.
type DeviceAuthorization struct {
	ID             DeviceAuthorizationID
	DeviceCodeHash string
	UserCodeHash   string
	ClientName     string
	Status         DeviceStatus
	// Person decided it; zero while pending.
	Person       PersonID
	Interval     time.Duration
	RequestedAt  time.Time
	ExpiresAt    time.Time
	LastPolledAt *time.Time
	DecidedAt    *time.Time
	RedeemedAt   *time.Time
}

// NewDeviceAuthorization starts the flow for a device that names
// itself, returning the aggregate and the two codes to hand out.
func NewDeviceAuthorization(clientName string, now time.Time) (DeviceAuthorization, DeviceCode, UserCode, error) {
	name := strings.TrimSpace(clientName)
	if n := utf8.RuneCountInString(name); n == 0 || n > MaxDeviceClientName || !utf8.ValidString(name) ||
		strings.IndexFunc(name, isControl) >= 0 {
		return DeviceAuthorization{}, DeviceCode{}, UserCode{}, ErrInvalidClientName
	}
	dc, err := NewDeviceCode()
	if err != nil {
		return DeviceAuthorization{}, DeviceCode{}, UserCode{}, err
	}
	uc, err := NewUserCode()
	if err != nil {
		return DeviceAuthorization{}, DeviceCode{}, UserCode{}, err
	}
	return DeviceAuthorization{
		ID: DeviceAuthorizationID(newV7()), DeviceCodeHash: dc.Hash(), UserCodeHash: uc.Hash(),
		ClientName: name, Status: DevicePending, Interval: DevicePollInterval,
		RequestedAt: now, ExpiresAt: now.Add(DeviceAuthorizationTTL),
	}, dc, uc, nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) }

// Expired reports whether both codes are past their lifetime.
func (d DeviceAuthorization) Expired(now time.Time) bool { return !now.Before(d.ExpiresAt) }

// Pending reports whether the person can still decide it.
func (d DeviceAuthorization) Pending(now time.Time) bool {
	return d.Status == DevicePending && !d.Expired(now)
}

// Decide approves or denies a pending authorization on behalf of person.
func (d *DeviceAuthorization) Decide(person PersonID, approve bool, now time.Time) error {
	if !d.Pending(now) {
		return ErrDeviceNotPending
	}
	if person.IsZero() {
		return ErrInvalidID
	}
	d.Person, d.DecidedAt = person, &now
	d.Status = DeviceDenied
	if approve {
		d.Status = DeviceApproved
	}
	return nil
}

// Poll records one poll and says what it gets: nil when the device may
// now receive its session (the caller then calls Redeem), otherwise one
// of RFC 8628 §3.5's errors. The poll is recorded whatever it gets, so
// the aggregate must be saved even when Poll returns an error.
//
// A poll that arrives before the interval is up gets ErrSlowDown and
// raises the interval by DeviceSlowDownStep for good. The first poll is
// never too early: a client that polls at once is merely wasteful.
func (d *DeviceAuthorization) Poll(now time.Time) error {
	if d.Expired(now) || d.Status == DeviceRedeemed {
		return ErrExpiredToken
	}
	last := d.LastPolledAt
	d.LastPolledAt = &now
	if last != nil && now.Sub(*last) < d.Interval-DevicePollSlack {
		d.Interval = min(d.Interval+DeviceSlowDownStep, maxDevicePollInterval)
		return ErrSlowDown
	}
	switch d.Status {
	case DevicePending:
		return ErrAuthorizationPending
	case DeviceDenied:
		return ErrAccessDenied
	case DeviceApproved:
		return nil
	}
	return ErrExpiredToken
}

// Redeem spends an approved authorization: the device has its session.
func (d *DeviceAuthorization) Redeem(now time.Time) error {
	if d.Status != DeviceApproved || d.Expired(now) {
		return ErrExpiredToken
	}
	d.Status, d.RedeemedAt = DeviceRedeemed, &now
	return nil
}
