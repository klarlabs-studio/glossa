package domain

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// BranchName is a Git branch name, unique per project. It follows
// git check-ref-format closely enough that every name Git accepts for a
// pushed branch is accepted here, and none it refuses.
type BranchName string

// MaxBranchNameLen bounds a branch name.
const MaxBranchNameLen = 255

// ParseBranchName validates s.
func ParseBranchName(s string) (BranchName, error) {
	bad := s == "" || len(s) > MaxBranchNameLen || s == "@" ||
		strings.ContainsAny(s, " ~^:?*[\\\x7f") || strings.Contains(s, "..") || strings.Contains(s, "//") ||
		strings.Contains(s, "@{") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") ||
		strings.HasSuffix(s, ".") || strings.HasPrefix(s, "-")
	for component := range strings.SplitSeq(s, "/") {
		bad = bad || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock")
	}
	for _, r := range s {
		if r < 0x20 {
			bad = true
		}
	}
	if bad {
		return "", fmt.Errorf("%w: %q", ErrInvalidBranchName, s)
	}
	return BranchName(s), nil
}

// BranchState is where a branch is in its lifecycle.
type BranchState string

// Branch states.
const (
	BranchOpen   BranchState = "open"
	BranchMerged BranchState = "merged"
	BranchClosed BranchState = "closed"
)

// ProposalRetention is how long a closed, unmerged branch's proposed
// messages stay proposed before they become obsolete (RFC 0004 §4.1).
//
// Not to be confused with Context's retention of builds and captures,
// whose closed-branch grace is 7 days (RFC 0004 §2.3): this one is
// about the text a translator worked on, which outlives the screenshots
// of it, and stays at 14 days.
const ProposalRetention = 14 * 24 * time.Hour

// MaxPreviewURLLen bounds a preview URL.
const MaxPreviewURLLen = 2000

var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// MaxInvalidItems bounds what a branch remembers of a push's rejected
// items. A push that rejects more than this is broken in a way the
// first hundred already explain, and the list is a report, not a log.
const MaxInvalidItems = 100

// InvalidItem is one item a branch's last push could not accept, with
// the same code and detail the push itself reported.
type InvalidItem struct {
	Key    string `json:"key"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// Branch is a feature branch of a project's source (RFC 0004 §4.1): an
// overlay on the main catalog. It proposes new messages and changes to
// existing ones' source; nothing it proposes is released outside its
// own preview until the default branch's push brings it in.
type Branch struct {
	ID        BranchID
	ProjectID ProjectID
	Name      BranchName
	// PR is the pull request number, when the branch has one.
	PR *int
	// HeadCommit is the commit last pushed ("" if the push named none).
	HeadCommit string
	State      BranchState
	// PreviewURL is where CI deployed the branch's preview, if anywhere.
	PreviewURL string
	// ClosedAt is when the branch was closed or merged; nil while open.
	ClosedAt *time.Time
	// RemovedKeys are the keys of active messages the branch's last
	// complete push no longer had. A branch never obsoletes anything;
	// they are only reported.
	RemovedKeys []MessageKey
	// Invalid are the items the branch's last push could not accept:
	// source that is not a valid MessageFormat 2 message, a key or
	// namespace that does not parse, details that do not validate. The
	// push applied the rest, so these are what the Glossa PR check
	// reports as invalid messages (RFC 0004 §6.4). The next push
	// replaces the list.
	Invalid []InvalidItem
	// Version increments with every change; it is the branch's ETag and
	// orders its events.
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewBranch creates an open branch.
func NewBranch(project ProjectID, name BranchName, now time.Time) (Branch, error) {
	if _, err := ParseBranchName(string(name)); err != nil {
		return Branch{}, err
	}
	return Branch{
		ID: NewBranchID(), ProjectID: project, Name: name, State: BranchOpen,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// PushInfo is what a push says about the branch itself.
type PushInfo struct {
	// HeadCommit is the pushed commit's SHA ("" keeps the stored one).
	HeadCommit string
	// PR is the pull request number (nil keeps the stored one).
	PR *int
}

func (p PushInfo) validate() error {
	if p.HeadCommit != "" && !commitPattern.MatchString(p.HeadCommit) {
		return fmt.Errorf("%w: head commit %q is not a lowercase hex SHA", ErrInvalidPush, p.HeadCommit)
	}
	if p.PR != nil && *p.PR < 1 {
		return fmt.Errorf("%w: pull request number %d", ErrInvalidPush, *p.PR)
	}
	return nil
}

// Push records a push. A push on a closed branch reopens it (reported by
// reopened); a merged branch takes no more pushes.
func (b *Branch) Push(p PushInfo, now time.Time) (reopened bool, err error) {
	if err := p.validate(); err != nil {
		return false, err
	}
	was := b.State
	if err := b.transition(eventPush); err != nil {
		return false, err
	}
	if p.HeadCommit != "" {
		b.HeadCommit = p.HeadCommit
	}
	if p.PR != nil {
		pr := *p.PR
		b.PR = &pr
	}
	b.ClosedAt = nil
	b.touch(now)
	return was == BranchClosed, nil
}

// Close closes an unmerged branch; false if it already was closed.
func (b *Branch) Close(now time.Time) (bool, error) {
	return b.move(eventClose, BranchClosed, now)
}

// Reopen opens a closed branch again; false if it was open.
func (b *Branch) Reopen(now time.Time) (bool, error) {
	return b.move(eventReopen, BranchOpen, now)
}

// Merge marks the branch merged; false if it already was.
func (b *Branch) Merge(now time.Time) (bool, error) {
	return b.move(eventMerge, BranchMerged, now)
}

// move applies e unless the branch is already in target (idempotent
// webhooks), keeping ClosedAt in step with the state.
func (b *Branch) move(e branchEvent, target BranchState, now time.Time) (bool, error) {
	if b.State == target {
		return false, nil
	}
	if err := b.transition(e); err != nil {
		return false, err
	}
	if b.State == BranchOpen {
		b.ClosedAt = nil
	} else {
		at := now
		b.ClosedAt = &at
	}
	b.touch(now)
	return true, nil
}

func (b *Branch) transition(e branchEvent) error {
	next, ok := nextBranchState(b.State, e)
	if !ok {
		if b.State == BranchMerged {
			return ErrBranchMerged
		}
		return fmt.Errorf("%w: %s in state %s", ErrBranchTransition, e, b.State)
	}
	b.State = next
	return nil
}

// ReportPreview records where CI deployed the branch's preview; ""
// clears it.
func (b *Branch) ReportPreview(raw string, now time.Time) (bool, error) {
	if raw != "" {
		u, err := url.Parse(raw)
		if err != nil || len(raw) > MaxPreviewURLLen || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return false, fmt.Errorf("%w: %q", ErrInvalidPreviewURL, raw)
		}
	}
	if b.PreviewURL == raw {
		return false, nil
	}
	b.PreviewURL = raw
	b.touch(now)
	return true, nil
}

// ProposalsExpired reports whether the branch's proposed messages are
// due to become obsolete: it was closed or merged at least
// ProposalRetention ago.
func (b Branch) ProposalsExpired(now time.Time) bool {
	return b.State != BranchOpen && b.ClosedAt != nil && !now.Before(b.ClosedAt.Add(ProposalRetention))
}

func (b *Branch) touch(now time.Time) {
	b.Version++
	b.UpdatedAt = now
}
