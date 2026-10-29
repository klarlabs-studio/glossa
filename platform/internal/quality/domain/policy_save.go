package domain

import (
	"errors"
	"fmt"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// Saving a policy (RFC 0005 §4.3, §13 wave 3).
//
// The evaluator is the kernel's and stays there. What belongs here is
// the rule about *writing* one: which parts of a document a caller may
// say, which parts are the server's answer to "when did this become
// true", and how a new version is placed in front of the one it
// replaces so that no open pull request wakes up red for a rule its
// author never saw.

// Policy write errors.
var (
	// ErrPolicyBookkeeping is a write that sets what the server assigns.
	// A caller that could set the version could rewrite history; one
	// that could set effective_from or previous could unpin an open pull
	// request in the middle of its grace.
	ErrPolicyBookkeeping = errors.New(
		"quality: version, effective_from, grace_until and previous are the server's, not a policy write's")
	// ErrInvalidGrace is a grace that is negative or past MaxGrace.
	ErrInvalidGrace = errors.New("quality: invalid grace period")
)

// MaxGrace bounds how long a saved policy may pin the pull requests
// that predate it. A grace nobody bounded is a policy that never takes
// effect, which is the same failure as no policy at all — with the
// added cost that two documents are live the whole time and every run
// has to say which it used.
const MaxGrace = 90 * 24 * time.Hour

// NextPolicy is the document to store when candidate replaces current,
// saved at now with a grace of grace.
//
// Three things happen, in this order, and each of them is a rule rather
// than a convenience:
//
//  1. The candidate may not carry the server's bookkeeping. It says
//     what the policy *is*; when it became true, which number it has
//     and what it replaced are answers only the server can give.
//  2. It is validated as a document — the vocabularies, the rules, and
//     the one thing no rule may do, which is fail a build on an
//     opinion. A document that could never be graded is never stored.
//  3. It supersedes the current one: version one past it, the moment
//     recorded, and — where the save asks for a grace — the current
//     version kept behind it as the one a pull request older than the
//     save keeps grading against.
//
// The locales a require_complete names are *not* checked here: they are
// the project's, which the Catalog owns and validates when it stores
// the document. Quality asking a second time would be a second answer
// to the same question.
func NextPolicy(current, candidate checkpolicy.Policy, now time.Time, grace time.Duration) (checkpolicy.Policy, error) {
	if candidate.Version != 0 || candidate.EffectiveFrom != nil ||
		candidate.GraceUntil != nil || candidate.Previous != nil {
		return checkpolicy.Policy{}, ErrPolicyBookkeeping
	}
	if grace < 0 || grace > MaxGrace {
		return checkpolicy.Policy{}, fmt.Errorf("%w: %v is not between 0 and %v", ErrInvalidGrace, grace, MaxGrace)
	}
	valid, err := candidate.Validate(nil)
	if err != nil {
		return checkpolicy.Policy{}, err
	}
	// Supersede keeps current's own history out of the new document, so
	// a policy saved three times in a fortnight pins to the version
	// before the last and not to a chain. The record of every version is
	// the version table, not the document.
	next := valid.Supersede(current, now, grace)
	// Validated again, because Supersede is what makes the bookkeeping
	// and a document whose bookkeeping does not hold together must not
	// reach storage.
	return next.Validate(nil)
}
