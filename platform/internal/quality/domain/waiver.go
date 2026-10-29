package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Waivers (RFC 0005 §2.3): some findings are correct and still fine.
// "Login" is the German term; the Japanese button is two lines by
// design. A waiver is the whole mechanism, and its reason is the whole
// point — a suppression nobody had to justify is technical debt with no
// paper trail.

// WaiverScope says how far a waiver reaches.
type WaiverScope string

// Waiver scopes.
const (
	// WaiverProject accepts the finding everywhere in the project.
	WaiverProject WaiverScope = "project"
	// WaiverBranch accepts it on one branch only.
	WaiverBranch WaiverScope = "branch"
)

// maxReason bounds the stored reason, matching the column.
const maxReason = 1000

// Waiver errors.
var (
	// ErrReasonRequired is a waiver with no reason. There is no way to
	// waive a finding without saying why.
	ErrReasonRequired = errors.New("quality: a waiver needs a reason")
	// ErrReasonTooLong is a reason past maxReason characters.
	ErrReasonTooLong = errors.New("quality: the reason is too long")
	// ErrInvalidScope is a scope that is neither project nor branch.
	ErrInvalidScope = errors.New("quality: a waiver is scoped to the project or to a branch")
	// ErrBranchRequired is a branch-scoped waiver that names no branch.
	ErrBranchRequired = errors.New("quality: a branch-scoped waiver needs a branch")
	// ErrInvalidFingerprint is a waiver against something that is not a
	// finding fingerprint.
	ErrInvalidFingerprint = errors.New("quality: not a finding fingerprint")
	// ErrExpiryInThePast is a waiver that has already expired when it is
	// made. It would accept nothing, so saying no is kinder than storing
	// a waiver whose reason nobody will ever read.
	ErrExpiryInThePast = errors.New("quality: a waiver cannot expire in the past")
)

// Waiver is one accepted finding.
type Waiver struct {
	ID      uuid.UUID
	Project uuid.UUID
	// Fingerprint is the finding this accepts. It is a fingerprint and
	// not a finding ID, so the waiver survives the run that found it.
	Fingerprint string
	// Reason is why this finding is fine. Required, non-empty.
	Reason string
	Scope  WaiverScope
	// Ref is the branch, for WaiverBranch.
	Ref string
	// SourceRevision is the source revision the waiver was made against.
	// The waiver dies when the source moves past it: the German somebody
	// waived is not the German that now ships.
	SourceRevision int
	CreatedBy      string
	CreatedAt      time.Time
	// ExpiresAt is when the daily sweep retires the waiver; nil never.
	//
	// It stays optional (RFC 0005 §15 question 4, decided by the owner).
	// Some waivers are genuinely permanent decisions — "Login is the
	// German term" does not expire — and forcing a date makes people
	// pick one at random. The dashboard makes staleness visible instead:
	// the waivers no stored finding carries any more, and the ones about
	// to expire.
	ExpiresAt *time.Time
	// ExpiredAt is when the daily sweep recorded the expiry; nil while
	// the date has not passed, or has passed and the sweep has not run
	// yet. A waiver that expires is not deleted — it is recorded as
	// expired, the same way revoking never deletes — so the reason
	// somebody wrote is still there to read.
	ExpiredAt *time.Time
	// RevokedAt is when a person took it back; nil while it stands.
	RevokedAt *time.Time
}

// Expires reports whether w is a waiver the sweep can retire at now: it
// has a date, the date has passed, nobody revoked it, and no sweep has
// recorded it yet.
func (w Waiver) Expires(now time.Time) bool {
	return w.ExpiresAt != nil && !now.Before(*w.ExpiresAt) && w.RevokedAt == nil && w.ExpiredAt == nil
}

// Validate checks a waiver before it is stored. A waiver without a
// reason is a 400 and not a silently accepted blank.
func (w Waiver) Validate() error {
	if !strings.HasPrefix(w.Fingerprint, FingerprintPrefix) || len(w.Fingerprint) <= len(FingerprintPrefix) {
		return fmt.Errorf("%w: %q", ErrInvalidFingerprint, w.Fingerprint)
	}
	if strings.TrimSpace(w.Reason) == "" {
		return ErrReasonRequired
	}
	if len([]rune(w.Reason)) > maxReason {
		return fmt.Errorf("%w: %d characters, at most %d", ErrReasonTooLong, len([]rune(w.Reason)), maxReason)
	}
	switch w.Scope {
	case WaiverProject:
	case WaiverBranch:
		if w.Ref == "" {
			return ErrBranchRequired
		}
	default:
		return fmt.Errorf("%w: %q", ErrInvalidScope, w.Scope)
	}
	return nil
}

// Covers reports whether w accepts the finding f on ref at now.
//
// Three things have to hold, and the third is the one that matters: the
// waiver must not be revoked or expired, it must reach the ref, and the
// source revision f was computed against must be the one the waiver was
// made against. A newer source revision brings the finding back as a
// normal finding — Stale says so, and the caller explains why.
func (w Waiver) Covers(f Finding, ref string, now time.Time) bool {
	if w.Fingerprint != f.Fingerprint || !w.Live(now) {
		return false
	}
	if w.Scope == WaiverBranch && w.Ref != ref {
		return false
	}
	return !w.Stale(f)
}

// Live reports whether the waiver still stands at now: not revoked, not
// expired.
//
// The date is what decides, not the sweep: a waiver stops accepting
// findings the moment it expires, whether or not the daily job has run
// yet. The sweep only writes that down, so a job that is late never
// leaves a dead waiver accepting anything. ExpiredAt is honoured all
// the same, so a recorded expiry can never be contradicted by a clock
// that went backwards.
func (w Waiver) Live(now time.Time) bool {
	if w.RevokedAt != nil || w.ExpiredAt != nil {
		return false
	}
	return w.ExpiresAt == nil || now.Before(*w.ExpiresAt)
}

// Stale reports whether f was computed against a different source
// revision than the waiver was made against. A finding that carries no
// source revision is never stale: the waiver has nothing to disagree
// with.
func (w Waiver) Stale(f Finding) bool {
	return f.SourceRevision != nil && *f.SourceRevision != w.SourceRevision
}

// StaleReason is what a reappeared finding says about its dead waiver.
func (w Waiver) StaleReason(f Finding) string {
	if !w.Stale(f) {
		return ""
	}
	return fmt.Sprintf("waived against source revision %d, now at %d", w.SourceRevision, *f.SourceRevision)
}

// Waivers applies a project's waivers to a run's findings on ref: a
// covered finding is reported at severity Waived with its waiver named,
// and everything else is returned untouched. Nothing is ever dropped.
func Waivers(ws []Waiver, fs []Finding, ref string, now time.Time) []Finding {
	if len(ws) == 0 || len(fs) == 0 {
		return fs
	}
	out := make([]Finding, len(fs))
	copy(out, fs)
	for i, f := range out {
		for _, w := range ws {
			if w.Covers(f, ref, now) {
				out[i] = f.Waive(w.ID.String())
				break
			}
		}
	}
	return out
}
