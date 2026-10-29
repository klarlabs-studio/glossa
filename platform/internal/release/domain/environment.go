// Package domain is the Release context's model: what ships where
// (RFC 0002 §4, §7; intent §34–38). An Environment points at a Release;
// a Release is an immutable snapshot of a project's releasable text,
// written as content-addressed artifacts and described by a signed
// manifest (runtimes/SPEC.md §1). Publishing builds a release; promote
// and rollback only move environment pointers.
package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/release/delivery"
)

// Errors.
var (
	ErrInvalidEnvironment = errors.New("release: environment names are 1-63 characters of [a-z0-9-], starting with a letter or digit (\"a\" is reserved)")
	ErrInvalidPolicy      = errors.New("release: a policy ships one or more of draft, needs_review and approved (never rejected)")
	ErrInvalidNote        = errors.New("release: a note is at most 1000 characters")
	ErrInvalidKeyName     = errors.New("release: a delivery key name is 1-200 characters")
	ErrNotReleasable      = errors.New("release: the catalog can't be released")
	ErrIneligible         = errors.New("release: the release ships review states this environment's policy excludes")
	ErrNoRollbackTarget   = errors.New("release: the environment has no earlier release to roll back to")
	ErrNotInHistory       = errors.New("release: the environment never served that release")
	ErrKeyRevoked         = errors.New("release: the delivery key is already revoked")

	ErrInvalidBranch = errors.New("release: a branch environment needs a branch name of 1-255 bytes " +
		"without control characters, and a pull request number that isn't negative")
	ErrFixedPolicy = errors.New("release: a branch environment's policy is fixed " +
		"(draft, needs_review and approved, outdated included)")
	ErrBranchReleaseNotPromotable = errors.New("release: a branch release can't be promoted: " +
		"it holds text that exists only on its branch. Publish to the environment instead")
	ErrTooManyBranches = errors.New("release: a project has at most 50 open branch environments")
)

// The default environments every project has (intent §37).
const (
	Development = "development"
	Preview     = "preview"
	Staging     = "staging"
	Production  = "production"
)

// DefaultEnvironments are created with a project's first use of Release.
var DefaultEnvironments = []string{Development, Preview, Staging, Production}

// ParseEnvironmentName validates name.
func ParseEnvironmentName(name string) (string, error) {
	if !delivery.ValidEnvironment(name) {
		return "", fmt.Errorf("%w: %q", ErrInvalidEnvironment, name)
	}
	return name, nil
}

// Review states a policy may ship, in workflow order. Rejected text
// never ships.
const (
	StateDraft       = "draft"
	StateNeedsReview = "needs_review"
	StateApproved    = "approved"
)

var shippable = []string{StateDraft, StateNeedsReview, StateApproved}

// Policy is an environment's eligibility rule: which review states of a
// translation ship there, and whether a translation made against an
// older source revision (outdated) still ships. A locale without an
// eligible translation falls back at runtime (SPEC §4); it is never
// padded with source text.
type Policy struct {
	States          []string `json:"states"`
	IncludeOutdated bool     `json:"include_outdated"`
}

// NewPolicy validates states and returns them in workflow order.
func NewPolicy(states []string, includeOutdated bool) (Policy, error) {
	if len(states) == 0 {
		return Policy{}, ErrInvalidPolicy
	}
	var out []string
	for _, s := range shippable {
		if slices.Contains(states, s) {
			out = append(out, s)
		}
	}
	for _, s := range states {
		if !slices.Contains(shippable, s) {
			return Policy{}, fmt.Errorf("%w: %q", ErrInvalidPolicy, s)
		}
	}
	return Policy{States: out, IncludeOutdated: includeOutdated}, nil
}

// DefaultPolicy is the policy a default environment starts with:
// production and staging ship approved text only; development, preview
// and custom environments everything not rejected. Outdated text ships
// everywhere until an editor says otherwise: an approved translation of
// an older source is closer to right than a fallback language.
func DefaultPolicy(environment string) Policy {
	if environment == Production || environment == Staging {
		return Policy{States: []string{StateApproved}, IncludeOutdated: true}
	}
	return Policy{States: slices.Clone(shippable), IncludeOutdated: true}
}

// Covers reports whether everything a release built under other may ship
// is allowed under p: promoting a preview release with drafts into
// production must not smuggle drafts in.
func (p Policy) Covers(other Policy) bool {
	for _, s := range other.States {
		if !slices.Contains(p.States, s) {
			return false
		}
	}
	return p.IncludeOutdated || !other.IncludeOutdated
}

// IneligibleError is a promotion the target's policy refuses: the
// release was built under a policy that ships text the target excludes.
// It is ErrIneligible, and its text says what differs and how to get
// the text there.
type IneligibleError struct {
	Environment string
	Policy      Policy
	// Version, From and FromPolicy describe the release: its version,
	// the environment it was published to and the policy it was built
	// under.
	Version    int
	From       string
	FromPolicy Policy
}

// Ineligible explains why a release of version, published to from under
// fromPolicy, can't be promoted to environment with policy p.
func Ineligible(environment string, p Policy, version int, from string, fromPolicy Policy) error {
	return &IneligibleError{Environment: environment, Policy: p, Version: version, From: from, FromPolicy: fromPolicy}
}

func (e *IneligibleError) Error() string {
	var excess []string
	for _, s := range e.FromPolicy.States {
		if !slices.Contains(e.Policy.States, s) {
			excess = append(excess, s)
		}
	}
	var what []string
	if len(excess) > 0 {
		what = append(what, strings.Join(excess, ", ")+" text")
	}
	if e.FromPolicy.IncludeOutdated && !e.Policy.IncludeOutdated {
		what = append(what, "outdated translations")
	}
	ships := strings.Join(e.Policy.States, ", ")
	if !e.Policy.IncludeOutdated {
		ships += ", outdated excluded"
	}
	msg := fmt.Sprintf("release: v%d can't be promoted to %s: it was published to %s, whose policy ships %s, and %s ships %s only. "+
		"Publish to %s directly, or promote a release published under a policy %s covers",
		e.Version, e.Environment, e.From, strings.Join(what, " and "), e.Environment, ships, e.Environment, e.Environment)
	if e.Environment == Production {
		msg += " (by default: publish to staging, then promote that release to production)"
	}
	return msg
}

// Unwrap makes the error ErrIneligible.
func (e *IneligibleError) Unwrap() error { return ErrIneligible }

// Equal reports whether p and o are the same policy.
func (p Policy) Equal(o Policy) bool {
	return slices.Equal(p.States, o.States) && p.IncludeOutdated == o.IncludeOutdated
}

// EnvironmentKind says what an environment serves.
type EnvironmentKind string

// Environment kinds.
const (
	// KindStandard serves the main catalog: the default environments and
	// custom ones (a QA stage).
	KindStandard EnvironmentKind = "standard"
	// KindBranch serves one open branch's preview: the main catalog plus
	// that branch's overlay (RFC 0004 §4.2).
	KindBranch EnvironmentKind = "branch"
)

// MaxBranchEnvironments bounds a project's open branch environments.
const MaxBranchEnvironments = 50

// maxBranchLen bounds a branch name (Catalog's limit).
const maxBranchLen = 255

// BranchPolicy is every branch environment's fixed policy: everything
// not rejected, outdated included — a preview shows the work in
// progress.
func BranchPolicy() Policy {
	return Policy{States: slices.Clone(shippable), IncludeOutdated: true}
}

// Environment is where a project's text is served (development,
// production, a branch preview): a policy for what may ship there and a
// pointer to the release it serves.
type Environment struct {
	ProjectID uuid.UUID
	Name      string
	Kind      EnvironmentKind
	// Branch is the branch a branch environment previews; "" otherwise.
	Branch string
	Policy Policy
	// Approval is who must approve a publish or a promote into the
	// environment before its pointer moves (RFC 0006 §5.1); nil — the
	// default — means none, and the move happens when it is asked for.
	Approval *ApprovalPolicy
	// Current is the release served, uuid.Nil before the first publish.
	Current uuid.UUID
	// Version increments with every change (policy, approval or
	// pointer); it is
	// the environment's ETag.
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewEnvironment creates a standard environment with policy. Branch
// environment names (pr-<n>, br-<8 hex>) are reserved: delivery keys
// reach those environments through their branches flag, by name.
func NewEnvironment(project uuid.UUID, name string, policy Policy, now time.Time) (Environment, error) {
	if _, err := ParseEnvironmentName(name); err != nil {
		return Environment{}, err
	}
	if delivery.IsBranchEnvironment(name) {
		return Environment{}, fmt.Errorf("%w: %q is reserved for branch environments", ErrInvalidEnvironment, name)
	}
	if _, err := NewPolicy(policy.States, policy.IncludeOutdated); err != nil {
		return Environment{}, err
	}
	return Environment{
		ProjectID: project, Name: name, Kind: KindStandard, Policy: policy, Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// NewBranchEnvironment creates the environment that previews branch
// (RFC 0004 §4.2): pr-<pr> for a pull request (pr > 0), br-<8 hex of
// sha256(branch)> otherwise, with the fixed BranchPolicy.
func NewBranchEnvironment(project uuid.UUID, branch string, pr int, now time.Time) (Environment, error) {
	if branch == "" || len(branch) > maxBranchLen || pr < 0 || strings.ContainsFunc(branch, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return Environment{}, ErrInvalidBranch
	}
	return Environment{
		ProjectID: project, Name: delivery.BranchEnvironmentName(branch, pr), Kind: KindBranch, Branch: branch,
		Policy: BranchPolicy(), Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// ChangePolicy replaces the policy; false if it is unchanged. The
// release served keeps serving: a policy decides what the next publish
// takes. A branch environment's policy is fixed.
func (e *Environment) ChangePolicy(p Policy, now time.Time) (bool, error) {
	if e.Policy.Equal(p) {
		return false, nil
	}
	if e.Kind == KindBranch {
		return false, ErrFixedPolicy
	}
	e.Policy = p
	e.touch(now)
	return true, nil
}

// Point makes the environment serve release; false if it already does.
func (e *Environment) Point(release uuid.UUID, now time.Time) bool {
	if e.Current == release {
		return false
	}
	e.Current = release
	e.touch(now)
	return true
}

func (e *Environment) touch(now time.Time) {
	e.Version++
	e.UpdatedAt = now
}

// Action is how an environment came to serve a release.
type Action string

// Actions.
const (
	ActionPublish  Action = "publish"
	ActionPromote  Action = "promote"
	ActionRollback Action = "rollback"
)

// Deployment is one pointer move, kept forever: the environment's
// history, which rollback walks back through.
type Deployment struct {
	ProjectID   uuid.UUID
	Environment string
	// Number counts the environment's deployments from 1.
	Number    int
	ReleaseID uuid.UUID
	// Previous is what the environment served before (uuid.Nil if
	// nothing).
	Previous  uuid.UUID
	Action    Action
	By        string
	CreatedAt time.Time
	// Override records a publish that went out although the
	// environment's check policy refused it, and why (RFC 0005 §4.1).
	// Its zero value is every deployment that met the gate or had none.
	Override Override
}
