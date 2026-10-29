package domain

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrChainBroken is returned by the verifier at the first entry that
// does not follow from the one before it.
var ErrChainBroken = errors.New("audit: chain broken")

// Break names how a chain broke, for reports a script can branch on.
type Break string

// The ways an entry can fail to follow the one before it.
const (
	BreakTenant   Break = "tenant_mismatch"
	BreakSequence Break = "sequence_gap"
	BreakLink     Break = "prev_hash_mismatch"
	BreakHash     Break = "hash_mismatch"
)

// ChainError says where and how a chain broke.
type ChainError struct {
	Sequence int64
	Kind     Break
	Reason   string
}

func (e *ChainError) Error() string {
	return fmt.Sprintf("%s at sequence %d: %s", ErrChainBroken, e.Sequence, e.Reason)
}

// Unwrap lets errors.Is match ErrChainBroken.
func (e *ChainError) Unwrap() error { return ErrChainBroken }

// Verifier recomputes a tenant's chain entry by entry: each must be the
// next sequence, link to the previous entry's hash, and hash to what it
// says. It streams, so a range of any length verifies in constant
// memory; `glossa audit verify` reads an export through it (VerifyExport).
//
// A chain proves a range was not edited after it was written; it does
// not protect against someone who can rewrite the whole table and
// recompute every hash (RFC 0006 §9.5).
type Verifier struct {
	tenant uuid.UUID
	head   Head
	count  int64
}

// NewVerifier starts a verifier after from: the zero Head for a chain
// read from its start, or the entry before a range (an export's first
// prev_hash and its sequence minus one).
func NewVerifier(tenant uuid.UUID, from Head) *Verifier {
	return &Verifier{tenant: tenant, head: Head{Sequence: from.Sequence, Hash: bytes.Clone(from.Hash)}}
}

// Next checks e against the chain so far and advances past it.
func (v *Verifier) Next(e Entry) error {
	want := v.head.Sequence + 1
	switch {
	case e.Tenant != v.tenant:
		return &ChainError{Sequence: e.Sequence, Kind: BreakTenant, Reason: "entry belongs to another tenant"}
	case e.Sequence != want:
		return &ChainError{Sequence: e.Sequence, Kind: BreakSequence, Reason: fmt.Sprintf("expected sequence %d", want)}
	case !bytes.Equal(e.PrevHash, v.head.hash()):
		return &ChainError{Sequence: e.Sequence, Kind: BreakLink, Reason: "prev_hash is not the previous entry's hash"}
	}
	sum, err := e.ComputeHash()
	if err != nil {
		return &ChainError{Sequence: e.Sequence, Kind: BreakHash, Reason: err.Error()}
	}
	if !bytes.Equal(sum, e.Hash) {
		return &ChainError{Sequence: e.Sequence, Kind: BreakHash, Reason: "hash does not match the entry's content"}
	}
	v.head = e.Head()
	v.count++
	return nil
}

// Head is the last verified entry's head.
func (v *Verifier) Head() Head { return v.head }

// Count is how many entries verified.
func (v *Verifier) Count() int64 { return v.count }

// Verify checks a whole range in order (see Verifier).
func Verify(tenant uuid.UUID, from Head, entries []Entry) error {
	v := NewVerifier(tenant, from)
	for _, e := range entries {
		if err := v.Next(e); err != nil {
			return err
		}
	}
	return nil
}
