package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
)

// MaxV0HistoryEntries bounds one ImportV0History call: one transaction
// holding the tenant's chain lock. The CLI sends a larger history in
// batches; idempotency on V0ID makes a retried batch harmless.
const MaxV0HistoryEntries = 1000

// ErrTooManyV0Entries refuses a call over MaxV0HistoryEntries.
var ErrTooManyV0Entries = fmt.Errorf("audit: at most %d v0.3 history entries per import", MaxV0HistoryEntries)

var _ V0HistoryImporter = (*Service)(nil)

// ImportV0History implements V0HistoryImporter (RFC 0006 §7.2): each
// row becomes an entry with source "import", appended to the end of the
// tenant's chain in the order given — the chain orders by append, so an
// entry from 2024 can follow one from today; occurred_at says when it
// happened. Every row is checked before anything is written, and the
// call is one transaction: all of it is recorded, or none.
func (s *Service) ImportV0History(ctx context.Context, in V0HistoryImport) (V0HistoryReport, error) {
	if err := authz.RequireUnscoped(ctx, authz.AuditImport); err != nil {
		return V0HistoryReport{}, err
	}
	if len(in.Entries) > MaxV0HistoryEntries {
		return V0HistoryReport{}, ErrTooManyV0Entries
	}
	drafts, err := v0Drafts(in)
	if err != nil {
		return V0HistoryReport{}, err
	}
	distinct := map[uuid.UUID]bool{}
	for _, d := range drafts {
		distinct[d.EventID] = true
	}
	appended, err := s.Append(ctx, drafts...)
	if err != nil {
		return V0HistoryReport{}, err
	}
	report := V0HistoryReport{Recorded: len(appended), Existing: len(distinct) - len(appended)}
	s.logger.InfoContext(ctx, "audit: imported v0.3 history",
		slog.Int("rows", len(in.Entries)), slog.Int("recorded", report.Recorded), slog.Int("existing", report.Existing))
	return report, nil
}

// maxReportedRows is how many invalid rows one refusal names.
const maxReportedRows = 5

// v0Drafts checks every row and returns the drafts, or an error wrapping
// domain.ErrInvalidEntry that names the first invalid rows by index.
func v0Drafts(in V0HistoryImport) ([]domain.Draft, error) {
	drafts := make([]domain.Draft, 0, len(in.Entries))
	var bad []string
	invalid := 0
	refuse := func(i int, why string) {
		invalid++
		if len(bad) < maxReportedRows {
			bad = append(bad, fmt.Sprintf("entry %d: %s", i, why))
		}
	}
	for i, e := range in.Entries {
		if e.Action != domain.ActionV0TranslationChanged {
			refuse(i, fmt.Sprintf("action %q is not %s", e.Action, domain.ActionV0TranslationChanged))
			continue
		}
		d, err := domain.V0Change{
			V0ID: e.V0ID, Actor: e.Actor, OccurredAt: e.OccurredAt, Project: e.Project,
			Key: e.Key, Locale: e.Locale, Unresolved: e.Unresolved,
			BeforeSHA256: e.BeforeSHA256, AfterSHA256: e.AfterSHA256,
			Restore: in.Restore, RestoreSHA256: in.RestoreSHA256,
		}.Draft()
		if err != nil {
			refuse(i, strings.TrimPrefix(err.Error(), domain.ErrInvalidEntry.Error()+": "))
			continue
		}
		drafts = append(drafts, d)
	}
	if invalid > 0 {
		return nil, fmt.Errorf("%w: %d of %d v0.3 history entries: %w", domain.ErrInvalidEntry, invalid, len(in.Entries),
			errors.New(strings.Join(bad, "; ")))
	}
	return drafts, nil
}
