package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
)

// The Glossa PR check (RFC 0004 §6.4).
//
// One Check is one pull request: the queue row a worker claims, the
// sticky comment's identity, and one CheckTarget per Git connection —
// because a repository can feed several projects, one per monorepo
// path, and each gets its own check run.

// CheckName is the check run's name on GitHub. A connection with a
// monorepo path gets its own, so several projects in one repository
// have one check each rather than fighting over one.
const CheckName = "Glossa"

// CheckWait is how long a head SHA waits for its Glossa CI run before
// the check completes `neutral` (RFC 0004 §6.4: "if nothing arrives
// within 30 min").
const CheckWait = 30 * time.Minute

// MaxCheckAttempts is how often a check job is retried before it is
// left alone until the next event wakes it.
const MaxCheckAttempts = 8

// CheckState is where a pull request's check is.
type CheckState string

// Check states. A completed check is not final: a fixed message or a
// landed translation wakes it, and it reports again.
const (
	CheckQueued    CheckState = "queued"
	CheckCompleted CheckState = "completed"
)

// CheckRunName is the check run's name for a connection at path ("" for
// the whole repository).
func CheckRunName(path string) string {
	if path == "" {
		return CheckName
	}
	return CheckName + " (" + path + ")"
}

// CheckTarget is one Git connection's half of a pull request's check:
// the check run GitHub made for the current head SHA, what that
// project's CI has uploaded for it, and which annotations have already
// been appended to that run.
type CheckTarget struct {
	// CheckRunID is GitHub's id; 0 until the run is created.
	CheckRunID int64 `json:"check_run_id,omitempty"`
	// Annotations fingerprints the annotations already sent to
	// CheckRunID. GitHub *appends* annotations rather than replacing
	// them, so an annotation is sent once per run and a retry sends
	// none. A new run (a new commit, or a rerequest) starts empty.
	Annotations []string `json:"annotations,omitempty"`
	// Pushed, Usages and Recorded are what this head SHA's CI has done:
	// the branch push, the usages build, and the `glossa check` run it
	// recorded. The check completes when all three have arrived, or when
	// CheckWait runs out.
	//
	// Recorded is the one RFC 0005 §12.3 adds. The pull request renders
	// the run CI recorded rather than computing a second one, so in a
	// project known to record runs there is nothing to render until it
	// exists. A project that has never recorded one is not waited for,
	// and completes on the first two (RFC 0005 §14 decision 11; the rule
	// lives with the worker).
	Pushed   bool `json:"pushed,omitempty"`
	Usages   bool `json:"usages,omitempty"`
	Recorded bool `json:"recorded,omitempty"`
	// Conclusion is what the run last reported.
	Conclusion string `json:"conclusion,omitempty"`
}

// Sent reports whether fingerprint has already been appended to the
// current check run.
func (t CheckTarget) Sent(fingerprint string) bool {
	for _, f := range t.Annotations {
		if f == fingerprint {
			return true
		}
	}
	return false
}

// MaxSentAnnotations bounds the ledger. GitHub keeps at most a few
// thousand annotations on a run, and the report itself is capped well
// below that; the bound only stops a pathological branch from growing
// the row without limit.
const MaxSentAnnotations = 500

// Record adds fingerprints to the ledger of what this run has been
// sent.
func (t *CheckTarget) Record(fingerprints []string) {
	for _, f := range fingerprints {
		if len(t.Annotations) >= MaxSentAnnotations {
			return
		}
		if !t.Sent(f) {
			t.Annotations = append(t.Annotations, f)
		}
	}
}

// AnnotationFingerprint identifies one annotation by what makes it the
// same annotation: where it points and what it says.
func AnnotationFingerprint(path string, line int, message string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(path))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(itoa(line)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil)[:8])
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// Check is a pull request's Glossa check: the queue row, the sticky
// comment and the check runs on the current head SHA.
type Check struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	// InstallationID is GitHub's, for the token, the circuit breaker and
	// the target.
	InstallationID int64
	RepositoryID   int64
	PullRequest    int
	Branch         string
	HeadSHA        string
	// FromFork says the head lives in another repository (RFC 0004
	// §6.3). Such a pull request gets no Glossa CI token and no secrets,
	// so nothing can ever be uploaded for this commit: the check is
	// concluded `neutral` at once rather than waiting out CheckWait for
	// an upload that cannot arrive.
	FromFork bool
	// CommentID is GitHub's id for the one sticky comment; 0 until it is
	// written. It survives a new commit — the comment is the pull
	// request's, not the commit's.
	CommentID int64
	// OpenedAt is GitHub's `pull_request.created_at`: when the pull
	// request was opened, not when Glossa first heard of it. It is what
	// a policy's grace is measured against, so that tightening a rule
	// cannot turn a pull request red for something its author did not do
	// (RFC 0005 §4.3).
	//
	// Like CommentID it survives a new commit: a push must not make the
	// pull request younger, or its grace would run a little further out
	// with every one. The zero time is "nobody recorded when" — a row
	// written before migration 0033, or a check that is not a pull
	// request's — and then the project's current policy grades it.
	OpenedAt time.Time
	// Targets is keyed by Git connection.
	Targets map[uuid.UUID]CheckTarget
	State   CheckState
	// Conclusion is the check's overall verdict, the worst of its runs'.
	Conclusion  string
	Attempts    int
	Failure     string
	ClaimToken  uuid.UUID
	RequestedAt time.Time
	AvailableAt time.Time
	CompletedAt *time.Time
	UpdatedAt   time.Time
}

// Target returns the connection's target, or a zero one.
func (c Check) Target(connection uuid.UUID) CheckTarget {
	if c.Targets == nil {
		return CheckTarget{}
	}
	return c.Targets[connection]
}

// SetTarget stores the connection's target.
func (c *Check) SetTarget(connection uuid.UUID, t CheckTarget) {
	if c.Targets == nil {
		c.Targets = map[uuid.UUID]CheckTarget{}
	}
	c.Targets[connection] = t
}

// Expired reports whether the head SHA has waited longer than CheckWait
// for its Glossa CI run.
func (c Check) Expired(now time.Time) bool {
	return !now.Before(c.RequestedAt.Add(CheckWait))
}

// Deadline is when the wait for CI runs out.
func (c Check) Deadline() time.Time { return c.RequestedAt.Add(CheckWait) }

// Conclusions, worst first: a check's verdict is the worst its runs
// reached, so one failing project fails the pull request.
var conclusionRank = map[string]int{"failure": 0, "neutral": 1, "success": 2}

// WorseConclusion returns the worse of two conclusions ("" is unknown
// and loses to anything).
func WorseConclusion(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	if conclusionRank[a] <= conclusionRank[b] {
		return a
	}
	return b
}
