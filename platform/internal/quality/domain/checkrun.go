package domain

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"
)

// A check run is stored (RFC 0005 §2.2 rule 3): the PR check, Studio,
// the dashboard and `glossa findings` read one row rather than four
// recomputations of the same thing. Runs are kept 90 days and their
// findings live as long as the run.

// RunRetention is how long a check run is kept (RFC 0005 §2.2). After
// it, the daily sweep deletes the run and the findings that live with
// it — with one exception the sweep enforces and this constant cannot:
// the newest run of a ref is never deleted, whatever its age, because
// it is what every dashboard, `listFindings` and the summary read.
//
// Nothing is lost to the trend by this. The findings-by-day rollup
// (migration 0035) already holds one row per day, layer and project,
// counted over distinct fingerprints, and retention never touches it —
// which is exactly why retention can be this blunt rather than growing
// a second, longer-lived copy of the findings.
const RunRetention = 90 * 24 * time.Hour

// Trigger says what asked for a run.
type Trigger string

// Triggers.
const (
	// TriggerCLI is `glossa check`.
	TriggerCLI Trigger = "cli"
	// TriggerPullRequest is the Glossa PR check.
	TriggerPullRequest Trigger = "pull_request"
	// TriggerWrite is the server job that runs the catalog layers when a
	// translation is written.
	TriggerWrite Trigger = "write"
	// TriggerCapture is the job that runs on a capture upload.
	TriggerCapture Trigger = "capture"
	// TriggerAPI is an explicit run through the API or MCP.
	TriggerAPI Trigger = "api"
)

// Triggers lists every trigger.
var Triggers = []Trigger{TriggerCLI, TriggerPullRequest, TriggerWrite, TriggerCapture, TriggerAPI}

// Conclusion is a run's verdict, spelled as the PR check spells it.
type Conclusion string

// Conclusions.
const (
	ConclusionSuccess Conclusion = "success"
	ConclusionFailure Conclusion = "failure"
	// ConclusionNeutral is a run that could not grade itself: no policy,
	// or no catalog to check.
	ConclusionNeutral Conclusion = "neutral"
)

// Run errors.
var (
	// ErrUnknownTrigger is a trigger outside Triggers.
	ErrUnknownTrigger = errors.New("quality: unknown trigger")
	// ErrUnknownLayer is a layer outside Layers.
	ErrUnknownLayer = errors.New("quality: unknown layer")
	// ErrInvalidRef is a run that names no ref, or one past the column.
	ErrInvalidRef = errors.New("quality: a check run is of a branch or an environment")
	// ErrInvalidCommit is a commit that is not a full Git object name.
	ErrInvalidCommit = errors.New("quality: not a commit SHA")
)

// MaxRefLength bounds a run's ref, matching the column.
const MaxRefLength = 255

// commitSHA is a full lowercase Git object name — SHA-1 or SHA-256, as
// every other upload in the platform spells one (a usages document, a
// captures manifest). A run that is not about a commit carries none.
var commitSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// Counts are a run's findings by how they ended up. Waived is counted
// on its own and is never part of Errors or Warnings, so the number a
// dashboard shows is true (RFC 0005 §14 decision 5).
type Counts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Waived   int `json:"waived"`
}

// Total is every finding the run reported.
func (c Counts) Total() int { return c.Errors + c.Warnings + c.Waived }

// Count adds one finding to the counts.
func (c *Counts) Count(f Finding) {
	switch f.Severity {
	case Waived:
		c.Waived++
	case Error:
		c.Errors++
	default:
		c.Warnings++
	}
}

// CheckRun is one evaluation of a project against its policy.
type CheckRun struct {
	ID      uuid.UUID
	Project uuid.UUID
	// Ref is what was checked: a branch, or an environment name.
	Ref string
	// Commit is the commit the run graded, where there is one. A branch
	// moves; the commit a verdict was about does not, and RFC 0005 §12.3
	// asks the CLI and the pull-request check of one commit to agree —
	// which needs the commit to be part of what a run records. Empty for
	// a run that is not about a commit (an environment's, a write-time
	// job's).
	Commit  string
	Trigger Trigger
	// PolicyVersion is the policy the run graded itself against, so a
	// run can say which version it used when two are live at once
	// (RFC 0005 §4.3). It is 0 while the policy document is still the
	// two-field kernel policy.
	PolicyVersion int
	// Layers are the layers the run actually computed, in report order.
	// A layer the policy switched off is not in the list, so a dashboard
	// can tell "clean" from "not looked at".
	Layers     []Layer
	Counts     Counts
	Conclusion Conclusion
	// CreatedBy is the actor that asked for the run.
	CreatedBy string
	StartedAt time.Time
	// CompletedAt is zero while the run is in flight.
	CompletedAt time.Time
}

// Validate checks a run before it is stored.
func (r CheckRun) Validate() error {
	if r.Ref == "" || len(r.Ref) > MaxRefLength {
		return fmt.Errorf("%w: %q", ErrInvalidRef, r.Ref)
	}
	if r.Commit != "" && !commitSHA.MatchString(r.Commit) {
		return fmt.Errorf("%w: %q", ErrInvalidCommit, r.Commit)
	}
	if !slices.Contains(Triggers, r.Trigger) {
		return fmt.Errorf("%w: %q", ErrUnknownTrigger, r.Trigger)
	}
	for _, l := range r.Layers {
		if !l.Valid() {
			return fmt.Errorf("%w: %q", ErrUnknownLayer, l)
		}
	}
	return nil
}

// Conclude sets the run's verdict from its findings and the policy that
// graded them. The policy stays the evaluator: a run never invents a
// rule of its own.
func Conclude(p interface{ Fails(Severity) bool }, fs []Finding) (Counts, Conclusion) {
	var c Counts
	conclusion := ConclusionSuccess
	for _, f := range fs {
		c.Count(f)
		if f.Severity != Waived && p.Fails(f.Severity) {
			conclusion = ConclusionFailure
		}
	}
	return c, conclusion
}
