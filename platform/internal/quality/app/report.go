package app

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// A check that ran somewhere else is recorded here (RFC 0005 §9): the
// create half of "check runs (create, read, list)". `glossa check` in a
// product's CI computes the layers against the project it already read,
// and posts what it found, so Studio's quality view, `listFindings`,
// the summary and the findings-by-day rollup see the runs that gate
// that product's builds. Without it, the surfaces stay empty for
// exactly the projects M4 targets.
//
// It is a fourth caller of RecordCheckRun, not a second use case: the
// capture ingest (§5.1), the linguistic job (§3.8) and the write-time
// job all end in the same store call, with the same waiver application
// and the same day rollup. What this one adds is what a *reporter*
// cannot be trusted with, and the two members it completes are the
// reason it exists at all rather than being a passthrough:
//
//   - The **fingerprint** is computed by domain.New, over the catalog
//     message ID the key resolved to. A reporter has the key; the
//     server has the catalog. A print minted over a key is not the one
//     the waiver list, the findings list and the pull-request check
//     compute for the same finding, so every waiver against it would
//     silently stop applying. ReportedFinding therefore has no
//     fingerprint field at all — it is not a value the ingest ignores,
//     it is a value the ingest cannot be handed.
//   - The **severity and the verdict** are the policy's. The reporter
//     says what its layer emitted; domain.Evaluate decides what that is
//     worth in this locale, this namespace and this environment, drops
//     what a rule switched off, and clamps an advisory layer back to
//     warning. There is no conclusion and no counts on the way in: a
//     caller that could assert those could declare its own build green.

// ReportedLocus is as much of a finding's locus as a reporter knows.
//
// It is domain.Locus minus Message, Capture and Region. Message is
// resolved here from Key, for the reason above. Capture and Region are
// the capture ingest's to mint — only the server that gave a capture
// its ID can pair a region with it — and a check run is of a catalog,
// not of a screenshot.
type ReportedLocus struct {
	Key       string
	Locale    string
	Revision  string
	Namespace string
	File      string
	Line      int
	Column    int
	Route     string
	Component string
	Span      *domain.Span
}

// ReportedFinding is one finding as a reporter hands it over: a
// glossa.finding/v1 finding without the identity only the catalog can
// give it.
type ReportedFinding struct {
	Layer domain.Layer
	Code  string
	// Severity is what the layer emitted. The policy decides what it is
	// worth; `waived` is refused, because a waiver is the project's to
	// apply and not a reporter's to assert.
	Severity domain.Severity
	Locus    ReportedLocus
	// Explanation is the sentence for a person. Its wording is not
	// stable and it is deliberately not fingerprinted.
	Explanation string
	// Subject is what the finding names. It is part of the fingerprint,
	// because two missing arguments in one message are two findings.
	Subject  string
	Detail   string
	Evidence map[string]any
	Fix      *domain.Fix
	// SourceRevision is the source revision the layer graded against —
	// the server's own number, read back from the translation. A waiver
	// dies when it changes (RFC 0005 §2.3).
	SourceRevision *int
}

// ReportCheckRun is one evaluation to record: what was checked, which
// layers ran, and what they found.
type ReportCheckRun struct {
	Project uuid.UUID
	// Ref is the branch or environment that was checked, and Commit the
	// commit graded where there is one.
	Ref     string
	Commit  string
	Trigger domain.Trigger
	// Environment selects the policy's block for it; "" is a branch
	// check, in no environment at all, which is every check in CI.
	Environment string
	// Layers are the layers that actually ran, in report order, so a
	// reader can tell "clean" from "not looked at".
	Layers    []domain.Layer
	Findings  []ReportedFinding
	StartedAt time.Time
}

// ReportableTriggers are the triggers a caller may claim. `capture` and
// `write` are the server's own jobs — the capture upload's visual pass
// and the write-time catalog check — and a caller that could claim one
// could put words in a job's mouth.
var ReportableTriggers = []domain.Trigger{domain.TriggerCLI, domain.TriggerPullRequest, domain.TriggerAPI}

// ReportCheckRun records a check that ran somewhere else, graded
// against the project's stored policy.
//
// The order matters and is the same order every other storing path
// takes: authorize, refuse what is too big, seal, grade, record. The
// cap is checked before anything is resolved or hashed, so a run past
// it costs a key lookup of nothing, and it is **refused rather than
// truncated** (RFC 0005 §10) — a silently shortened run is a report
// that lies about what was checked.
func (s *Service) ReportCheckRun(ctx context.Context, in ReportCheckRun) (run domain.CheckRun, err error) {
	if _, err := s.write(ctx, in.Project); err != nil {
		return domain.CheckRun{}, err
	}
	if len(in.Findings) > MaxRunFindings {
		return domain.CheckRun{}, fmt.Errorf("%w: %d findings, at most %d", ErrTooManyFindings, len(in.Findings), MaxRunFindings)
	}
	trigger := in.Trigger
	if trigger == "" {
		trigger = domain.TriggerAPI
	}
	if !slices.Contains(ReportableTriggers, trigger) {
		return domain.CheckRun{}, fmt.Errorf("%w: %q", ErrUnclaimableTrigger, trigger)
	}
	for _, f := range in.Findings {
		if err := f.validate(); err != nil {
			return domain.CheckRun{}, err
		}
	}
	ctx, end := s.span(ctx, "quality.report_check_run",
		attribute.String("glossa.project_id", in.Project.String()),
		attribute.String("glossa.quality.ref", in.Ref),
		attribute.String("glossa.quality.trigger", string(trigger)),
		attribute.Int("glossa.quality.findings", len(in.Findings)))
	defer end(&err)

	ids, err := s.messageIDs(ctx, in.Project, in.Findings)
	if err != nil {
		return domain.CheckRun{}, err
	}
	sealed := make([]domain.Finding, 0, len(in.Findings))
	for _, f := range in.Findings {
		sealed = append(sealed, f.seal(ids))
	}
	stored, err := s.storedPolicy(ctx, in.Project)
	if err != nil {
		return domain.CheckRun{}, err
	}
	ev := domain.Evaluate(stored.Policy, in.Environment, sealed)
	// A run with nothing left is still recorded, unlike a capture
	// upload's or a job's: "this ref was checked and found nothing" is
	// the answer a dashboard needs most, and dropping it would leave a
	// green project looking unchecked.
	return s.RecordCheckRun(ctx, RecordRun{
		Project: in.Project, Ref: in.Ref, Commit: in.Commit, Trigger: trigger,
		PolicyVersion: ev.PolicyVersion, Layers: in.Layers, Policy: stored.Policy,
		Findings: ev.Findings(), StartedAt: in.StartedAt,
	})
}

// validate refuses a finding the ingest cannot seal. A finding with no
// layer, no code or no explanation has no identity and nothing to say;
// storing one half-formed would put a row in the table that nobody can
// waive, filter or read.
func (f ReportedFinding) validate() error {
	switch {
	case !f.Layer.Valid():
		return fmt.Errorf("%w: %q is not a layer", ErrInvalidFinding, f.Layer)
	case f.Code == "":
		return fmt.Errorf("%w: a finding names the rule it applied", ErrInvalidFinding)
	case f.Explanation == "":
		return fmt.Errorf("%w: %s %s explains nothing", ErrInvalidFinding, f.Layer, f.Code)
	case f.Severity == domain.Waived:
		// The same refusal RecordCheckRun makes, made where the caller
		// is: a reporter applying a waiver could waive anything.
		return fmt.Errorf("%w: %s %s", ErrPreGradedFinding, f.Layer, f.Code)
	case f.Severity != domain.Error && f.Severity != domain.Warning:
		return fmt.Errorf("%w: %s %s carries severity %q, which no policy can rank",
			ErrInvalidFinding, f.Layer, f.Code, f.Severity)
	}
	return nil
}

// seal is the finding with its identity computed: the catalog message
// where the key resolved to one, and domain.New's fingerprint over it.
//
// A key the catalog does not know leaves Message empty, and the
// fingerprint then hashes the key — exactly what an offline `glossa
// check` prints, and the honest answer, because nothing said what that
// key is called.
func (f ReportedFinding) seal(ids map[string]uuid.UUID) domain.Finding {
	locus := domain.Locus{
		Key: f.Locus.Key, Locale: f.Locus.Locale, Revision: f.Locus.Revision,
		Namespace: f.Locus.Namespace, File: f.Locus.File, Line: f.Locus.Line,
		Column: f.Locus.Column, Route: f.Locus.Route, Component: f.Locus.Component,
		Span: f.Locus.Span,
	}
	if id, ok := ids[f.Locus.Key]; ok && id != uuid.Nil {
		locus.Message = id.String()
	}
	return domain.New(domain.Finding{
		Layer: f.Layer, Code: f.Code, Severity: f.Severity, Locus: locus,
		Message: f.Explanation, Subject: f.Subject, Detail: f.Detail,
		Evidence: f.Evidence, Fix: f.Fix, SourceRevision: f.SourceRevision,
	})
}

// messageIDs resolves the distinct keys the findings name, once each.
// A run of 10 000 findings over a hundred messages asks about a
// hundred keys.
func (s *Service) messageIDs(
	ctx context.Context, project uuid.UUID, fs []ReportedFinding,
) (map[string]uuid.UUID, error) {
	keys := make([]string, 0, len(fs))
	seen := make(map[string]struct{}, len(fs))
	for _, f := range fs {
		if _, had := seen[f.Locus.Key]; f.Locus.Key == "" || had {
			continue
		}
		seen[f.Locus.Key] = struct{}{}
		keys = append(keys, f.Locus.Key)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	return s.catalog.MessageIDs(ctx, project, keys)
}
