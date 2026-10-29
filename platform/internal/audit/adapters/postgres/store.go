// Package postgres implements the Audit context's store on the kernel's
// unit of work: audit_entries (migration 0045), append-only for the
// application role, under forced row-level security, with appends to a
// tenant's chain serialized by a transaction-scoped advisory lock.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/audit/adapters/postgres/auditsql"
	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
)

// Store implements app.Store.
type Store struct{ uow *db.UnitOfWork }

// NewStore returns the store on uow.
func NewStore(uow *db.UnitOfWork) *Store { return &Store{uow: uow} }

var _ app.Store = (*Store)(nil)

// InChain implements app.Store. The lock is taken first, in its own
// statement, so the head read after it sees every append that committed
// while this one waited.
func (s *Store) InChain(ctx context.Context, fn func(context.Context, app.Chain) error) error {
	return s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		q := auditsql.New(tx)
		tenant := tx.Tenant().UUID()
		if err := q.LockAuditChain(ctx, tenant); err != nil {
			return fmt.Errorf("audit: lock the chain: %w", err)
		}
		return fn(ctx, &chain{q: q, tenant: tenant})
	})
}

// Entries implements app.Store.
func (s *Store) Entries(ctx context.Context, after int64, limit int) ([]domain.Entry, error) {
	var out []domain.Entry
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		rows, err := auditsql.New(tx).AuditEntriesAfter(ctx, auditsql.AuditEntriesAfterParams{
			TenantID: tx.Tenant().UUID(), AfterSequence: after, PageSize: int32(min(max(limit, 1), 10_000)),
		})
		if err != nil {
			return err
		}
		out = make([]domain.Entry, 0, len(rows))
		for _, r := range rows {
			out = append(out, entryOf(r))
		}
		return nil
	})
	return out, err
}

// OutboxEntries implements app.Store.
func (s *Store) OutboxEntries(ctx context.Context) (int64, error) {
	var n int64
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		var err error
		n, err = auditsql.New(tx).AuditOutboxCount(ctx, tx.Tenant().UUID())
		return err
	})
	return n, err
}

func entryOf(r auditsql.AuditEntriesAfterRow) domain.Entry {
	return domain.Entry{
		Draft: domain.Draft{
			EventID: r.EventID, Source: domain.Source(r.Source), Action: r.Action, Actor: r.Actor,
			OccurredAt: r.OccurredAt.UTC(), AggregateType: r.AggregateType, AggregateID: r.AggregateID,
			Project: r.ProjectID, Locale: r.Locale.String, Summary: r.Summary,
			RequestID: r.RequestID.String, TraceID: r.TraceID.String,
		},
		Tenant: r.TenantID, Sequence: r.Sequence, PrevHash: r.PrevHash, Hash: r.Hash,
	}
}

type chain struct {
	q      *auditsql.Queries
	tenant uuid.UUID
}

func (c *chain) Tenant() uuid.UUID { return c.tenant }

func (c *chain) Head(ctx context.Context) (domain.Head, error) {
	row, err := c.q.AuditHead(ctx, c.tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Head{}, nil
	}
	if err != nil {
		return domain.Head{}, fmt.Errorf("audit: read the chain head: %w", err)
	}
	return domain.Head{Sequence: row.Sequence, Hash: row.Hash}, nil
}

func (c *chain) Recorded(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	got, err := c.q.AuditRecorded(ctx, auditsql.AuditRecordedParams{TenantID: c.tenant, EventIds: ids})
	if err != nil {
		return nil, fmt.Errorf("audit: read recorded events: %w", err)
	}
	out := make(map[uuid.UUID]bool, len(got))
	for _, id := range got {
		out[id] = true
	}
	return out, nil
}

func (c *chain) Insert(ctx context.Context, e domain.Entry) error {
	err := c.q.InsertAuditEntry(ctx, auditsql.InsertAuditEntryParams{
		TenantID: e.Tenant, Sequence: e.Sequence, EventID: e.EventID, Source: string(e.Source),
		Action: e.Action, Actor: e.Actor, OccurredAt: e.OccurredAt, AggregateType: e.AggregateType,
		AggregateID: e.AggregateID, ProjectID: e.Project, Locale: text(e.Locale), Summary: summary(e.Summary),
		RequestID: text(e.RequestID), TraceID: text(e.TraceID), PrevHash: e.PrevHash, Hash: e.Hash,
	})
	if err != nil {
		return fmt.Errorf("audit: insert entry %d: %w", e.Sequence, err)
	}
	return nil
}

func (c *chain) InsertAll(ctx context.Context, es []domain.Entry) error {
	switch len(es) {
	case 0:
		return nil
	case 1:
		return c.Insert(ctx, es[0])
	}
	n := len(es)
	p := auditsql.InsertAuditEntriesParams{
		TenantID: c.tenant, Sequences: make([]int64, n), EventIds: make([]uuid.UUID, n), Sources: make([]string, n),
		Actions: make([]string, n), Actors: make([]string, n), OccurredAts: make([]time.Time, n),
		AggregateTypes: make([]string, n), AggregateIds: make([]string, n), ProjectIds: make([]string, n),
		Locales: make([]string, n), Summaries: make([]json.RawMessage, n), RequestIds: make([]string, n),
		TraceIds: make([]string, n), PrevHashes: make([][]byte, n), Hashes: make([][]byte, n),
	}
	for i, e := range es {
		if e.Tenant != c.tenant {
			return fmt.Errorf("audit: entry %d is not in the chain's tenant", e.Sequence)
		}
		p.Sequences[i], p.EventIds[i], p.Sources[i], p.Actions[i] = e.Sequence, e.EventID, string(e.Source), e.Action
		p.Actors[i], p.OccurredAts[i], p.AggregateTypes[i], p.AggregateIds[i] = e.Actor, e.OccurredAt, e.AggregateType, e.AggregateID
		if e.Project.Valid {
			p.ProjectIds[i] = e.Project.UUID.String()
		}
		p.Locales[i], p.Summaries[i], p.RequestIds[i], p.TraceIds[i] = e.Locale, summary(e.Summary), e.RequestID, e.TraceID
		p.PrevHashes[i], p.Hashes[i] = e.PrevHash, e.Hash
	}
	if err := c.q.InsertAuditEntries(ctx, p); err != nil {
		return fmt.Errorf("audit: insert entries %d–%d: %w", es[0].Sequence, es[n-1].Sequence, err)
	}
	return nil
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func summary(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte(`{}`)
	}
	return raw
}
