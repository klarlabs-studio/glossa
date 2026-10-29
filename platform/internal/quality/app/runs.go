package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Grader is the check policy, as a run uses it: the policy stays the
// evaluator and a run never invents a rule of its own (RFC 0005 §2.2).
type Grader interface {
	Fails(domain.Severity) bool
}

// RecordRun is one evaluation to store: what was checked, which layers
// ran, and what they found.
type RecordRun struct {
	Project uuid.UUID
	// Ref is the branch or environment that was checked.
	Ref string
	// Commit is the commit graded, where there is one.
	Commit  string
	Trigger domain.Trigger
	// PolicyVersion is the policy the run graded itself against; 0 while
	// the policy document is still the two-field kernel policy.
	PolicyVersion int
	// Layers are the layers that actually ran, in report order, so a
	// reader can tell "clean" from "not looked at".
	Layers []domain.Layer
	// Policy decides what fails. nil is checkpolicy's documented
	// default.
	Policy Grader
	// Findings carry the severity their layer emitted. A finding handed
	// in as `waived` is refused: the run applies the project's waivers
	// itself, because a caller asserting one could waive anything.
	Findings  []domain.Finding
	StartedAt time.Time
}

// RecordCheckRun stores a run and its findings, graded against the
// project's live waivers (RFC 0005 §2.2 rule 3): every surface then
// reads one row instead of recomputing the same thing four times.
//
// The findings are stored at the severity their layers emitted; which
// of them a waiver accepts is decided on read, against the waivers that
// stand then. What this run concluded is kept in its own counts, and
// nothing rewrites those: a verdict is history.
func (s *Service) RecordCheckRun(ctx context.Context, in RecordRun) (run domain.CheckRun, err error) {
	actor, err := s.write(ctx, in.Project)
	if err != nil {
		return domain.CheckRun{}, err
	}
	if len(in.Findings) > MaxRunFindings {
		return domain.CheckRun{}, fmt.Errorf("%w: %d findings, at most %d", ErrTooManyFindings, len(in.Findings), MaxRunFindings)
	}
	for _, f := range in.Findings {
		if f.Severity == domain.Waived {
			return domain.CheckRun{}, fmt.Errorf("%w: %s %s", ErrPreGradedFinding, f.Layer, f.Code)
		}
	}
	policy := in.Policy
	if policy == nil {
		policy = checkpolicy.Policy{}
	}
	now := s.now()
	started := in.StartedAt
	if started.IsZero() {
		started = now
	}
	ctx, end := s.span(ctx, "quality.record_check_run",
		attribute.String("glossa.project_id", in.Project.String()),
		attribute.String("glossa.quality.ref", in.Ref),
		attribute.String("glossa.quality.trigger", string(in.Trigger)),
		attribute.Int("glossa.quality.findings", len(in.Findings)))
	defer end(&err)

	run = domain.CheckRun{
		ID: uuid.Must(uuid.NewV7()), Project: in.Project, Ref: in.Ref, Commit: in.Commit, Trigger: in.Trigger,
		PolicyVersion: in.PolicyVersion, Layers: in.Layers, CreatedBy: actor,
		StartedAt: started.UTC(), CompletedAt: now,
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		waivers, err := st.LiveWaivers(ctx, in.Project, now)
		if err != nil {
			return err
		}
		run.Counts, run.Conclusion = domain.Conclude(policy, domain.Waivers(waivers, in.Findings, in.Ref, now))
		if err := run.Validate(); err != nil {
			return err
		}
		if err := st.InsertCheckRun(ctx, run); err != nil {
			return err
		}
		if err := st.InsertFindings(ctx, run.ID, in.Project, in.Findings); err != nil {
			return err
		}
		// The day's trend is restated from the findings, in the same
		// transaction that wrote them, so the rollup can never disagree
		// with what it summarizes (RFC 0005 §8). It is a recomputation of
		// the whole day, not an increment, so a retried write and a
		// second run of the same day both land on the same numbers.
		if err := st.RollUpFindingsByDay(ctx, in.Project, run.StartedAt); err != nil {
			return err
		}
		// And the run is announced, in the same transaction: the Glossa
		// pull-request check renders the run CI recorded (RFC 0005
		// §12.3), and nothing else would ever tell it one had arrived.
		return st.Publish(ctx, outbox.Event{
			Type: domain.EventCheckRunRecorded, AggregateType: domain.AggregateCheckRun,
			AggregateID: run.ID.String(), Actor: outbox.Actor(run.CreatedBy), Payload: domain.CheckRunRecordedOf(run),
		})
	})
	if err != nil {
		return domain.CheckRun{}, err
	}
	s.metrics.CheckRunRecorded(run.Trigger, run.Conclusion)
	for _, f := range in.Findings {
		s.metrics.FindingRecorded(f.Layer, f.Code, f.Severity)
	}
	return run, nil
}

// GetCheckRun reads one of the project's runs with its counts.
func (s *Service) GetCheckRun(ctx context.Context, project, id uuid.UUID) (domain.CheckRun, error) {
	if err := s.read(ctx, project); err != nil {
		return domain.CheckRun{}, err
	}
	var run domain.CheckRun
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		run, err = st.CheckRun(ctx, project, id)
		return err
	})
	return run, err
}

// ListCheckRuns pages a project's runs, newest first, narrowed by ref,
// commit, conclusion and trigger.
func (s *Service) ListCheckRuns(ctx context.Context, project uuid.UUID, f RunFilter, page pagination.Page) ([]domain.CheckRun, *string, error) {
	if err := s.read(ctx, project); err != nil {
		return nil, nil, err
	}
	if err := f.validate(); err != nil {
		return nil, nil, err
	}
	after, err := parseRunCursor(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.CheckRun
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.ListCheckRuns(ctx, project, f, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, runCursor)
	return items, next, nil
}

// HasReportedRun reports whether the project has ever recorded a
// reported run — one of ReportableTriggers, of any ref and any commit —
// among the runs retention still keeps.
//
// It is how the Glossa pull-request check learns that a repository's
// CI runs `glossa check`, and so whether a commit without a run yet is
// worth waiting for (RFC 0005 §14 decision 11). `capture` and `write`
// runs are the server's own and say nothing about the repository's CI.
// It is asked on every readiness pass, so it is one existence check and
// never a listing.
func (s *Service) HasReportedRun(ctx context.Context, project uuid.UUID) (bool, error) {
	if err := s.read(ctx, project); err != nil {
		return false, err
	}
	var found bool
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		found, err = st.HasCheckRunOf(ctx, project, ReportableTriggers)
		return err
	})
	return found, err
}

// validate refuses a filter value outside the vocabulary, so a typo
// answers 400 rather than silently matching nothing.
func (f RunFilter) validate() error {
	if f.Trigger != "" {
		var known bool
		for _, t := range domain.Triggers {
			known = known || string(t) == f.Trigger
		}
		if !known {
			return fmt.Errorf("%w: trigger %q", ErrInvalidQuery, f.Trigger)
		}
	}
	switch domain.Conclusion(f.Conclusion) {
	case "", domain.ConclusionSuccess, domain.ConclusionFailure, domain.ConclusionNeutral:
		return nil
	default:
		return fmt.Errorf("%w: conclusion %q", ErrInvalidQuery, f.Conclusion)
	}
}

func runCursor(r domain.CheckRun) string {
	return r.StartedAt.UTC().Format(time.RFC3339Nano) + "|" + r.ID.String()
}

func parseRunCursor(s string) (*RunCursor, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // no cursor is the first page
	}
	at, id, ok := strings.Cut(s, "|")
	t, errTime := time.Parse(time.RFC3339Nano, at)
	u, errID := uuid.Parse(id)
	if !ok || errTime != nil || errID != nil {
		return nil, invalidPageToken()
	}
	return &RunCursor{StartedAt: t, ID: u}, nil
}
