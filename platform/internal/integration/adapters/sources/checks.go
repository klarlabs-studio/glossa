package sources

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	contextapp "go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	knowledgeapp "go.klarlabs.de/glossa/platform/internal/knowledge/app"
	knowledgedomain "go.klarlabs.de/glossa/platform/internal/knowledge/domain"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
	quality "go.klarlabs.de/glossa/platform/internal/quality/domain"
	releaseapp "go.klarlabs.de/glossa/platform/internal/release/app"
	releasedelivery "go.klarlabs.de/glossa/platform/internal/release/delivery"
)

// Checks implements app.CheckSources: the read model the Glossa PR
// check renders from (RFC 0004 §6.4).
//
// It reaches the other bounded contexts through their application
// services, as RFC 0002 §4 requires, so each keeps checking the
// caller's permissions — the check worker's background principal holds
// exactly catalog.read, translations.read, knowledge.read and
// releases.read, and a project it may not read answers accordingly.
type Checks struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
	knowledge    *knowledgeapp.Service
	usages       *contextapp.Service
	release      *releaseapp.Service
	// quality is where the run CI recorded is read from (RFC 0005
	// §12.3), through Quality's own application service and never its
	// tables. Nil in a deployment that does not wire it, and the check
	// then always renders its reduced view — and says so.
	quality *qualityapp.Service
	// edgeURL is GLOSSA_EDGE_PUBLIC_URL; without one the comment names
	// no manifest, because there is no address to give.
	edgeURL string
}

// ChecksDeps are the services the check reads.
type ChecksDeps struct {
	Catalog      *catalogapp.Service
	Localization *localizationapp.Service
	Knowledge    *knowledgeapp.Service
	Usages       *contextapp.Service
	Release      *releaseapp.Service
	Quality      *qualityapp.Service
	EdgeURL      string
}

// NewChecks returns the check's read model.
func NewChecks(d ChecksDeps) *Checks {
	return &Checks{
		catalog: d.Catalog, localization: d.Localization, knowledge: d.Knowledge,
		usages: d.Usages, release: d.Release, quality: d.Quality,
		edgeURL: strings.TrimRight(d.EdgeURL, "/"),
	}
}

var _ app.CheckSources = (*Checks)(nil)

// Policy implements app.CheckSources: the project's stored check
// policy (Catalog's settings.check_policy).
//
// The policy is `glossa check`'s, shared through kernel/checkpolicy so
// the pull request and the terminal never disagree — and read from the
// project, so they agree about the same settings rather than by both
// falling back to the same defaults. A project that has never set one
// reads as the zero policy, which is the documented default: every
// locale must be complete, and errors fail.
func (c *Checks) Policy(ctx context.Context, project uuid.UUID) (checkpolicy.Policy, error) {
	p, err := c.catalog.GetProject(ctx, catalogdomain.ProjectID(project))
	if err != nil {
		return checkpolicy.Policy{}, err
	}
	return p.Settings.Policy(), nil
}

// maxRunPages bounds the read of a recorded run's findings. A run holds
// at most quality's MaxRunFindings, and the page size is the API's, so
// this is that ceiling expressed in pages rather than a limit of its
// own: a run is read whole or reported as truncated, never quietly
// shortened.
const maxRunPages = qualityapp.MaxRunFindings / pagination.MaxPageSize

// RecordedRun implements app.CheckSources: the newest check run
// recorded for this commit, with its findings as they stand now.
//
// It is one read of one stored run — the same read `glossa findings`
// and Studio's quality view make, through the same application service
// — so the pull request cannot be looking at a different arithmetic
// from theirs. The commit is the key: a branch moves under a pull
// request, and the run that graded a commit is the run that graded that
// commit forever (RFC 0005 §12.3).
func (c *Checks) RecordedRun(ctx context.Context, project uuid.UUID, commit string) (app.RecordedRun, bool, error) {
	if c.quality == nil || commit == "" {
		return app.RecordedRun{}, false, nil
	}
	run, ok, err := c.reportedRun(ctx, project, commit)
	if err != nil || !ok {
		return app.RecordedRun{}, false, err
	}
	out := app.RecordedRun{
		ID: run.ID, Ref: run.Ref, Commit: run.Commit, Trigger: string(run.Trigger),
		PolicyVersion: run.PolicyVersion, Layers: run.Layers,
		StartedAt: run.StartedAt, CompletedAt: run.CompletedAt,
	}
	page := pagination.Page{Size: pagination.MaxPageSize}
	for range maxRunPages {
		got, err := c.quality.ListFindings(ctx, project, qualityapp.FindingQuery{Run: run.ID}, page)
		if err != nil {
			return app.RecordedRun{}, false, err
		}
		for _, f := range got.Items {
			out.Findings = append(out.Findings, f.Finding)
		}
		if got.Next == nil {
			return out, true, nil
		}
		if page, err = pagination.Parse(&page.Size, got.Next); err != nil {
			return app.RecordedRun{}, false, err
		}
	}
	out.Truncated = true
	return out, true, nil
}

// RecordsRuns implements app.CheckSources: whether the project has ever
// recorded a reported run, read as one existence check through
// Quality's service. A deployment without Quality records nothing, so
// its checks never wait for a run.
func (c *Checks) RecordsRuns(ctx context.Context, project uuid.UUID) (bool, error) {
	if c.quality == nil {
		return false, nil
	}
	return c.quality.HasReportedRun(ctx, project)
}

// reportedRun is the newest run of this commit that somebody *reported*
// — `glossa check` in CI, the pull-request check, or an explicit API
// call — and never one of the server's own jobs.
//
// The distinction matters and is not a nicety. A capture upload records
// a `capture` run of the same commit holding the visual pass and
// nothing else, and the write-time job records a `write` run of the
// catalog layers alone. Either would be "the newest run of this commit"
// and neither is the check that gated the build; rendering one would
// put a pull request on a partial verdict and call it the terminal's.
func (c *Checks) reportedRun(ctx context.Context, project uuid.UUID, commit string) (quality.CheckRun, bool, error) {
	var newest quality.CheckRun
	var found bool
	for _, trigger := range qualityapp.ReportableTriggers {
		runs, _, err := c.quality.ListCheckRuns(ctx, project,
			qualityapp.RunFilter{Commit: commit, Trigger: string(trigger)},
			pagination.Page{Size: 1})
		if err != nil {
			return quality.CheckRun{}, false, err
		}
		if len(runs) == 0 {
			continue
		}
		if !found || runs[0].StartedAt.After(newest.StartedAt) {
			newest, found = runs[0], true
		}
	}
	return newest, found, nil
}

// BranchStatus implements app.CheckSources.
func (c *Checks) BranchStatus(ctx context.Context, project uuid.UUID, branch string) (app.BranchStatus, error) {
	rep, err := c.catalog.BranchStatus(ctx, catalogdomain.ProjectID(project), branch)
	if errors.Is(err, catalogapp.ErrNotFound) {
		// CI has not pushed this branch yet: an empty status, not a
		// failure. The check then simply waits.
		return app.BranchStatus{Name: branch, Outdated: map[string]int{}}, nil
	}
	if err != nil {
		return app.BranchStatus{}, err
	}
	out := app.BranchStatus{
		Name: string(rep.Branch.Name), PR: rep.Branch.PR, HeadCommit: rep.Branch.HeadCommit,
		State: string(rep.Branch.State), PreviewURL: rep.Branch.PreviewURL,
		NewKeys: keys(rep.NewKeys), SourceProposals: keys(rep.SourceProposals), Removed: keys(rep.Removed),
		Outdated: rep.Outdated,
	}
	for _, m := range rep.Invalid {
		out.Invalid = append(out.Invalid, app.InvalidMessage{Key: m.Key, Code: m.Code, Detail: m.Detail})
	}
	for _, k := range rep.Conflicts {
		conflict := app.KeyConflict{Key: string(k.Key)}
		for _, b := range k.Branches {
			conflict.Branches = append(conflict.Branches, string(b))
		}
		out.Conflicts = append(out.Conflicts, conflict)
	}
	if out.Outdated == nil {
		out.Outdated = map[string]int{}
	}
	return out, nil
}

func keys(ks []catalogdomain.MessageKey) []string {
	out := make([]string, 0, len(ks))
	for _, k := range ks {
		out = append(out, string(k))
	}
	return out
}

// maxQAPages bounds the QA scan. A pull request's check is a report,
// not an audit: the first few hundred translations of the project's
// locales are enough to say what is wrong on the branch, and the
// terminology layer has its own page limit for the same reason.
const maxQAPages = 5

// BranchQuality implements app.CheckSources.
func (c *Checks) BranchQuality(ctx context.Context, project uuid.UUID, branch string, keys []string) (app.BranchQuality, error) {
	out := app.BranchQuality{Untranslated: map[string]int{}}
	locales, err := c.locales(ctx, project)
	if err != nil {
		return out, err
	}
	out.Locales = locales
	ids, err := c.newKeyMessages(ctx, project, branch)
	if err != nil {
		return out, err
	}
	if len(ids) > 0 {
		// CurrentTranslations counts the usable translations of those
		// messages per locale — counts, not text — so what is left is
		// what nobody has written yet.
		counts, err := c.localization.CurrentTranslations(ctx, project, ids)
		if err != nil {
			return out, err
		}
		for _, l := range locales {
			out.Untranslated[l] = len(ids) - counts[l]
		}
	}
	if out.Findings, err = c.qaFindings(ctx, project, locales, keys); err != nil {
		return out, err
	}
	return out, nil
}

// locales lists the project's target locales.
func (c *Checks) locales(ctx context.Context, project uuid.UUID) ([]string, error) {
	tags, err := (&Localization{svc: c.localization}).Locales(ctx, project)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, t.String())
	}
	return out, nil
}

// newKeyMessages are the messages the branch proposes as new keys.
func (c *Checks) newKeyMessages(ctx context.Context, project uuid.UUID, branch string) ([]uuid.UUID, error) {
	o, err := c.catalog.BranchOverlay(ctx, catalogdomain.ProjectID(project), branch)
	if errors.Is(err, catalogapp.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for _, p := range o.Proposals {
		if p.Kind == catalogdomain.ProposalNewKey {
			out = append(out, p.MessageID.UUID())
		}
	}
	return out, nil
}

// qaFindings collects the QA the server already holds on the branch's
// messages: the structural warnings Localization stored with each text
// (max_length among them) and the terminology findings of M2.
func (c *Checks) qaFindings(ctx context.Context, project uuid.UUID, locales, branchKeys []string) ([]quality.Finding, error) {
	if len(locales) == 0 || len(branchKeys) == 0 {
		return nil, nil
	}
	wanted := map[string]bool{}
	for _, k := range branchKeys {
		wanted[k] = true
	}
	out, err := c.storedWarnings(ctx, project, locales, wanted)
	if err != nil {
		return nil, err
	}
	terms, err := c.terminology(ctx, project, locales, wanted)
	if err != nil {
		return nil, err
	}
	return append(out, terms...), nil
}

// storedWarnings reads the structural QA Localization kept with each
// translation when it was written (max-length-exceeded and the compat
// warnings), for the branch's own keys.
func (c *Checks) storedWarnings(ctx context.Context, project uuid.UUID, locales []string, wanted map[string]bool) ([]quality.Finding, error) {
	var out []quality.Finding
	page := pagination.Page{Size: pagination.MaxPageSize}
	for range maxQAPages {
		rows, next, err := c.localization.ListProjectTranslations(ctx, project,
			localizationapp.TranslationFilter{Locales: locales}, page)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !wanted[r.Key] {
				continue
			}
			for _, w := range r.Warnings {
				// The kernel's subject and qualifier survive now, and so
				// does which message and which revision this is about.
				out = append(out, quality.New(quality.Finding{
					Layer: quality.LayerParity, Code: string(w.Code), Severity: severity(w.Severity),
					Locus: quality.Locus{
						Message: r.MessageID.String(), Key: r.Key, Locale: r.Locale.String(),
						Namespace: r.Namespace, Revision: r.ID.String(),
					},
					Message: w.Message, Subject: w.Subject, Detail: w.Detail,
					SourceRevision: sourceRevision(r.SourceRevision),
				}))
			}
		}
		if next == nil {
			return out, nil
		}
		if page, err = pagination.Parse(&page.Size, next); err != nil {
			return out, err
		}
	}
	return out, nil
}

func severity(s mf.Severity) checkpolicy.Severity {
	if s == mf.SeverityError {
		return checkpolicy.Error
	}
	return checkpolicy.Warning
}

// terminology asks Knowledge for the branch's terminology findings.
func (c *Checks) terminology(ctx context.Context, project uuid.UUID, locales []string, wanted map[string]bool) ([]quality.Finding, error) {
	if c.knowledge == nil {
		return nil, nil
	}
	tags := make([]bcp47.Tag, 0, len(locales))
	for _, l := range locales {
		t, err := bcp47.Parse(l)
		if err != nil {
			continue
		}
		tags = append(tags, t)
	}
	if len(tags) == 0 {
		return nil, nil
	}
	var out []quality.Finding
	page := pagination.Page{Size: pagination.MaxPageSize}
	for range maxQAPages {
		rep, err := c.knowledge.CheckProjectTerminology(ctx, project,
			knowledgeapp.ProjectTermCheck{Locales: tags, Page: page})
		if err != nil {
			return nil, err
		}
		for _, item := range rep.Items {
			if !wanted[item.Key] {
				continue
			}
			for _, f := range item.Findings {
				// The span and the offending text reach the pull request
				// too: Knowledge has computed them since M2, and the
				// conversion this replaces threw them away.
				out = append(out, quality.New(quality.Finding{
					Layer: quality.LayerTerminology, Code: string(f.Code), Severity: termSeverity(f.Severity),
					Locus: quality.Locus{
						Message: item.MessageID.String(), Key: item.Key, Locale: item.Locale.String(),
						Namespace: item.Namespace,
						Span:      &quality.Span{Side: quality.Side(f.Side), Start: f.Start, End: f.End},
					},
					Message: f.Message, Subject: f.Text, Evidence: termEvidence(f),
					// The source it was checked against, which a waiver
					// on it is measured against (RFC 0005 §2.3).
					SourceRevision: sourceRevision(item.SourceRevision),
				}))
			}
		}
		if rep.Next == nil {
			return out, nil
		}
		if page, err = pagination.Parse(&page.Size, rep.Next); err != nil {
			return out, err
		}
	}
	return out, nil
}

func termSeverity(s knowledgedomain.Severity) checkpolicy.Severity {
	if s == knowledgedomain.SeverityError {
		return checkpolicy.Error
	}
	return checkpolicy.Warning
}

// termEvidence keeps the concept and the allowed terms with the
// finding, so a reader can follow it back to the termbase.
func termEvidence(f knowledgedomain.TermFinding) map[string]any {
	out := map[string]any{"concept_id": f.ConceptID.String(), "term_id": f.TermID.String()}
	if len(f.Suggestions) > 0 {
		out["suggestions"] = f.Suggestions
	}
	return out
}

// sourceRevision is the revision a finding was computed against, which
// is what a waiver is measured against.
func sourceRevision(rev int) *int {
	if rev == 0 {
		return nil
	}
	return &rev
}

// maxUnknownPages bounds the unknown-key scan. The query already keeps
// only the unknown ones, so this is a cap on a pathological branch, not
// on the report.
const maxUnknownPages = 5

// BranchUsages implements app.CheckSources.
func (c *Checks) BranchUsages(ctx context.Context, project uuid.UUID, branch string) (app.BranchUsages, error) {
	var out app.BranchUsages
	builds, err := c.usages.CurrentBuilds(ctx, project, branch)
	if err != nil {
		return out, err
	}
	out.Builds = len(builds)
	for _, b := range builds {
		out.Commits = append(out.Commits, string(b.Commit))
	}
	seen := map[string]bool{}
	page := pagination.Page{Size: pagination.MaxPageSize}
	for range maxUnknownPages {
		p, err := c.usages.ListUsages(ctx, project, branch, contextapp.UsageFilter{Unknown: true}, page)
		if err != nil {
			return out, err
		}
		for _, u := range p.Usages {
			if seen[u.Key] {
				continue
			}
			seen[u.Key] = true
			out.Unknown = append(out.Unknown, app.UnknownKey{Key: u.Key, File: u.File, Line: u.Line})
		}
		if p.Next == nil {
			break
		}
		if page, err = pagination.Parse(&page.Size, p.Next); err != nil {
			return out, err
		}
	}
	if out.Where, err = c.where(ctx, project, branch); err != nil {
		return out, err
	}
	cov, err := c.usages.CaptureCoverage(ctx, project, branch)
	if err != nil {
		return out, err
	}
	out.Captured, out.NotCaptured = cov.Captured, cov.NotCaptured()
	return out, nil
}

// maxSitePages bounds the scan for where each key is used. A check run
// carries at most MaxAnnotations findings on the diff, so knowing where
// two thousand keys live is already far more than can be shown; the
// bound is there so a pathological branch cannot make the check page
// forever.
const maxSitePages = 20

// where is one usage site per key the branch's build has, which is what
// puts a finding on the diff.
//
// The first usage of a key wins. A key used in five components has five
// true answers and the check can only annotate one line; taking the
// first keeps the answer stable between renders, which matters more
// than which of the five it is — an annotation that moved every time
// the job ran would be sent again as a new one.
func (c *Checks) where(ctx context.Context, project uuid.UUID, branch string) (map[string]app.UsageSite, error) {
	out := map[string]app.UsageSite{}
	page := pagination.Page{Size: pagination.MaxPageSize}
	for range maxSitePages {
		p, err := c.usages.ListUsages(ctx, project, branch, contextapp.UsageFilter{}, page)
		if err != nil {
			return nil, err
		}
		for _, u := range p.Usages {
			if u.File == "" || u.Line <= 0 {
				continue
			}
			if _, seen := out[u.Key]; !seen {
				out[u.Key] = app.UsageSite{File: u.File, Line: u.Line}
			}
		}
		if p.Next == nil {
			return out, nil
		}
		if page, err = pagination.Parse(&page.Size, p.Next); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ManifestURL implements app.CheckSources: where the branch
// environment's manifest is served at the edge.
//
// It needs three things to exist — the environment, a delivery key that
// may read branch environments, and an announced edge — and answers ""
// rather than an error when any is missing: a comment with one link
// fewer is not a failed check.
func (c *Checks) ManifestURL(ctx context.Context, project uuid.UUID, branch string, pr int) (string, error) {
	if c.release == nil || c.edgeURL == "" {
		return "", nil
	}
	name := releasedelivery.BranchEnvironmentName(branch, pr)
	if _, err := c.release.GetEnvironment(ctx, project, name); err != nil {
		if errors.Is(err, releaseapp.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	keys, _, err := c.release.ListDeliveryKeys(ctx, project, pagination.Page{Size: pagination.MaxPageSize})
	if err != nil {
		return "", err
	}
	for _, k := range keys {
		if k.RevokedAt == nil && k.Scope.Allows(name) {
			return fmt.Sprintf("%s/v1/%s/%s/manifest.json", c.edgeURL, k.Key, name), nil
		}
	}
	return "", nil
}
