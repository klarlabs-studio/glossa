package app

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The check policy as a resource (RFC 0005 §4, §13 wave 3): read it,
// write it, see what a write would do before doing it, and read how it
// got to be what it is.
//
// Two things are deliberately kept apart. The **document that grades**
// is Catalog's, in the project's settings, because that is where
// `glossa check` and the pull-request check already read it and the one
// property this whole area exists to protect is that those two never
// disagree. The **record of every version** is Quality's, in its own
// table, because a policy is an organizational decision about a project
// and the decision outlives the version that carried it.
//
// Permissions follow the rest of the context (Service): reading needs
// `catalog.read`, writing needs `catalog.write` — the permission that
// already carries the authority to change what a project's check
// concludes.

// The impact preview is bounded, because a project with a year of runs
// must not turn a dry run into a table scan. It is measured against the
// newest run of each of the most recently checked refs, which is
// exactly the set a policy change can break today: an older run of a
// ref has already been superseded by a newer one.
const (
	// previewScanRuns is how many runs are read to find those refs.
	previewScanRuns = 200
	// previewRefs is how many refs the preview covers.
	previewRefs = 20
	// previewFindingsPerRun bounds one run's findings in the preview.
	previewFindingsPerRun = 1000
)

// PolicyState is the project's live check policy with the record of who
// put it there.
type PolicyState struct {
	// Policy is the document that grades, history and all.
	Policy checkpolicy.Policy
	// CreatedBy and CreatedAt are from the version's history row. They
	// are empty for a policy that predates the policy API — a project
	// that has never saved one, or one whose three fields were set
	// through the project-settings API, which keeps no history.
	CreatedBy string
	CreatedAt time.Time
}

// CheckPolicy reads the project's check policy.
func (s *Service) CheckPolicy(ctx context.Context, project uuid.UUID) (state PolicyState, err error) {
	ctx, end := s.span(ctx, "quality.check_policy", attribute.String("project.id", project.String()))
	defer end(&err)
	if err := s.read(ctx, project); err != nil {
		return PolicyState{}, err
	}
	stored, err := s.storedPolicy(ctx, project)
	if err != nil {
		return PolicyState{}, err
	}
	return s.stateOf(ctx, project, stored.Policy)
}

// stateOf pairs a document with its history row, where there is one. A
// version nobody recorded is not an error: every policy written before
// this API has none, and the document is still the truth.
func (s *Service) stateOf(ctx context.Context, project uuid.UUID, p checkpolicy.Policy) (PolicyState, error) {
	out := PolicyState{Policy: p}
	if p.Version == 0 {
		return out, nil
	}
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		v, err := st.PolicyVersion(ctx, project, p.Version)
		if err != nil {
			return err
		}
		out.CreatedBy, out.CreatedAt = v.CreatedBy, v.CreatedAt
		return nil
	})
	if err != nil && !isNotFound(err) {
		return PolicyState{}, err
	}
	return out, nil
}

func isNotFound(err error) bool {
	return errors.Is(err, ErrPolicyVersionNotFound) || errors.Is(err, ErrNotFound)
}

// SavePolicy is a check-policy write.
type SavePolicy struct {
	// Policy is the document to store. It carries what the policy says
	// and none of the server's bookkeeping (domain.NextPolicy).
	Policy checkpolicy.Policy
	// Grace is how long the save pins the pull requests that predate it
	// to the version they were opened under. nil is
	// checkpolicy.DefaultGrace — the default protects the people who did
	// not cause the change — and zero pins nothing, which is what a
	// policy that only loosens wants.
	Grace *time.Duration
	// DryRun answers the impact preview and stores nothing: neither the
	// document nor a version.
	DryRun bool
}

// PolicySave is what a write did, or — for a dry run — what it would
// have done.
type PolicySave struct {
	// DryRun says nothing was stored.
	DryRun bool
	// State is the policy as it now stands, or as it would stand.
	State PolicyState
	// Impact is what the candidate changes about the findings and the
	// pull requests the project already has (RFC 0005 §4.3). It is
	// computed for every save, not only a dry run: a reader of the audit
	// trail deserves the same number the author saw.
	Impact Preview
}

// SavePolicy stores a new version of the project's check policy, or —
// with DryRun — answers what doing so would change and stores nothing.
//
// The order is: read the current document, place the candidate in front
// of it, measure the two against the runs that exist, then write. The
// document goes to Catalog first because it is the one that grades; the
// history row follows, and a row already there is not an error, because
// the live document is the truth and a repeated append is not a second
// decision.
func (s *Service) SavePolicy(ctx context.Context, project uuid.UUID, in SavePolicy) (out PolicySave, err error) {
	ctx, end := s.span(ctx, "quality.save_check_policy",
		attribute.String("project.id", project.String()), attribute.Bool("policy.dry_run", in.DryRun))
	defer end(&err)
	actor, err := s.write(ctx, project)
	if err != nil {
		return PolicySave{}, err
	}
	stored, err := s.storedPolicy(ctx, project)
	if err != nil {
		return PolicySave{}, err
	}
	grace := checkpolicy.DefaultGrace
	if in.Grace != nil {
		grace = *in.Grace
	}
	now := s.now()
	next, err := domain.NextPolicy(stored.Policy, in.Policy, now, grace)
	if err != nil {
		return PolicySave{}, err
	}
	impact, err := s.previewAgainstStoredRuns(ctx, project, stored.Policy, next, now)
	if err != nil {
		return PolicySave{}, err
	}
	out = PolicySave{DryRun: in.DryRun, Impact: impact, State: PolicyState{Policy: next}}
	if in.DryRun {
		return out, nil
	}
	if err := s.catalog.SaveCheckPolicy(ctx, project, stored.ProjectVersion, next); err != nil {
		return PolicySave{}, err
	}
	out.State.CreatedBy, out.State.CreatedAt = actor, now
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		// The version stores the document without its history: the
		// record is the sequence of rows, and keeping `previous` in
		// every one of them would store each version twice.
		_, err := st.InsertPolicyVersion(ctx, PolicyVersion{
			ID: uuid.New(), Project: project, Version: next.Version, Policy: next.Current(),
			CreatedBy: actor, CreatedAt: now,
		})
		return err
	})
	if err != nil {
		return PolicySave{}, err
	}
	s.metrics.PolicyVersionRead(project, next.Version)
	s.logger.InfoContext(ctx, "check policy saved",
		"project", project, "version", next.Version, "rules", len(next.Rules),
		"newly_failing", impact.NewlyFailing, "open_pull_requests", impact.OpenPullRequests)
	return out, nil
}

// previewAgainstStoredRuns measures candidate against current over the
// runs the project already has.
//
// Every run is graded in no environment, because a run records the ref
// it checked and not whether that ref was an environment — which is
// right for the number this answers: the pull requests a change would
// break are branch checks, and a branch check runs in no environment at
// all.
func (s *Service) previewAgainstStoredRuns(
	ctx context.Context, project uuid.UUID, current, candidate checkpolicy.Policy, now time.Time,
) (Preview, error) {
	open, err := s.catalog.OpenPullRequests(ctx, project)
	if err != nil {
		return Preview{}, err
	}
	var runs []PreviewRun
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		stored, err := st.ListCheckRuns(ctx, project, RunFilter{}, nil, previewScanRuns)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, r := range stored {
			if seen[r.Ref] {
				// Runs come back newest first, so the first run of a ref
				// is its newest — the only one a policy change can still
				// turn red.
				continue
			}
			if len(runs) == previewRefs {
				break
			}
			seen[r.Ref] = true
			rows, err := st.ListFindings(ctx, r, FindingFilter{}, "", previewFindingsPerRun, now)
			if err != nil {
				return err
			}
			pr := PreviewRun{Ref: r.Ref, PullRequest: open[r.Ref], Open: open[r.Ref] > 0}
			for _, row := range rows {
				pr.Findings = append(pr.Findings, row.Finding)
			}
			runs = append(runs, pr)
		}
		return nil
	})
	if err != nil {
		return Preview{}, err
	}
	// Both documents are compared as they grade — without their
	// history — so the preview answers about this version and not about
	// the one a pull request is pinned to.
	preview := PreviewPolicy(current.Current(), candidate.Current(), runs)
	if err := s.linkPullRequests(ctx, project, preview.PullRequests); err != nil {
		return Preview{}, err
	}
	return preview, nil
}

// SetPullRequestLinks wires Integration's answer to where a project's
// pull requests are. It is a setter because Integration's GitHub service
// is built after Quality and reads Quality's check runs.
func (s *Service) SetPullRequestLinks(l PullRequestLinks) { s.links = l }

// linkPullRequests fills in where each pull request is, when something
// knows. A caller who may not read the integration still gets the pull
// requests by number — the link is the extra, never the verdict — but
// any other failure fails the preview: a preview that silently lost its
// links would look like one that had none to give.
func (s *Service) linkPullRequests(ctx context.Context, project uuid.UUID, prs []PullRequestImpact) error {
	if s.links == nil || len(prs) == 0 {
		return nil
	}
	numbers := make([]int, len(prs))
	for i, pr := range prs {
		numbers[i] = pr.Number
	}
	urls, err := s.links.PullRequestURLs(ctx, project, numbers)
	switch {
	case errors.Is(err, authz.ErrForbidden):
		return nil
	case err != nil:
		return err
	}
	for i := range prs {
		prs[i].URL = urls[prs[i].Number]
	}
	return nil
}

// ListPolicyVersions pages the project's policy versions, newest first.
func (s *Service) ListPolicyVersions(
	ctx context.Context, project uuid.UUID, page pagination.Page,
) (vs []PolicyVersion, next *string, err error) {
	ctx, end := s.span(ctx, "quality.list_policy_versions", attribute.String("project.id", project.String()))
	defer end(&err)
	if err := s.read(ctx, project); err != nil {
		return nil, nil, err
	}
	after, err := afterVersion(page.After)
	if err != nil {
		return nil, nil, err
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		vs, err = st.ListPolicyVersions(ctx, project, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, token := pagination.Trim(vs, page, func(v PolicyVersion) string { return strconv.Itoa(v.Version) })
	return items, token, nil
}

// afterVersion reads the page token: the version the previous page
// ended on. The version is monotonic and unique per project, so it is
// the whole cursor.
func afterVersion(token string) (*int, error) {
	if token == "" {
		return nil, nil
	}
	v, err := strconv.Atoi(token)
	if err != nil || v < 0 {
		return nil, invalidPageToken()
	}
	return &v, nil
}

// PolicyVersion reads one version of the project's check policy.
func (s *Service) PolicyVersion(
	ctx context.Context, project uuid.UUID, version int,
) (v PolicyVersion, err error) {
	ctx, end := s.span(ctx, "quality.policy_version",
		attribute.String("project.id", project.String()), attribute.Int("policy.version", version))
	defer end(&err)
	if err := s.read(ctx, project); err != nil {
		return PolicyVersion{}, err
	}
	if version < 0 {
		return PolicyVersion{}, ErrPolicyVersionNotFound
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		v, err = st.PolicyVersion(ctx, project, version)
		return err
	})
	if err != nil {
		return PolicyVersion{}, err
	}
	return v, nil
}
