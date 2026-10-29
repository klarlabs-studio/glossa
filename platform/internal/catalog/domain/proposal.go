package domain

import (
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// ProposalKind says what a branch proposes for a key.
type ProposalKind string

// Proposal kinds.
const (
	// ProposalNewKey: the key is new on the branch. The branch owns the
	// key's proposed message (shared with every other branch proposing
	// the same source for it).
	ProposalNewKey ProposalKind = "new_key"
	// ProposalSourceChange: the key's message is live and the branch
	// changes its source. The change is a proposal, not a source
	// revision, so the append-only source log only records merged
	// history (RFC 0004 §4.1).
	ProposalSourceChange ProposalKind = "source_change"
)

// Proposal is what one branch says about one key: the source it pushed,
// and the message that key names. A branch has at most one per key.
type Proposal struct {
	BranchID  BranchID
	Key       MessageKey
	MessageID MessageID
	Kind      ProposalKind
	// Source is what the branch pushed for the key.
	Source mfcontent.Content
	// BaseRevision is, for a source change, the message's revision the
	// change was proposed against; 0 for a new key.
	BaseRevision int
	Author       Author
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ProposeNewKey records that branch proposes m, a proposed message, with
// source (m's own source unless another branch's differs: a conflict).
func ProposeNewKey(branch BranchID, m Message, source mfcontent.Content, by Author, now time.Time) Proposal {
	return Proposal{
		BranchID: branch, Key: m.Key, MessageID: m.ID, Kind: ProposalNewKey, Source: source,
		Author: by, CreatedAt: now, UpdatedAt: now,
	}
}

// ProposeSourceChange records that branch changes live message m's
// source to source.
func ProposeSourceChange(branch BranchID, m Message, source mfcontent.Content, by Author, now time.Time) Proposal {
	return Proposal{
		BranchID: branch, Key: m.Key, MessageID: m.ID, Kind: ProposalSourceChange, Source: source,
		BaseRevision: m.Revision, Author: by, CreatedAt: now, UpdatedAt: now,
	}
}

// Matches reports whether m's current source is the proposed one.
func (p Proposal) Matches(m Message) bool {
	return p.MessageID == m.ID && p.Source.SameModel(m.Source)
}

// Update replaces the proposed source and, for a source change, its base
// revision; false if both are unchanged.
func (p *Proposal) Update(source mfcontent.Content, base int, now time.Time) bool {
	if p.Source.SameModel(source) && p.BaseRevision == base {
		return false
	}
	p.Source, p.BaseRevision, p.UpdatedAt = source, base, now
	return true
}
