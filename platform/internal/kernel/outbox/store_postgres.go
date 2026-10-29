package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox/outboxsql"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// PostgresStore is the Store adapter over outbox_events. It claims and
// settles in the "outbox.relay" system scope, which migration 0001
// opens to SELECT and UPDATE on outbox_events only.
type PostgresStore struct {
	uow   *db.UnitOfWork
	scope db.SystemScope
}

// NewPostgresStore returns a store on uow.
func NewPostgresStore(uow *db.UnitOfWork) *PostgresStore {
	return &PostgresStore{uow: uow, scope: db.NewSystemScope("outbox.relay")}
}

// Claim implements Store.
func (s *PostgresStore) Claim(ctx context.Context, limit int, lease time.Duration) ([]Claim, error) {
	var claims []Claim
	err := s.uow.InSystemTx(ctx, s.scope, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := outboxsql.New(tx).ClaimOutboxEvents(ctx, outboxsql.ClaimOutboxEventsParams{
			BatchSize:    int32(min(limit, 1<<20)),
			LeaseSeconds: lease.Seconds(),
		})
		if err != nil {
			return err
		}
		claims = make([]Claim, 0, len(rows))
		for _, r := range rows {
			claims = append(claims, toClaim(r))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("outbox: claim events: %w", err)
	}
	return claims, nil
}

func toClaim(r outboxsql.ClaimOutboxEventsRow) Claim {
	var traceCtx map[string]string
	_ = json.Unmarshal(r.TraceContext, &traceCtx) // best effort: a bad carrier only loses the link
	return Claim{
		Delivery: Delivery{
			EventID:       r.ID,
			TenantID:      tenancy.ID(r.TenantID),
			Type:          r.EventType,
			AggregateType: r.AggregateType,
			AggregateID:   r.AggregateID,
			Actor:         actorRead(r.Actor),
			Payload:       r.Payload,
			OccurredAt:    r.OccurredAt,
			TraceID:       traceIDOf(traceCtx),
			Attempt:       int(r.Attempts),
		},
		ClaimToken:   r.ClaimToken.UUID,
		DeliveredTo:  r.DeliveredTo,
		TraceContext: traceCtx,
	}
}

// Settle implements Store.
func (s *PostgresStore) Settle(ctx context.Context, st Settlement) error {
	var affected int64
	err := s.uow.InSystemTx(ctx, s.scope, func(ctx context.Context, tx *db.SystemTx) error {
		var err error
		affected, err = settle(ctx, outboxsql.New(tx), st)
		return err
	})
	if err != nil {
		return fmt.Errorf("outbox: settle %s as %s: %w", st.EventID, st.Outcome, err)
	}
	if affected == 0 {
		return ErrLeaseLost
	}
	return nil
}

var _ BatchSettler = (*PostgresStore)(nil)

// SettleAll implements BatchSettler: every settlement in one
// transaction, each still fenced by its own claim token. Each is one
// UPDATE as Settle's is; the batch saves the commits. On a database
// error it returns nil, and the dispatcher settles one at a time.
func (s *PostgresStore) SettleAll(ctx context.Context, ss []Settlement) []error {
	errs := make([]error, len(ss))
	err := s.uow.InSystemTx(ctx, s.scope, func(ctx context.Context, tx *db.SystemTx) error {
		q := outboxsql.New(tx)
		for i, st := range ss {
			affected, err := settle(ctx, q, st)
			if err != nil {
				return fmt.Errorf("outbox: settle %s as %s: %w", st.EventID, st.Outcome, err)
			}
			if affected == 0 {
				errs[i] = ErrLeaseLost
			}
		}
		return nil
	})
	if err != nil {
		return nil
	}
	return errs
}

func settle(ctx context.Context, q *outboxsql.Queries, st Settlement) (int64, error) {
	delivered := st.DeliveredTo
	if delivered == nil {
		delivered = []string{}
	}
	switch st.Outcome {
	case OutcomeDelivered:
		return q.MarkOutboxEventDelivered(ctx, outboxsql.MarkOutboxEventDeliveredParams{
			ID: st.EventID, ClaimToken: st.ClaimToken, DeliveredTo: delivered,
		})
	case OutcomeRetry:
		return q.RescheduleOutboxEvent(ctx, outboxsql.RescheduleOutboxEventParams{
			ID: st.EventID, ClaimToken: st.ClaimToken, DeliveredTo: delivered,
			DelaySeconds: st.RetryAfter.Seconds(), LastError: st.LastError,
		})
	case OutcomeDead:
		return q.DeadLetterOutboxEvent(ctx, outboxsql.DeadLetterOutboxEventParams{
			ID: st.EventID, ClaimToken: st.ClaimToken, DeliveredTo: delivered, LastError: st.LastError,
		})
	case OutcomeRelease:
		return q.ReleaseOutboxEvent(ctx, outboxsql.ReleaseOutboxEventParams{
			ID: st.EventID, ClaimToken: st.ClaimToken, DeliveredTo: st.DeliveredTo, // nil keeps it
		})
	}
	return 0, fmt.Errorf("unknown outcome %d", st.Outcome)
}
