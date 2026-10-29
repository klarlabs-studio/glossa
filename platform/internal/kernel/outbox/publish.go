package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/propagation"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox/outboxsql"
)

// Publish records e in tx, the same tenant-scoped transaction as the
// state change that raised it: if tx rolls back, the event never
// existed. The event belongs to tx's tenant (row-level security rejects
// anything else), and it carries ctx's trace context to its handlers.
// It refuses an event that names no actor (see Actor): nothing is
// attributed by default. It returns the event's ID.
func Publish(ctx context.Context, tx *db.TenantTx, e Event) (uuid.UUID, error) {
	if err := e.Validate(); err != nil {
		return uuid.Nil, err
	}
	row, err := insertParams(ctx, tx, e)
	if err != nil {
		return uuid.Nil, err
	}
	if err := outboxsql.New(tx).InsertOutboxEvent(ctx, row); err != nil {
		return uuid.Nil, fmt.Errorf("outbox: publish %s: %w", e.Type, err)
	}
	return row.ID, nil
}

// PublishAll records events in tx in one statement, in order: the bulk
// form of Publish for writes that raise many events at once (a bulk
// upsert raises one or more per message, #77). Every event is validated
// first; none is recorded unless all are. It returns their IDs.
func PublishAll(ctx context.Context, tx *db.TenantTx, events []Event) ([]uuid.UUID, error) {
	if len(events) == 0 {
		return nil, nil
	}
	p := outboxsql.InsertOutboxEventsParams{TenantID: tx.Tenant().UUID()}
	ids := make([]uuid.UUID, len(events))
	for i, e := range events {
		if err := e.Validate(); err != nil {
			return nil, err
		}
		row, err := insertParams(ctx, tx, e)
		if err != nil {
			return nil, err
		}
		ids[i] = row.ID
		p.Ids = append(p.Ids, row.ID)
		p.EventTypes = append(p.EventTypes, row.EventType)
		p.AggregateTypes = append(p.AggregateTypes, row.AggregateType)
		p.AggregateIds = append(p.AggregateIds, row.AggregateID)
		p.Actors = append(p.Actors, row.Actor)
		p.Payloads = append(p.Payloads, row.Payload)
		p.TraceContexts = append(p.TraceContexts, row.TraceContext)
		p.OccurredAts = append(p.OccurredAts, row.OccurredAt)
	}
	if err := outboxsql.New(tx).InsertOutboxEvents(ctx, p); err != nil {
		return nil, fmt.Errorf("outbox: publish %d events (%s, …): %w", len(events), events[0].Type, err)
	}
	return ids, nil
}

func insertParams(ctx context.Context, tx *db.TenantTx, e Event) (outboxsql.InsertOutboxEventParams, error) {
	id := e.ID
	if id == uuid.Nil {
		var err error
		if id, err = uuid.NewV7(); err != nil {
			return outboxsql.InsertOutboxEventParams{}, fmt.Errorf("outbox: event id: %w", err)
		}
	}
	payload, err := marshalPayload(e.Payload)
	if err != nil {
		return outboxsql.InsertOutboxEventParams{}, fmt.Errorf("outbox: marshal %s payload: %w", e.Type, err)
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	traceCtx, err := json.Marshal(carrier)
	if err != nil {
		return outboxsql.InsertOutboxEventParams{}, fmt.Errorf("outbox: marshal trace context: %w", err)
	}
	occurred := e.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	return outboxsql.InsertOutboxEventParams{
		ID:            id,
		TenantID:      tx.Tenant().UUID(),
		EventType:     e.Type,
		AggregateType: e.AggregateType,
		AggregateID:   e.AggregateID,
		Actor:         string(e.Actor),
		Payload:       payload,
		TraceContext:  traceCtx,
		OccurredAt:    occurred,
	}, nil
}

func marshalPayload(p any) (json.RawMessage, error) {
	if p == nil {
		return json.RawMessage(`{}`), nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return b, nil
}
