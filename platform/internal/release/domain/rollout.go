package domain

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Staged rollout (RFC 0006 §5.2, runtimes/SPEC.md §1.4): an environment
// serves a candidate release to a stable share of installations while
// every other installation keeps the release the environment points at.
// The runtime decides an installation's side from the signed manifest;
// Release only says which candidate, what share and under which salt.
//
// The owner's decision (RFC 0006 §15 Q5): cohorts per installation, any
// percentage at any step — up or down — and halting by a person only.
// Nothing here halts a rollout on its own except max_duration, which
// aborts a forgotten one so it doesn't become a permanent second
// production.

// Rollout events. Each names its actor in the envelope, and its payload
// says who in `by`.
const (
	AggregateRollout = "rollout"

	EventRolloutStarted   = "release.rollout.started"
	EventRolloutAdvanced  = "release.rollout.advanced"
	EventRolloutCompleted = "release.rollout.completed"
	EventRolloutAborted   = "release.rollout.aborted"
)

// RolloutStatus is where a rollout is in its life. Only an active
// rollout appears in the manifest.
type RolloutStatus string

// Rollout statuses.
const (
	RolloutActive    RolloutStatus = "active"
	RolloutCompleted RolloutStatus = "completed"
	RolloutAborted   RolloutStatus = "aborted"
)

// RolloutEnd is why a rollout ended.
type RolloutEnd string

// How rollouts end.
const (
	// EndCompleted: a person completed it; the candidate is the
	// environment's release.
	EndCompleted RolloutEnd = "completed"
	// EndAborted: a person aborted it.
	EndAborted RolloutEnd = "aborted"
	// EndExpired: the sweep aborted it at its max_duration.
	EndExpired RolloutEnd = "expired"
	// EndRolledBack: a rollback of the environment aborted it, so every
	// installation lands on the rollback target (RFC 0006 §5.1: rollback
	// is never refused or delayed).
	EndRolledBack RolloutEnd = "rolled_back"
)

// Bounds of a rollout's max_duration. The default is RFC 0006 §5.2's.
const (
	DefaultRolloutMaxDuration = 14 * 24 * time.Hour
	MinRolloutMaxDuration     = time.Hour
	MaxRolloutMaxDuration     = 90 * 24 * time.Hour
)

// SaltLen is the length of a rollout salt: 16 random bytes, base64url
// without padding (SPEC §1.4).
const SaltLen = 22

var saltPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// Rollout errors.
var (
	ErrInvalidPercent     = errors.New("release: a rollout's percent is an integer from 0 to 100")
	ErrInvalidMaxDuration = errors.New("release: a rollout's max_duration is from 1 hour to 90 days")
	ErrInvalidSalt        = errors.New("release: a rollout salt is 22 base64url characters")
	// ErrRolloutActive: the environment has an active rollout, which
	// must end before the environment can start another or move its
	// pointer by publish or promote.
	ErrRolloutActive = errors.New("release: the environment has an active rollout; complete or abort it first")
	// ErrRolloutEnded: the rollout was completed or aborted already.
	ErrRolloutEnded = errors.New("release: the rollout has ended")
	// ErrRolloutNoStable: a rollout serves its candidate beside the
	// environment's release, so the environment must serve one.
	ErrRolloutNoStable = errors.New("release: the environment serves no release yet; publish one before rolling out another")
	// ErrRolloutCandidateServed: the candidate is what the environment
	// serves already.
	ErrRolloutCandidateServed = errors.New("release: the environment already serves that release")
	// ErrRolloutBranchEnvironment: rollouts in branch environments are
	// deferred (RFC 0006 §5.3).
	ErrRolloutBranchEnvironment = errors.New("release: branch environments have no staged rollouts")
	// ErrRolloutSourceLocale: the candidate and the stable release share
	// one manifest's sourceLocale (SPEC §1.4), so a rollout can't change
	// it.
	ErrRolloutSourceLocale = errors.New("release: the candidate's source locale differs from the environment's release; promote it instead of rolling it out")
	// ErrRolloutNeedsApproval: the environment requires release
	// approvals (RFC 0006 §5.1). Starting a rollout there needs them as
	// a publish would (§5.2), and no approval path carries a rollout
	// yet, so it is refused rather than started unapproved.
	ErrRolloutNeedsApproval = errors.New("release: the environment requires release approvals, and a rollout can't be approved yet; " +
		"publish or promote through a release request instead")
)

// NewSalt returns a fresh rollout salt from a cryptographically secure
// source. Each rollout gets its own, so one rollout's cohorts say
// nothing about the next one's.
func NewSalt() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("release: crypto/rand: %v", err)) // crypto/rand.Read never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// ValidSalt reports whether s has a salt's shape.
func ValidSalt(s string) bool { return saltPattern.MatchString(s) }

// ValidPercent reports whether p is a percent a manifest may carry.
func ValidPercent(p int) bool { return p >= 0 && p <= 100 }

// Rollout is a candidate release served to a share of an environment's
// installations.
type Rollout struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	Environment string
	// Candidate is the release rolled out; Stable is the release the
	// environment served when the rollout started (its pointer does not
	// move until the rollout completes).
	Candidate uuid.UUID
	Stable    uuid.UUID
	Percent   int
	// Salt is fixed for the rollout's life, so an installation in the
	// candidate at one percent is in it at every higher one.
	Salt   string
	Status RolloutStatus
	// MaxDuration bounds the rollout: at ExpiresAt the sweep aborts it.
	MaxDuration time.Duration
	ExpiresAt   time.Time
	// Override records a start that went past the environment's check
	// policy, and why; completing the rollout records it on the
	// deployment.
	Override  Override
	StartedBy string
	StartedAt time.Time
	UpdatedAt time.Time
	// Version increments with every change; it is the rollout's ETag.
	Version int
	// EndedBy, EndedAt and End say who ended the rollout, when and how;
	// zero while it is active.
	EndedBy string
	EndedAt time.Time
	End     RolloutEnd
}

// RolloutStart is what starting a rollout asks for.
type RolloutStart struct {
	Percent int
	// MaxDuration zero means DefaultRolloutMaxDuration.
	MaxDuration time.Duration
	// Override is the forced start's reason, if the gate refused it.
	Override Override
	// Salt is NewSalt() in production; tests pass a fixture's.
	Salt string
	// ApprovalRequired says the environment requires release approvals.
	ApprovalRequired bool
}

// StartRollout starts rolling candidate out in env, beside stable — the
// release env serves. The candidate must be one env could be promoted
// to: a main-catalog release its policy covers.
func StartRollout(id uuid.UUID, env Environment, stable, candidate Release, in RolloutStart, by string, now time.Time) (Rollout, error) {
	if err := rolloutTarget(env, stable, candidate, in.ApprovalRequired); err != nil {
		return Rollout{}, err
	}
	if !ValidPercent(in.Percent) {
		return Rollout{}, fmt.Errorf("%w: %d", ErrInvalidPercent, in.Percent)
	}
	maxDuration := in.MaxDuration
	if maxDuration == 0 {
		maxDuration = DefaultRolloutMaxDuration
	}
	if maxDuration < MinRolloutMaxDuration || maxDuration > MaxRolloutMaxDuration {
		return Rollout{}, fmt.Errorf("%w: %s", ErrInvalidMaxDuration, maxDuration)
	}
	if !ValidSalt(in.Salt) {
		return Rollout{}, ErrInvalidSalt
	}
	now = now.UTC().Truncate(time.Microsecond)
	return Rollout{
		ID: id, ProjectID: env.ProjectID, Environment: env.Name, Candidate: candidate.ID, Stable: stable.ID,
		Percent: in.Percent, Salt: in.Salt, Status: RolloutActive, MaxDuration: maxDuration,
		ExpiresAt: now.Add(maxDuration), Override: in.Override,
		StartedBy: by, StartedAt: now, UpdatedAt: now, Version: 1,
	}, nil
}

// rolloutTarget checks that candidate may be rolled out in env beside
// stable, in the order a person would fix the problems in.
func rolloutTarget(env Environment, stable, candidate Release, approvalRequired bool) error {
	switch {
	case env.Kind == KindBranch:
		return ErrRolloutBranchEnvironment
	case approvalRequired:
		return ErrRolloutNeedsApproval
	case env.Current == uuid.Nil || stable.ID != env.Current:
		return ErrRolloutNoStable
	case candidate.ID == env.Current:
		return ErrRolloutCandidateServed
	case candidate.ProjectID != env.ProjectID:
		return fmt.Errorf("release: release %s is not in project %s", candidate.ID, env.ProjectID)
	}
	if err := candidate.Promotable(); err != nil {
		return err
	}
	if !env.Policy.Covers(candidate.Policy) {
		return Ineligible(env.Name, env.Policy, candidate.Version, candidate.Environment, candidate.Policy)
	}
	if candidate.Content.SourceLocale != stable.Content.SourceLocale {
		return ErrRolloutSourceLocale
	}
	return nil
}

// Active reports whether the rollout is still serving its candidate.
func (r Rollout) Active() bool { return r.Status == RolloutActive }

// Expired reports whether an active rollout is past its max_duration.
func (r Rollout) Expired(now time.Time) bool { return r.Active() && !now.Before(r.ExpiresAt) }

// Advance changes the share of installations in the candidate; false if
// it is unchanged. Steps are free (SPEC §1.4): any percent may follow
// any other, down as well as up — lowering it is how a person narrows a
// rollout without aborting it. The salt stays, so the installations in
// the candidate at the lower percent are a subset of those at the
// higher.
func (r *Rollout) Advance(percent int, now time.Time) (bool, error) {
	if !r.Active() {
		return false, ErrRolloutEnded
	}
	if !ValidPercent(percent) {
		return false, fmt.Errorf("%w: %d", ErrInvalidPercent, percent)
	}
	if percent == r.Percent {
		return false, nil
	}
	r.Percent = percent
	r.touch(now)
	return true, nil
}

// Complete ends the rollout with the candidate as the environment's
// release; the caller moves the pointer in the same transaction.
func (r *Rollout) Complete(by string, now time.Time) error {
	return r.end(RolloutCompleted, EndCompleted, by, now)
}

// Abort ends the rollout with the environment's release unchanged; why
// is EndAborted, EndExpired or EndRolledBack.
func (r *Rollout) Abort(why RolloutEnd, by string, now time.Time) error {
	switch why {
	case EndAborted, EndExpired, EndRolledBack:
	default:
		return fmt.Errorf("release: %q is not a way to abort a rollout", why)
	}
	return r.end(RolloutAborted, why, by, now)
}

func (r *Rollout) end(status RolloutStatus, why RolloutEnd, by string, now time.Time) error {
	if !r.Active() {
		return ErrRolloutEnded
	}
	r.Status, r.End, r.EndedBy = status, why, by
	r.touch(now)
	r.EndedAt = r.UpdatedAt
	return nil
}

func (r *Rollout) touch(now time.Time) {
	r.Version++
	r.UpdatedAt = now.UTC().Truncate(time.Microsecond)
}

// ── the manifest member (SPEC §1.4) ─────────────────────────────────

// ManifestRollout is the manifest's `rollout` member. The candidate is
// nested, and the top level stays the stable release, so a runtime
// that predates SPEC §1.4 ignores it and serves stable.
type ManifestRollout struct {
	ID        string            `json:"id"`
	Percent   int               `json:"percent"`
	Salt      string            `json:"salt"`
	Candidate ManifestCandidate `json:"candidate"`
}

// ManifestCandidate is the candidate release's view: exactly the
// top-level members SPEC §1.4 lets it replace. sourceLocale is shared
// with the stable release.
type ManifestCandidate struct {
	Release   ReleaseRef                        `json:"release"`
	Locales   []Locale                          `json:"locales"`
	Fallback  map[string][]string               `json:"fallback"`
	Artifacts map[string]map[string]ArtifactRef `json:"artifacts"`
}

// WithRollout adds r's member to m, describing candidate. An ended
// rollout adds nothing: the manifest is the stable one.
func (m Manifest) WithRollout(r Rollout, candidate Release) Manifest {
	if !r.Active() {
		return m
	}
	m.Rollout = &ManifestRollout{
		ID: r.ID.String(), Percent: r.Percent, Salt: r.Salt,
		Candidate: ManifestCandidate{
			Release:  releaseRef(candidate),
			Locales:  candidate.Content.Locales,
			Fallback: candidate.Content.Fallback, Artifacts: candidate.Content.Artifacts,
		},
	}
	return m
}

// RolloutChanged is the payload of the rollout events: what the
// rollout is after the change, and who changed it. ReleaseID is the
// candidate; StableReleaseID what the environment served when it
// started. A completed rollout's pointer move is also announced as
// release.promoted.
type RolloutChanged struct {
	RolloutID       string `json:"rollout_id"`
	ProjectID       string `json:"project_id"`
	Environment     string `json:"environment"`
	ReleaseID       string `json:"release_id"`
	StableReleaseID string `json:"stable_release_id"`
	Percent         int    `json:"percent"`
	// PreviousPercent is the percent before an advance.
	PreviousPercent *int   `json:"previous_percent,omitempty"`
	Status          string `json:"status"`
	// End is how an ended rollout ended.
	End string `json:"end,omitempty"`
	// MaxDurationSeconds and ExpiresAt bound the rollout.
	MaxDurationSeconds int64     `json:"max_duration_seconds"`
	ExpiresAt          time.Time `json:"expires_at"`
	Forced             bool      `json:"forced,omitempty"`
	By                 string    `json:"by"`
}

// Changed is r's event payload.
func (r Rollout) Changed(by string) RolloutChanged {
	return RolloutChanged{
		RolloutID: r.ID.String(), ProjectID: r.ProjectID.String(), Environment: r.Environment,
		ReleaseID: r.Candidate.String(), StableReleaseID: r.Stable.String(), Percent: r.Percent,
		Status: string(r.Status), End: string(r.End), MaxDurationSeconds: int64(r.MaxDuration / time.Second),
		ExpiresAt: r.ExpiresAt, Forced: r.Override.Forced, By: by,
	}
}
