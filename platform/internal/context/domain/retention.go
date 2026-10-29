package domain

import (
	"cmp"
	"slices"
	"time"

	"github.com/google/uuid"
)

// BuildSummary is what retention and the current views need of a build.
type BuildSummary struct {
	ID              uuid.UUID
	ApplicationID   uuid.UUID
	Source          Source
	Branch          Branch
	OnDefaultBranch bool
	CreatedAt       time.Time
}

// Summary returns b's summary.
func (b Build) Summary() BuildSummary {
	return BuildSummary{
		ID: b.ID, ApplicationID: b.ApplicationID, Source: b.Source, Branch: b.Branch,
		OnDefaultBranch: b.OnDefaultBranch, CreatedAt: b.CreatedAt,
	}
}

// newer orders summaries newest first (UUIDv7 IDs break ties in
// creation order).
func newer(a, b BuildSummary) int {
	if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
		return c
	}
	return cmp.Compare(b.ID.String(), a.ID.String())
}

// lineage is one collector's stream of builds for one application: the
// current views pick the latest build per lineage, so a capture build
// or a Go extract never displaces the bundler plugin's usages.
type lineage struct {
	application uuid.UUID
	source      Source
}

// CurrentBuilds returns the builds whose usages are current (RFC 0004
// §2.2): per application and source, the latest default-branch build;
// in the view of branch (empty: the default view), that branch's latest
// build where it has one, falling back to the default branch.
func CurrentBuilds(builds []BuildSummary, branch Branch) []uuid.UUID {
	latest := map[lineage]BuildSummary{}
	onBranch := map[lineage]BuildSummary{}
	for _, b := range builds {
		l := lineage{b.ApplicationID, b.Source}
		if b.OnDefaultBranch {
			if cur, ok := latest[l]; !ok || newer(b, cur) < 0 {
				latest[l] = b
			}
		}
		if branch != "" && b.Branch == branch {
			if cur, ok := onBranch[l]; !ok || newer(b, cur) < 0 {
				onBranch[l] = b
			}
		}
	}
	for l, b := range onBranch {
		latest[l] = b
	}
	out := make([]uuid.UUID, 0, len(latest))
	for _, b := range latest {
		out = append(out, b.ID)
	}
	return out
}

// RetentionPolicy says which builds are kept (RFC 0004 §2.3).
type RetentionPolicy struct {
	// Keep is how many builds are kept per application, branch and
	// source, newest first.
	Keep int
	// ClosedBranchGrace is how long a closed branch's builds outlive it.
	ClosedBranchGrace time.Duration
}

// DefaultRetention keeps the latest 3 builds per application and branch
// (and source) and purges a closed branch's builds 7 days after it
// closed (RFC 0004 §2.3, the owner's decision of 2026-09-20).
//
// This is Context's retention, over builds and the captures that hang
// off them. Catalog's catalogdomain.ProposalRetention — how long a
// closed branch's proposed messages stay proposed — is a different rule
// over different data and stays at 14 days: text a translator worked on
// outlives the screenshots of it.
var DefaultRetention = RetentionPolicy{Keep: 3, ClosedBranchGrace: 7 * 24 * time.Hour}

type stream struct {
	lineage
	branch Branch
}

// Expired returns the builds to delete at now, given when each closed
// branch closed. Every build that is still current — the latest
// default-branch build per application and source, and each open
// branch's latest — is kept whatever its age, except that a closed
// branch's builds go once the grace period has passed. Default-branch
// builds never go with a closed branch.
func (p RetentionPolicy) Expired(builds []BuildSummary, closed map[Branch]time.Time, now time.Time) []uuid.UUID {
	keep := map[uuid.UUID]bool{}
	for _, id := range CurrentBuilds(builds, "") {
		keep[id] = true
	}
	streams := map[stream][]BuildSummary{}
	for _, b := range builds {
		s := stream{lineage{b.ApplicationID, b.Source}, b.Branch}
		streams[s] = append(streams[s], b)
	}
	for _, bs := range streams {
		slices.SortFunc(bs, newer)
		if !bs[0].OnDefaultBranch {
			keep[bs[0].ID] = true // current in its branch's view
		}
		for _, b := range bs[:min(p.Keep, len(bs))] {
			keep[b.ID] = true
		}
	}
	var out []uuid.UUID
	for _, b := range builds {
		closedAt, isClosed := closed[b.Branch]
		gone := !b.OnDefaultBranch && isClosed && !now.Before(closedAt.Add(p.ClosedBranchGrace))
		if gone || !keep[b.ID] {
			out = append(out, b.ID)
		}
	}
	return out
}
