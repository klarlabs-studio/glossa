// Package app is the Audit context's application layer: the outbox
// subscriber that projects every event into an entry, the Recorder
// other contexts write their non-event acts through, the backfill of
// the outbox's history, and verification of a stored chain.
package app

import (
	"context"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Store is audit_entries.
type Store interface {
	// InChain runs fn in a transaction on the chain of ctx's tenant,
	// holding that chain's lock until it commits: appends to one tenant
	// serialize, appends to two tenants do not wait on each other.
	InChain(ctx context.Context, fn func(context.Context, Chain) error) error
	// Entries reads ctx's tenant's chain in order, after a sequence.
	Entries(ctx context.Context, after int64, limit int) ([]domain.Entry, error)
	// OutboxEntries counts ctx's tenant's entries projected from the
	// outbox.
	OutboxEntries(ctx context.Context) (int64, error)
}

// Chain is one tenant's chain inside InChain.
type Chain interface {
	// Tenant is the chain's tenant.
	Tenant() uuid.UUID
	// Head is the chain's last entry (the zero Head when it is empty).
	Head(ctx context.Context) (domain.Head, error)
	// Recorded returns which of ids already have an entry.
	Recorded(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error)
	// Insert appends e, which must be the head's successor.
	Insert(ctx context.Context, e domain.Entry) error
	// InsertAll appends es, consecutive successors of the head, in one
	// statement.
	InsertAll(ctx context.Context, es []domain.Entry) error
}

// History is the outbox's record of past events (outbox.History).
type History interface {
	// Tenants lists the tenants with recorded events. ctx carries no
	// tenant.
	Tenants(ctx context.Context) ([]tenancy.ID, error)
	// Count is how many events ctx's tenant has recorded.
	Count(ctx context.Context) (int64, error)
	// Page reads ctx's tenant's events after a cursor, in the order they
	// occurred.
	Page(ctx context.Context, after outbox.HistoryCursor, limit int) ([]outbox.Delivery, error)
}

// Recorder is the port other contexts write the security-relevant acts
// that never reach the outbox through (RFC 0006 §6.1, "not only
// events"). Each call records one entry in the tenant on ctx. A
// Recorder never takes text: what it is handed is already content-free,
// and the entry's summary is checked to be shaped like one.
type Recorder interface {
	// RecordSignIn records a sign-in attempt by a known person.
	RecordSignIn(ctx context.Context, s domain.SignIn) error
	// RecordToolCall records one MCP tool call, pointing at its ledger
	// row.
	RecordToolCall(ctx context.Context, c domain.ToolCall) error
}
