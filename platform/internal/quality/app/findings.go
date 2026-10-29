package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// FindingQuery says which run's findings to read and how to narrow
// them. A finding belongs to a run, so the list always reads exactly
// one: the one Run names, or the newest of Ref and Commit, or the
// project's newest. Listing every run's copy of the same problem would
// report it once per run and say nothing more.
type FindingQuery struct {
	// Run names a run exactly; uuid.Nil leaves it to Ref and Commit.
	Run uuid.UUID
	// Ref is the branch or environment whose newest run to read.
	Ref string
	// Commit is the commit whose newest run to read.
	Commit string
	Filter FindingFilter
}

// Findings is a page of one run's findings as they stand now.
type Findings struct {
	// Run is the run they came from; nil when the project has no run
	// matching the query yet, and the page is then empty.
	Run *domain.CheckRun
	// Counts are the run's findings with today's waivers applied,
	// whatever the filters select — so waiving a finding changes them
	// while the run's own stored verdict stays what it was.
	Counts domain.Counts
	Items  []FindingRecord
	Next   *string
}

// ListFindings pages a run's findings, graded against the waivers that
// stand now (RFC 0005 §2.3): a waived finding is still computed, still
// listed and counted on its own, and it comes back as an ordinary
// finding once the source revision it was computed against has moved
// past the one its waiver was made against.
//
// The order is stable — errors, then warnings, then the waived, and
// within each by layer, locale, message key and id — and the cursor is
// that order's key, so a page never shifts. MCP's `findings_list` reads
// this shape (RFC 0005 §7.3): it is the contract, not an implementation
// detail.
func (s *Service) ListFindings(ctx context.Context, project uuid.UUID, q FindingQuery, page pagination.Page) (Findings, error) {
	if err := s.read(ctx, project); err != nil {
		return Findings{}, err
	}
	if err := q.Filter.validate(); err != nil {
		return Findings{}, err
	}
	now := s.now()
	var out Findings
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		run, err := s.resolveRun(ctx, st, project, q)
		if err != nil {
			if errors.Is(err, ErrCheckRunNotFound) && q.Run == uuid.Nil {
				return nil // nothing has been checked yet: an empty list, not a 404
			}
			return err
		}
		out.Run = &run
		if out.Counts, err = st.CountFindings(ctx, run, now); err != nil {
			return err
		}
		rows, err := st.ListFindings(ctx, run, q.Filter, page.After, page.Limit(), now)
		if err != nil {
			return err
		}
		out.Items, out.Next = pagination.Trim(rows, page, func(f FindingRecord) string { return f.SortKey })
		return nil
	})
	if err != nil {
		return Findings{}, err
	}
	if out.Items == nil {
		out.Items = []FindingRecord{}
	}
	return out, nil
}

func (s *Service) resolveRun(ctx context.Context, st Store, project uuid.UUID, q FindingQuery) (domain.CheckRun, error) {
	if q.Run != uuid.Nil {
		return st.CheckRun(ctx, project, q.Run)
	}
	return st.LatestCheckRun(ctx, project, RunFilter{Ref: q.Ref, Commit: q.Commit})
}

// validate refuses a layer or severity outside the vocabulary, so a
// typo answers 400 rather than silently matching nothing.
func (f FindingFilter) validate() error {
	if f.Layer != "" && !domain.Layer(f.Layer).Valid() {
		return fmt.Errorf("%w: layer %q", ErrInvalidQuery, f.Layer)
	}
	switch domain.Severity(f.Severity) {
	case "", domain.Error, domain.Warning, domain.Waived:
		return nil
	default:
		return fmt.Errorf("%w: severity %q", ErrInvalidQuery, f.Severity)
	}
}
