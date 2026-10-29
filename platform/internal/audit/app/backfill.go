package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// BackfillReport says what a backfill did. Unknown is reported because
// RFC 0006 §6.1 promises it: the backfill does not invent actors, so the
// entries it could not attribute are counted, not hidden.
type BackfillReport struct {
	Tenants int
	// Events is how many outbox events were read.
	Events int
	// Recorded is how many entries were appended; the rest were already
	// in the trail (live delivery, or an earlier run).
	Recorded int
	// ActorFromPayload counts appended entries whose event predates the
	// envelope's actor (migration 0042) and whose payload named one.
	ActorFromPayload int
	// Unknown counts appended entries attributed to no one.
	Unknown int
	// Retired counts appended entries of event types no code publishes
	// any more, recorded with their whole payload as its shape.
	Retired int
}

func (r *BackfillReport) add(o BackfillReport) {
	r.Events += o.Events
	r.Recorded += o.Recorded
	r.ActorFromPayload += o.ActorFromPayload
	r.Unknown += o.Unknown
	r.Retired += o.Retired
}

// backfillPage is how many events one backfill transaction appends.
const backfillPage = 500

// ErrNoHistory means the service was built without WithHistory.
var ErrNoHistory = errors.New("audit: no outbox history to backfill from")

// Backfill projects every event the outbox has recorded into its
// tenant's trail, in the order the events occurred (RFC 0006 §6.1,
// "History"). It is idempotent — an event already recorded, by live
// delivery or an earlier run, is skipped — so it can run again, and
// alongside live delivery: both append under the same per-tenant lock,
// so the chain stays whole whichever writes first. ctx carries no
// tenant.
func (s *Service) Backfill(ctx context.Context) (BackfillReport, error) {
	if s.history == nil {
		return BackfillReport{}, ErrNoHistory
	}
	tenants, err := s.history.Tenants(ctx)
	if err != nil {
		return BackfillReport{}, err
	}
	report := BackfillReport{Tenants: len(tenants)}
	for _, t := range tenants {
		r, err := s.backfillTenant(tenancy.ContextWithTenant(ctx, t))
		report.add(r)
		if err != nil {
			return report, fmt.Errorf("audit: backfill tenant %s: %w", t, err)
		}
		if r.Recorded > 0 {
			s.logger.InfoContext(ctx, "audit: backfilled a tenant",
				slog.String("tenant_id", t.String()), slog.Int("events", r.Events),
				slog.Int("recorded", r.Recorded), slog.Int("actor_unknown", r.Unknown))
		}
	}
	return report, nil
}

func (s *Service) backfillTenant(ctx context.Context) (BackfillReport, error) {
	var report BackfillReport
	// Every outbox entry names a distinct event and events are never
	// purged, so equal counts mean every event is already recorded: a
	// restart re-reads no history it has projected before.
	events, err := s.history.Count(ctx)
	if err != nil {
		return report, err
	}
	entries, err := s.store.OutboxEntries(ctx)
	if err != nil {
		return report, err
	}
	if entries >= events {
		return report, nil
	}
	var cursor outbox.HistoryCursor
	for {
		page, err := s.history.Page(ctx, cursor, backfillPage)
		if err != nil {
			return report, err
		}
		if len(page) == 0 {
			return report, nil
		}
		report.Events += len(page)
		drafts := make([]domain.Draft, 0, len(page))
		fromPayload := map[string]bool{}
		retired := map[string]bool{}
		for _, d := range page {
			draft, err := domain.FromEvent(d)
			if errors.Is(err, domain.ErrUnmapped) {
				draft, retired[d.EventID.String()] = domain.FromRetiredEvent(d), true
			}
			if domain.ActorFromPayload(d) {
				fromPayload[d.EventID.String()] = true
			}
			drafts = append(drafts, draft)
		}
		appended, err := s.Append(ctx, drafts...)
		if err != nil {
			return report, err
		}
		for _, e := range appended {
			report.Recorded++
			if e.Actor == string(outbox.ActorUnknown) {
				report.Unknown++
			}
			if fromPayload[e.EventID.String()] {
				report.ActorFromPayload++
			}
			if retired[e.EventID.String()] {
				report.Retired++
			}
		}
		cursor = outbox.CursorAfter(page[len(page)-1])
		if len(page) < backfillPage {
			return report, nil
		}
	}
}
