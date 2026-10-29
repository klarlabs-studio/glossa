package domain

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Release is an immutable snapshot of what a project says, in every
// locale, at one moment (intent §36): versioned, auditable, reproducible
// and never changed after it is recorded — the database refuses UPDATE
// and DELETE on it. Environments point at releases.
type Release struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	// Version counts the project's releases from 1, without gaps.
	Version int
	// Parent is the release the environment served when this one was
	// published (uuid.Nil for its first).
	Parent uuid.UUID
	// Environment is where it was published; its policy is recorded.
	Environment string
	Policy      Policy
	// Branch is the branch whose overlay the release was built with, for
	// a branch environment's release; "" for the main catalog's.
	Branch  string
	Content Content
	// Digest is Content.Digest(), the manifest digest.
	Digest    string
	Stats     Stats
	Note      string
	Author    string
	CreatedAt time.Time
}

// MaxNoteLen bounds a release note.
const MaxNoteLen = 1000

// NewRelease records a release built for env, under its policy (and, for
// a branch environment, with its branch's overlay).
func NewRelease(id, project uuid.UUID, version int, parent uuid.UUID, env Environment, b Built, note, by string, now time.Time) (Release, error) {
	if utf8.RuneCountInString(note) > MaxNoteLen {
		return Release{}, ErrInvalidNote
	}
	if version < 1 {
		return Release{}, fmt.Errorf("release: version %d", version)
	}
	digest, err := b.Content.Digest()
	if err != nil {
		return Release{}, err
	}
	return Release{
		ID: id, ProjectID: project, Version: version, Parent: parent, Environment: env.Name, Policy: env.Policy, Branch: env.Branch,
		Content: b.Content, Digest: digest, Stats: b.Stats, Note: note, Author: by,
		// Postgres keeps microseconds; the manifest, seconds.
		CreatedAt: now.UTC().Truncate(time.Microsecond),
	}, nil
}

// Promotable reports whether the release may be promoted: a branch
// release may not, because it holds text that exists only on its branch
// (RFC 0004 §4.2). Staging and production receive only releases built
// for them or promoted from main-catalog environments.
func (r Release) Promotable() error {
	if r.Branch != "" {
		return ErrBranchReleaseNotPromotable
	}
	return nil
}

// Artifacts lists the release's artifact references by locale, then
// namespace.
func (r Release) Artifacts() map[string]map[string]ArtifactRef { return r.Content.Artifacts }
