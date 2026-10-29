package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/audit/adapters/postgres/auditsql"
	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
)

var _ app.EntryReader = (*Store)(nil)

// ListEntries implements app.EntryReader.
func (s *Store) ListEntries(ctx context.Context, f app.EntryFilter, cursor int64, limit int) ([]domain.Entry, error) {
	var out []domain.Entry
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		q := auditsql.New(tx)
		p := auditsql.AuditEntriesFilteredAscParams{
			TenantID: tx.Tenant().UUID(), AfterSequence: cursor,
			OccurredFrom: timestamptz(f.From), OccurredTo: timestamptz(f.To),
			FirstSequence: optInt8(f.FirstSequence), LastSequence: optInt8(f.LastSequence),
			Actor: text(f.Actor), Action: text(f.Action), Source: text(string(f.Source)),
			AggregateType: text(f.AggregateType), AggregateID: text(f.AggregateID),
			Project: f.Project, Projects: f.Projects, PageSize: int32(min(max(limit, 1), 10_000)),
		}
		if f.Ascending {
			rows, err := q.AuditEntriesFilteredAsc(ctx, p)
			for _, r := range rows {
				out = append(out, entryOf(auditsql.AuditEntriesAfterRow(r)))
			}
			return err
		}
		rows, err := q.AuditEntriesFilteredDesc(ctx, auditsql.AuditEntriesFilteredDescParams{
			TenantID: p.TenantID, BeforeSequence: cursor, OccurredFrom: p.OccurredFrom, OccurredTo: p.OccurredTo,
			FirstSequence: p.FirstSequence, LastSequence: p.LastSequence, Actor: p.Actor, Action: p.Action,
			Source: p.Source, AggregateType: p.AggregateType, AggregateID: p.AggregateID,
			Project: p.Project, Projects: p.Projects, PageSize: p.PageSize,
		})
		for _, r := range rows {
			out = append(out, entryOf(auditsql.AuditEntriesAfterRow(r)))
		}
		return err
	})
	return out, err
}

// EntryAt implements app.EntryReader.
func (s *Store) EntryAt(ctx context.Context, sequence int64) (domain.Entry, error) {
	var out domain.Entry
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		r, err := auditsql.New(tx).AuditEntryAt(ctx, auditsql.AuditEntryAtParams{TenantID: tx.Tenant().UUID(), Sequence: sequence})
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrNotFound
		}
		out = entryOf(auditsql.AuditEntriesAfterRow(r))
		return err
	})
	return out, err
}

// ChainHead implements app.EntryReader.
func (s *Store) ChainHead(ctx context.Context) (domain.Head, error) {
	var out domain.Head
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		var err error
		out, err = (&chain{q: auditsql.New(tx), tenant: tx.Tenant().UUID()}).Head(ctx)
		return err
	})
	return out, err
}

// OccurredSpan implements app.EntryReader.
func (s *Store) OccurredSpan(ctx context.Context, from, to time.Time) (app.Span, error) {
	var out app.Span
	err := s.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		r, err := auditsql.New(tx).AuditOccurredSpan(ctx, auditsql.AuditOccurredSpanParams{
			TenantID: tx.Tenant().UUID(), OccurredFrom: from, OccurredTo: to,
		})
		out = app.Span{First: r.FirstSequence, Last: r.LastSequence, Inside: r.Inside}
		return err
	})
	return out, err
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

func timestampPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return timestamptz(*t)
}

func optInt8(n int64) pgtype.Int8 { return pgtype.Int8{Int64: n, Valid: n != 0} }
