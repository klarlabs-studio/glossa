package app_test

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// The in-memory halves of the check (RFC 0004 §6.4): the queue the
// worker claims from, and the read model the report is rendered out of.
// Both mirror what the Postgres adapter and the sources adapter do, so
// the flow tests pin the decisions rather than the SQL — the store has
// its own integration test on real Postgres.

// memChecks is the check queue, keyed by (repository, pull request) as
// the table is. That key is the whole concurrency story: one row per
// pull request means one claim per pull request, so two jobs can never
// race the one sticky comment.
type memChecks struct {
	mu   sync.Mutex
	rows map[checkKey]*domain.Check
	now  func() time.Time
}

type checkKey struct {
	repository  int64
	pullRequest int
}

func newMemChecks(now func() time.Time) *memChecks {
	return &memChecks{rows: map[checkKey]*domain.Check{}, now: now}
}

func (m *memChecks) Open(_ context.Context, in domain.Check) (domain.Check, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := checkKey{in.RepositoryID, in.PullRequest}
	row, ok := m.rows[k]
	if !ok {
		c := in
		m.rows[k] = &c
		return c, nil
	}
	// The comment is the pull request's, so it survives a new commit;
	// the check runs are the commit's, so they do not.
	if row.HeadSHA != in.HeadSHA {
		row.HeadSHA, row.Targets, row.Conclusion = in.HeadSHA, nil, ""
		row.RequestedAt = in.RequestedAt
	}
	row.Branch, row.TenantID, row.InstallationID = in.Branch, in.TenantID, in.InstallationID
	// The event says where the head lives now, as the upsert does.
	row.FromFork = in.FromFork
	// When the pull request was opened does not move with a new commit,
	// or a policy's grace would run a little further out with every
	// push. A row that never learned it takes the event's, which is what
	// COALESCE does in the upsert.
	if row.OpenedAt.IsZero() {
		row.OpenedAt = in.OpenedAt
	}
	row.State, row.CompletedAt, row.Attempts, row.Failure = domain.CheckQueued, nil, 0, ""
	row.AvailableAt, row.UpdatedAt = in.AvailableAt, in.UpdatedAt
	return *row, nil
}

func (m *memChecks) Rerun(_ context.Context, repository int64, headSHA string, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, row := range m.rows {
		if row.RepositoryID != repository || row.HeadSHA != headSHA {
			continue
		}
		// GitHub makes new check runs for a rerequest, so the ledger of
		// annotations already appended starts empty again.
		row.Targets, row.Conclusion, row.CompletedAt = nil, "", nil
		row.State, row.Attempts, row.Failure = domain.CheckQueued, 0, ""
		row.AvailableAt, row.UpdatedAt = now, now
		n++
	}
	return n, nil
}

func (m *memChecks) Wake(_ context.Context, repositories []int64, branch string, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, row := range m.rows {
		if !slices.Contains(repositories, row.RepositoryID) || row.ClaimToken != uuid.Nil {
			continue
		}
		if branch != "" && row.Branch != branch {
			continue
		}
		row.State, row.AvailableAt, row.UpdatedAt = domain.CheckQueued, now, now
		n++
	}
	return n, nil
}

func (m *memChecks) Claim(_ context.Context, now time.Time, lease time.Duration) (domain.Check, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range slices.SortedFunc(maps.Keys(m.rows), func(a, b checkKey) int {
		if a.repository != b.repository {
			return cmp.Compare(a.repository, b.repository)
		}
		return cmp.Compare(a.pullRequest, b.pullRequest)
	}) {
		row := m.rows[k]
		if row.State != domain.CheckQueued || row.ClaimToken != uuid.Nil || now.Before(row.AvailableAt) {
			continue
		}
		row.Attempts++
		row.ClaimToken = uuid.New()
		row.AvailableAt = now.Add(lease)
		return *row, true, nil
	}
	return domain.Check{}, false, nil
}

func (m *memChecks) Save(_ context.Context, in domain.Check, available time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[checkKey{in.RepositoryID, in.PullRequest}]
	if !ok || row.ClaimToken != in.ClaimToken {
		return nil // the lease was lost; its holder settles
	}
	saved := in
	saved.ClaimToken, saved.AvailableAt = uuid.Nil, available
	*row = saved
	return nil
}

func (m *memChecks) Retry(_ context.Context, in domain.Check, now time.Time, delay time.Duration, failure string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[checkKey{in.RepositoryID, in.PullRequest}]
	if !ok || row.ClaimToken != in.ClaimToken {
		return nil
	}
	row.ClaimToken, row.Failure = uuid.Nil, failure
	row.AvailableAt = now.Add(delay)
	return nil
}

func (m *memChecks) Expire(_ context.Context, deadline, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, row := range m.rows {
		if row.State != domain.CheckQueued || row.ClaimToken != uuid.Nil || row.RequestedAt.After(deadline) {
			continue
		}
		if row.AvailableAt.After(now) {
			row.AvailableAt = now
			n++
		}
	}
	return n, nil
}

func (m *memChecks) Depth(context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, row := range m.rows {
		if row.State == domain.CheckQueued && !m.now().Before(row.AvailableAt) {
			n++
		}
	}
	return n, nil
}

func (m *memChecks) DropRepository(_ context.Context, repository int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, row := range m.rows {
		if row.RepositoryID == repository {
			delete(m.rows, k)
			n++
		}
	}
	return n, nil
}

func (m *memChecks) Check(_ context.Context, repository int64, pullRequest int) (domain.Check, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[checkKey{repository, pullRequest}]
	if !ok {
		return domain.Check{}, app.ErrNotFound
	}
	return *row, nil
}

// memSources is what the other contexts say about the branch. Tests set
// it the way a push, a usages upload or a translation would.
type memSources struct {
	mu      sync.Mutex
	policy  checkpolicy.Policy
	status  app.BranchStatus
	quality app.BranchQuality
	usages  app.BranchUsages
	// recorded are the check runs CI recorded, by commit: what
	// `glossa check` found and the server stored, which is what the
	// pull request renders (RFC 0005 §12.3).
	recorded map[string]app.RecordedRun
	// recordedBefore is a project that has recorded a run of some other
	// commit, one the test does not otherwise care about. It makes the
	// check wait for this commit's run.
	recordedBefore bool
	// historyAsked counts how often the check asked whether the project
	// records runs at all, so a test can hold it to asking only when the
	// answer matters.
	historyAsked int
	manifest     string
}

func newMemSources() *memSources {
	return &memSources{
		status:   app.BranchStatus{Name: branchName, Outdated: map[string]int{}},
		quality:  app.BranchQuality{Locales: []string{"de", "fr"}, Untranslated: map[string]int{}},
		recorded: map[string]app.RecordedRun{},
	}
}

// record is CI recording a `glossa check` run for a commit.
func (m *memSources) record(commit string, run app.RecordedRun) {
	m.set(func(s *memSources) {
		run.Commit = commit
		s.recorded[commit] = run
	})
}

// RecordedRun answers the run recorded for exactly this commit. A
// commit nobody checked has none, which is the fallback's case.
func (m *memSources) RecordedRun(_ context.Context, _ uuid.UUID, commit string) (app.RecordedRun, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.recorded[commit]
	return run, ok, nil
}

// RecordsRuns answers whether the project has ever recorded a run: any
// run on the record, of any commit.
func (m *memSources) RecordsRuns(context.Context, uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.historyAsked++
	return m.recordedBefore || len(m.recorded) > 0, nil
}

func (m *memSources) set(fn func(*memSources)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(m)
}

func (m *memSources) Policy(context.Context, uuid.UUID) (checkpolicy.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policy, nil
}

func (m *memSources) BranchStatus(context.Context, uuid.UUID, string) (app.BranchStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status, nil
}

func (m *memSources) BranchQuality(context.Context, uuid.UUID, string, []string) (app.BranchQuality, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.quality, nil
}

func (m *memSources) BranchUsages(context.Context, uuid.UUID, string) (app.BranchUsages, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usages, nil
}

func (m *memSources) ManifestURL(context.Context, uuid.UUID, string, int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.manifest, nil
}
