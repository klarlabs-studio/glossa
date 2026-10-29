package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// The linguistic layer's jobs (RFC 0005 §3.8, §13 wave 6).
//
// The linguistic layer is the one layer that needs a model, and that
// single fact decides everything below.
//
// It is **a job**, triggered explicitly or on a batch, and never a
// check: `glossa check` never calls an AI provider (RFC 0005 §14
// decision 2), so the check stays free, offline-capable and
// deterministic, which is what makes people run it. A check reports the
// linguistic findings a job stored; it never computes one.
//
// Its findings are **advisory**: `warning`, and a policy may not raise
// them to `error`. A model's opinion does not fail a build (§14
// decision 10). Three things enforce that, and none of them is new
// here:
//
//  1. A producer cannot assert a severity. LinguisticFinding — what the
//     Intelligence port hands back — has no severity member at all, and
//     SealLinguistic sets `warning`. This is the same shape the visual
//     ingest already has (context/domain refuses any severity but
//     `warning` from a page): the thing that found it does not get to
//     say what it is worth.
//  2. A policy cannot name one. checkpolicy.Rule.Validate refuses a
//     rule that gives an advisory layer `error` (ErrAdvisoryLayer),
//     which is where a project's policy write is refused.
//  3. A wildcard cannot raise one. checkpolicy.Policy.Decide clamps
//     Error to Warning for any target whose layer is advisory and marks
//     the decision Clamped — the rule that raises everything without
//     naming `linguistic` included. Evaluate runs Decide over these
//     findings exactly as it runs it over every other layer's, so the
//     clamp is reached on the one path that grades.
//
// The kernel owns the advisory list (checkpolicy.AdvisoryLayers), so
// nothing here decides that `linguistic` is advisory; it only makes
// sure a finding of that layer can never be born with another severity.

// LinguisticCodes are the codes the linguistic layer may emit
// (RFC 0005 §3.8). The list is closed: a model that invents a code has
// produced malformed output, which is rejected exactly as a malformed
// draft is (RFC 0003 §3.2).
var LinguisticCodes = []string{
	"meaning-divergence",
	"tone-mismatch",
	"grammar-suspected",
	"inconsistent-phrasing",
}

// LinguisticCode reports whether c is one of the layer's codes.
func LinguisticCode(c string) bool { return slices.Contains(LinguisticCodes, c) }

// LinguisticJobState is where a linguistic-QA job stands.
type LinguisticJobState string

// Linguistic-QA job states. queued → running → succeeded | failed;
// a job that has not finished can be cancelled.
const (
	LinguisticQueued    LinguisticJobState = "queued"
	LinguisticRunning   LinguisticJobState = "running"
	LinguisticSucceeded LinguisticJobState = "succeeded"
	LinguisticFailed    LinguisticJobState = "failed"
	LinguisticCancelled LinguisticJobState = "cancelled"
)

// LinguisticJobStates lists every state.
var LinguisticJobStates = []LinguisticJobState{
	LinguisticQueued, LinguisticRunning, LinguisticSucceeded, LinguisticFailed, LinguisticCancelled,
}

// ParseLinguisticJobState validates s.
func ParseLinguisticJobState(s string) (LinguisticJobState, error) {
	if st := LinguisticJobState(s); slices.Contains(LinguisticJobStates, st) {
		return st, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidLinguisticState, s)
}

// Final reports whether the job is over.
func (s LinguisticJobState) Final() bool {
	return s != LinguisticQueued && s != LinguisticRunning
}

// Failure codes a linguistic-QA job ends with. They are part of the API
// contract, and they are spelled exactly as Intelligence's job failures
// are (intelligence/domain), because they are the same refusals seen
// from the other side of the port: a caller that already knows what
// `provider_consent` means on a translation job does not learn a second
// vocabulary for the same thing.
const (
	// LinguisticFailureProviderConsent: the tenant has not enabled
	// sending text to AI providers (RFC 0003 §7).
	LinguisticFailureProviderConsent = "provider_consent"
	// LinguisticFailureSensitive: every namespace the job selected is
	// tagged `sensitive`, so there was nothing it was allowed to send.
	LinguisticFailureSensitive = "sensitive"
	// LinguisticFailureBudgetExceeded: the tenant's monthly AI budget is
	// spent (RFC 0005 §10: the linguistic layer is bounded by the
	// existing per-tenant AI budget).
	LinguisticFailureBudgetExceeded = "budget_exceeded"
	// LinguisticFailureNoRoute: no provider is configured for the task.
	LinguisticFailureNoRoute = "no_route"
	// LinguisticFailureInvalidOutput: the model's answer did not parse
	// into (code, span, explanation, optional suggestion).
	LinguisticFailureInvalidOutput = "invalid_output"
	// LinguisticFailureProviderError: the provider refused or failed.
	LinguisticFailureProviderError = "provider_error"
	// LinguisticFailureLayerOff: the project's policy switched the
	// linguistic layer off, so the project does not compute it and does
	// not pay for it (RFC 0005 §4.1).
	LinguisticFailureLayerOff = "layer_off"
	// LinguisticFailureInternal: anything else.
	LinguisticFailureInternal = "internal"
)

// Linguistic-QA limits (RFC 0005 §10).
const (
	// MaxLinguisticLocales bounds one job's locales, as a fill's are
	// bounded.
	MaxLinguisticLocales = 20
	// MaxLinguisticKeys bounds the keys a job may name.
	MaxLinguisticKeys = 500
	// MaxLinguisticReason bounds the sentence a failure carries.
	MaxLinguisticReason = 4000
)

// Linguistic-QA errors.
var (
	// ErrInvalidLinguisticState is a state outside LinguisticJobStates.
	ErrInvalidLinguisticState = errors.New("quality: invalid linguistic-QA job state")
	// ErrInvalidLinguisticScope is a job that selects nothing, or more
	// than the limits allow.
	ErrInvalidLinguisticScope = errors.New("quality: invalid linguistic-QA scope")
	// ErrLinguisticJobNotCancellable is a job that has already finished.
	// Cancelling one is not a way to undo its findings.
	ErrLinguisticJobNotCancellable = errors.New(
		"quality: only a linguistic-QA job that has not finished can be cancelled")
	// ErrUnknownLinguisticCode is a finding whose code is not one of
	// LinguisticCodes: malformed model output.
	ErrUnknownLinguisticCode = errors.New("quality: not a linguistic-QA code")
	// ErrInvalidLinguisticFinding is a finding the layer could not have
	// produced: no locale, no key, no explanation.
	ErrInvalidLinguisticFinding = errors.New("quality: invalid linguistic finding")
)

// LinguisticScope is what one job reviews: the locales, and the slice
// of the catalog it covers. A job always names at least one locale —
// a review is of a translation, and there is no translation without a
// locale.
type LinguisticScope struct {
	// Locales are the target locales to review, canonical BCP 47.
	Locales []string
	// Namespace narrows to one namespace; empty is every namespace.
	Namespace string
	// KeyPrefix narrows to the keys under a prefix; empty is every key.
	KeyPrefix string
	// Keys names individual messages; empty is the whole selection.
	Keys []string
}

// Validate checks the scope before a job is created.
func (s LinguisticScope) Validate() error {
	switch {
	case len(s.Locales) == 0:
		return fmt.Errorf("%w: a linguistic review is of at least one locale", ErrInvalidLinguisticScope)
	case len(s.Locales) > MaxLinguisticLocales:
		return fmt.Errorf("%w: %d locales, at most %d", ErrInvalidLinguisticScope, len(s.Locales), MaxLinguisticLocales)
	case len(s.Keys) > MaxLinguisticKeys:
		return fmt.Errorf("%w: %d keys, at most %d", ErrInvalidLinguisticScope, len(s.Keys), MaxLinguisticKeys)
	}
	for _, l := range s.Locales {
		if strings.TrimSpace(l) == "" {
			return fmt.Errorf("%w: an empty locale", ErrInvalidLinguisticScope)
		}
	}
	for _, k := range s.Keys {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("%w: an empty key", ErrInvalidLinguisticScope)
		}
	}
	return nil
}

// LinguisticJob is one explicit or batch linguistic review: what it
// covers, where it stands, and what became of it.
//
// It is Quality's row and not Intelligence's, because what a job
// produces is findings — Quality's data, in Quality's tables, under
// Quality's forced RLS, and never at the edge (RFC 0005 §10). The model
// call it causes is Intelligence's, reached through a port; Batch is
// the handle that side gave back.
type LinguisticJob struct {
	ID      uuid.UUID
	Project uuid.UUID
	// Ref is the branch or environment the review is of. A finding is
	// recorded in a check run, and a check run is always of a ref.
	Ref   string
	Scope LinguisticScope
	State LinguisticJobState
	// Batch is Intelligence's handle for the work this job asked for,
	// so a read can follow it and a cancel can stop it. Empty until the
	// review has been handed over.
	Batch string
	// CheckRun is the run the job's findings were recorded in; nil while
	// the job has produced none. It is what makes recording idempotent:
	// a job that already has a run never records a second one.
	CheckRun *uuid.UUID
	// Findings counts what was stored, and SkippedSensitive how many
	// messages were left out because their namespace is tagged
	// `sensitive` — named rather than silently dropped, because a
	// reviewer has to know the layer did not look there.
	Findings         int
	SkippedSensitive int
	// Reviewed counts the translations the model actually saw.
	Reviewed    int
	FailureCode string
	LastError   string
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
}

// Validate checks a job before it is stored.
func (j LinguisticJob) Validate() error {
	if j.Ref == "" || len(j.Ref) > MaxRefLength {
		return fmt.Errorf("%w: %q", ErrInvalidRef, j.Ref)
	}
	if !slices.Contains(LinguisticJobStates, j.State) {
		return fmt.Errorf("%w: %q", ErrInvalidLinguisticState, j.State)
	}
	return j.Scope.Validate()
}

// Cancellable reports whether the job can still be stopped.
func (j LinguisticJob) Cancellable() bool { return !j.State.Final() }

// Fail returns the job failed, with the code and the sentence that says
// what to change.
func (j LinguisticJob) Fail(code, reason string, at time.Time) LinguisticJob {
	j.State, j.FailureCode = LinguisticFailed, code
	j.LastError = truncateReason(reason)
	j.UpdatedAt, j.FinishedAt = at, &at
	return j
}

func truncateReason(s string) string {
	if len(s) <= MaxLinguisticReason {
		return s
	}
	return strings.ToValidUTF8(s[:MaxLinguisticReason], "")
}

// LinguisticFinding is one thing a model suspects, as the Intelligence
// port hands it back.
//
// It deliberately carries **no severity**. A model says what looks
// wrong and where; what that is worth is the policy's answer, and the
// policy's answer for this layer can never be `error`. A producer that
// could assert a severity could fail somebody's build with an opinion,
// which is the exact outcome RFC 0005 §14 decision 10 forbids — so the
// type makes it unsayable rather than checking for it afterwards.
type LinguisticFinding struct {
	// Code is one of LinguisticCodes.
	Code string
	// Message is the catalog message's ID, where the reviewer had one.
	Message string
	// Key is the message key, and Locale the locale reviewed.
	Key    string
	Locale string
	// Namespace is the message's namespace; a sensitive one never
	// reaches a provider and so never reaches here.
	Namespace string
	// Revision is the translation revision reviewed.
	Revision string
	// Explanation is the sentence for a person.
	Explanation string
	// Subject is what the finding names — the phrase, the term, the
	// register. It is part of the fingerprint, because two suspicions
	// about one message are two findings.
	Subject string
	// Span delimits the offending words, in bytes.
	Span *Span
	// Suggestion is the better wording the model proposed, where it
	// proposed one. It becomes a `replace` fix, which is a hint and
	// never an action — and `glossa check --fix` never applies a fix a
	// model produced (RFC 0005 §9).
	Suggestion string
	// Evidence is what the reviewer recorded: the prompt version, the
	// model, its own confidence.
	Evidence map[string]any
	// SourceRevision is the source revision the review was made
	// against; a waiver dies when it changes.
	SourceRevision *int
}

// SealLinguistic turns one reviewed suspicion into a finding of the
// linguistic layer, at the one severity it may have.
//
// The severity is set here and taken from nowhere: `warning`, always.
// It is then graded like every other finding — Evaluate asks the policy
// through checkpolicy.Policy.Decide, which clamps an advisory layer
// back to Warning however strict the document is, and refuses outright
// a rule that names the layer at `error`. Two mechanisms, both already
// there; this constructor only makes sure a finding can never enter at
// a severity those mechanisms would have to undo.
func SealLinguistic(in LinguisticFinding) (Finding, error) {
	switch {
	case !LinguisticCode(in.Code):
		return Finding{}, fmt.Errorf("%w: %q (one of %s)",
			ErrUnknownLinguisticCode, in.Code, strings.Join(LinguisticCodes, ", "))
	case strings.TrimSpace(in.Locale) == "":
		return Finding{}, fmt.Errorf("%w: %s names no locale, and a review is of a translation",
			ErrInvalidLinguisticFinding, in.Code)
	case strings.TrimSpace(in.Key) == "":
		return Finding{}, fmt.Errorf("%w: %s names no message", ErrInvalidLinguisticFinding, in.Code)
	case strings.TrimSpace(in.Explanation) == "":
		return Finding{}, fmt.Errorf("%w: %s explains nothing, and an opinion nobody can read is not a finding",
			ErrInvalidLinguisticFinding, in.Code)
	}
	f := Finding{
		Layer: LayerLinguistic,
		Code:  in.Code,
		// The one severity the layer may emit. checkpolicy.Advisory
		// (RFC 0005 §14 decision 10) is why: a build never fails on an
		// opinion.
		Severity: Warning,
		Locus: Locus{
			Message: in.Message, Key: in.Key, Locale: in.Locale,
			Namespace: in.Namespace, Revision: in.Revision, Span: in.Span,
		},
		Message:        in.Explanation,
		Subject:        in.Subject,
		Evidence:       in.Evidence,
		SourceRevision: in.SourceRevision,
	}
	if in.Suggestion != "" {
		f.Fix = &Fix{Kind: FixReplace, Hint: in.Suggestion}
	}
	return New(f), nil
}

// LinguisticAdvisory is the kernel's answer for this layer, restated
// where the job can assert it: a policy may not raise a linguistic
// finding to error.
//
// It exists so that a test of this package fails when the kernel's
// AdvisoryLayers stops containing `linguistic` — the guarantee this
// whole file rests on would otherwise disappear silently.
func LinguisticAdvisory() bool { return checkpolicy.Advisory(string(LayerLinguistic)) }
