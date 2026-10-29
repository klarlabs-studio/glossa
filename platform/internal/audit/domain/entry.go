// Package domain is the Audit context's model: an entry, the canonical
// form it is hashed in, the per-tenant hash chain, and how each outbox
// event type is projected into an entry without its content.
package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// ErrInvalidEntry is returned for an entry the chain would not accept.
var ErrInvalidEntry = errors.New("audit: invalid entry")

// HashSize is the length of an entry's hash and of its link to the
// previous one (SHA-256).
const HashSize = sha256.Size

// Source says where an entry came from.
type Source string

const (
	// SourceOutbox is a domain event, projected by the outbox subscriber
	// or the backfill. Its EventID is the outbox event's id.
	SourceOutbox Source = "outbox"
	// SourceDirect is an act that never reaches the outbox — a sign-in,
	// an MCP tool call — written through the Recorder. Its EventID is the
	// act's own id (the MCP ledger row's, the sign-in attempt's).
	SourceDirect Source = "direct"
	// SourceImport is history that happened before this platform: v0.3's
	// audit_log, imported once (RFC 0006 §7.2). Its EventID is derived
	// from the v0.3 row's id (V0EventID), its actor is a v0.3 actor
	// ("v0:<uuid>", "v0:ai:<label>"…), never a platform one, and its
	// occurred_at is v0.3's. It is chained like any entry, so it sits at
	// the sequence it was appended at: a chain is ordered by append, not
	// by occurred_at.
	//
	// A third source is a new value of the canonical form's "source"
	// member, not a change to the canonical form: an entry recorded
	// before it existed encodes, and hashes, exactly as it did, so
	// CanonicalFormat stays glossa.audit.entry/1.
	SourceImport Source = "import"
)

// Valid reports whether s is one of the three sources.
func (s Source) Valid() bool { return s == SourceOutbox || s == SourceDirect || s == SourceImport }

// Draft is an entry before the chain places it: everything that is
// recorded, without its position or its hashes. It is what a projection
// or a direct write produces.
type Draft struct {
	// EventID identifies the recorded act within the tenant. Appending a
	// draft whose EventID is already recorded is a no-op, which is what
	// makes the projection idempotent under at-least-once delivery.
	EventID uuid.UUID
	Source  Source
	// Action names what happened: the event type for an outbox entry
	// (RFC 0006 §6.1), "identity.person.signed_in" and the like for a
	// direct one.
	Action string
	// Actor is who did it, spelled as outbox.Actor ("person:<uuid>",
	// "token:<uuid>", "system:<uuid>"), or "unknown" where it was never
	// recorded (an event from before migration 0042).
	Actor      string
	OccurredAt time.Time
	// AggregateType and AggregateID are the target.
	AggregateType string
	AggregateID   string
	// Project and Locale are set where the act names them.
	Project uuid.NullUUID
	Locale  string
	// Summary is the act's content-free description: a JSON object whose
	// identifiers and selectors are verbatim and everything else is its
	// shape (see Summarize). Never message text.
	Summary json.RawMessage
	// RequestID is the X-Request-ID of the request that caused a direct
	// write; TraceID is the W3C trace id of the request that published
	// an event. Either is "" when unknown.
	RequestID string
	TraceID   string
}

// Entry is a recorded draft: placed in its tenant's chain at Sequence,
// linked to the previous entry's hash, and hashed.
type Entry struct {
	Draft
	Tenant   uuid.UUID
	Sequence int64
	PrevHash []byte
	Hash     []byte
}

// Head is the end of a tenant's chain: the last sequence and its hash.
// The zero Head is the empty chain, whose hash is GenesisHash.
type Head struct {
	Sequence int64
	Hash     []byte
}

// GenesisHash is the prev_hash of a tenant's first entry: 32 zero bytes.
func GenesisHash() []byte { return make([]byte, HashSize) }

func (h Head) hash() []byte {
	if h.Sequence == 0 {
		return GenesisHash()
	}
	return h.Hash
}

// Append places d after h in tenant's chain: the next sequence, h's
// hash as its link, and its own hash. It refuses a draft the chain must
// not hold.
func Append(tenant uuid.UUID, h Head, d Draft) (Entry, error) {
	if tenant == uuid.Nil {
		return Entry{}, fmt.Errorf("%w: no tenant", ErrInvalidEntry)
	}
	if h.Sequence < 0 || (h.Sequence > 0 && len(h.Hash) != HashSize) {
		return Entry{}, fmt.Errorf("%w: a malformed chain head", ErrInvalidEntry)
	}
	d.OccurredAt = Instant(d.OccurredAt)
	if err := d.Validate(); err != nil {
		return Entry{}, err
	}
	e := Entry{Draft: d, Tenant: tenant, Sequence: h.Sequence + 1, PrevHash: bytes.Clone(h.hash())}
	sum, err := e.ComputeHash()
	if err != nil {
		return Entry{}, err
	}
	e.Hash = sum
	return e, nil
}

// Head returns the chain's head after e.
func (e Entry) Head() Head { return Head{Sequence: e.Sequence, Hash: bytes.Clone(e.Hash)} }

// Instant is how an entry's time is kept: UTC, microsecond precision —
// what Postgres stores — so the time hashed is the time read back.
func Instant(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// timeLayout renders occurred_at in the canonical form: RFC 3339, UTC,
// always six fractional digits.
const timeLayout = "2006-01-02T15:04:05.000000Z"

var (
	actionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	localePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,35}$`)
	tracePattern  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	// requestPattern is the kernel's X-Request-ID rule.
	requestPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
)

const maxNameLen = 200

// Validate reports whether d may be recorded.
func (d Draft) Validate() error {
	var errs []error
	if d.EventID == uuid.Nil {
		errs = append(errs, errors.New("no event id"))
	}
	if !d.Source.Valid() {
		errs = append(errs, fmt.Errorf("source %q", d.Source))
	}
	if len(d.Action) > maxNameLen || !actionPattern.MatchString(d.Action) {
		errs = append(errs, fmt.Errorf("action %q", d.Action))
	}
	if err := d.validateActor(); err != nil {
		errs = append(errs, err)
	}
	if d.OccurredAt.IsZero() {
		errs = append(errs, errors.New("no time"))
	}
	for name, v := range map[string]string{"aggregate type": d.AggregateType, "aggregate id": d.AggregateID} {
		if v == "" || len(v) > maxNameLen || !utf8.ValidString(v) || hasControl(v) {
			errs = append(errs, fmt.Errorf("%s %q", name, v))
		}
	}
	if d.Locale != "" && !localePattern.MatchString(d.Locale) {
		errs = append(errs, fmt.Errorf("locale %q", d.Locale))
	}
	if d.RequestID != "" && !requestPattern.MatchString(d.RequestID) {
		errs = append(errs, errors.New("request id"))
	}
	if d.TraceID != "" && !tracePattern.MatchString(d.TraceID) {
		errs = append(errs, errors.New("trace id"))
	}
	if err := ValidateSummary(d.Summary); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalidEntry, errors.Join(errs...))
	}
	return nil
}

// validateActor holds each source to its own actors: an imported entry
// names a v0.3 actor and only an imported entry may, so a v0.3 actor can
// never be passed off as having acted here, nor a platform actor as
// having acted in v0.3.
func (d Draft) validateActor() error {
	if d.Source == SourceImport {
		if !ValidV0Actor(d.Actor) {
			return fmt.Errorf("actor %q is not a v0.3 actor", d.Actor)
		}
		return nil
	}
	if d.Actor != string(outbox.ActorUnknown) && outbox.Actor(d.Actor).Validate() != nil {
		return fmt.Errorf("actor %q", d.Actor)
	}
	return nil
}

// canonicalEntry is the hashed form. Its members are the entry's whole
// recorded content except the two hashes; prev_hash enters the hash as
// its prefix instead (RFC 0006 §6.1: hash = sha256(prev_hash ‖
// canonical(entry))). An absent optional value is null, never omitted,
// so every canonical entry has the same members.
type canonicalEntry struct {
	Format        string          `json:"format"`
	TenantID      string          `json:"tenant_id"`
	Sequence      int64           `json:"sequence"`
	EventID       string          `json:"event_id"`
	Source        string          `json:"source"`
	Action        string          `json:"action"`
	Actor         string          `json:"actor"`
	OccurredAt    string          `json:"occurred_at"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	ProjectID     *string         `json:"project_id"`
	Locale        *string         `json:"locale"`
	Summary       json.RawMessage `json:"summary"`
	RequestID     *string         `json:"request_id"`
	TraceID       *string         `json:"trace_id"`
}

// CanonicalFormat versions the canonical encoding. A change to what is
// hashed is a new format, never an edit to this one: entries already
// chained must keep verifying.
const CanonicalFormat = "glossa.audit.entry/1"

// Canonical returns e's canonical encoding: the RFC 8785 (JCS) form of
// the object above.
func (e Entry) Canonical() ([]byte, error) {
	out, err := jcs.Marshal(e.canonical())
	if err != nil {
		return nil, fmt.Errorf("%w: canonical form: %w", ErrInvalidEntry, err)
	}
	return out, nil
}

// canonical is the object Canonical encodes.
func (e Entry) canonical() canonicalEntry {
	c := canonicalEntry{
		Format:        CanonicalFormat,
		TenantID:      e.Tenant.String(),
		Sequence:      e.Sequence,
		EventID:       e.EventID.String(),
		Source:        string(e.Source),
		Action:        e.Action,
		Actor:         e.Actor,
		OccurredAt:    Instant(e.OccurredAt).Format(timeLayout),
		AggregateType: e.AggregateType,
		AggregateID:   e.AggregateID,
		Locale:        optional(e.Locale),
		Summary:       e.Summary,
		RequestID:     optional(e.RequestID),
		TraceID:       optional(e.TraceID),
	}
	if e.Project.Valid {
		c.ProjectID = optional(e.Project.UUID.String())
	}
	if len(c.Summary) == 0 {
		c.Summary = json.RawMessage(`{}`)
	}
	return c
}

// ComputeHash returns sha256(prev_hash ‖ canonical(e)).
func (e Entry) ComputeHash() ([]byte, error) {
	if len(e.PrevHash) != HashSize {
		return nil, fmt.Errorf("%w: prev_hash is %d bytes", ErrInvalidEntry, len(e.PrevHash))
	}
	canon, err := e.Canonical()
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write(e.PrevHash)
	h.Write(canon)
	return h.Sum(nil), nil
}

// HexHash renders a hash the way exports and reports print it.
func HexHash(b []byte) string { return hex.EncodeToString(b) }

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
