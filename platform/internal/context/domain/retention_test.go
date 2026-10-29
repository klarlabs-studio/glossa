package domain_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/context/domain"
)

var otherApplication = uuid.MustParse("0192a1b2-0000-7000-8000-0000000000dd")

// history builds n summaries of one (application, source, branch), one
// hour apart, oldest first; their IDs are derived from label.
func history(label string, app uuid.UUID, source domain.Source, branch string, onDefault bool, n int, start time.Time) []domain.BuildSummary {
	out := make([]domain.BuildSummary, n)
	for i := range n {
		out[i] = domain.BuildSummary{
			ID:            uuid.NewSHA1(uuid.NameSpaceOID, []byte(label+string(rune('a'+i)))),
			ApplicationID: app, Source: source, Branch: domain.Branch(branch), OnDefaultBranch: onDefault,
			CreatedAt: start.Add(time.Duration(i) * time.Hour),
		}
	}
	return out
}

func ids(bs []domain.BuildSummary) []uuid.UUID {
	out := make([]uuid.UUID, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}

func sortedIDs(in []uuid.UUID) []string {
	out := make([]string, len(in))
	for i, id := range in {
		out[i] = id.String()
	}
	slices.Sort(out)
	return out
}

func sameIDs(t *testing.T, what string, got, want []uuid.UUID) {
	t.Helper()
	if !slices.Equal(sortedIDs(got), sortedIDs(want)) {
		t.Errorf("%s = %v, want %v", what, sortedIDs(got), sortedIDs(want))
	}
}

func TestRetentionKeepsTheLatestThreePerApplicationBranchAndSource(t *testing.T) {
	start := now.Add(-30 * 24 * time.Hour)
	mainWeb := history("main-web", application, domain.SourcePlugin, "main", true, 8, start)
	mainExtract := history("main-extract", application, domain.SourceExtract, "main", true, 7, start)
	mainOther := history("main-other", otherApplication, domain.SourcePlugin, "main", true, 6, start)
	feature := history("feature", application, domain.SourcePlugin, "feat/x", false, 6, start)
	all := slices.Concat(mainWeb, mainExtract, mainOther, feature)

	got := domain.DefaultRetention.Expired(all, nil, now)

	want := slices.Concat(ids(mainWeb[:5]), ids(mainExtract[:4]), ids(mainOther[:3]), ids(feature[:3]))
	sameIDs(t, "expired", got, want)
	if domain.DefaultRetention.Keep != 3 {
		t.Errorf("Keep = %d, want 3 (RFC 0004 §2.3)", domain.DefaultRetention.Keep)
	}
}

func TestRetentionAlwaysKeepsCurrentBuilds(t *testing.T) {
	// Keeping no history leaves exactly the current builds: the latest
	// default-branch build per (application, source), whatever the
	// default branch was called then, and each open branch's latest.
	none := domain.RetentionPolicy{Keep: 0, ClosedBranchGrace: 7 * 24 * time.Hour}
	start := now.Add(-30 * 24 * time.Hour)
	master := history("master", application, domain.SourceExtract, "master", true, 3, start)
	main := history("main", application, domain.SourcePlugin, "main", true, 3, start)
	feature := history("feat", application, domain.SourcePlugin, "feat/x", false, 2, start)

	got := none.Expired(slices.Concat(master, main, feature), nil, now)

	sameIDs(t, "expired", got, slices.Concat(ids(master[:2]), ids(main[:2]), ids(feature[:1])))
}

func TestRetentionPurgesClosedBranchesAfterTheGracePeriod(t *testing.T) {
	start := now.Add(-40 * 24 * time.Hour)
	closedLongAgo := history("old", application, domain.SourcePlugin, "feat/old", false, 2, start)
	closedRecently := history("recent", application, domain.SourcePlugin, "feat/recent", false, 2, start)
	open := history("open", application, domain.SourcePlugin, "feat/open", false, 2, start)
	main := history("main", application, domain.SourcePlugin, "main", true, 2, start)
	closed := map[domain.Branch]time.Time{
		"feat/old":    now.Add(-8 * 24 * time.Hour),
		"feat/recent": now.Add(-6 * 24 * time.Hour),
		// A default-branch build never goes because a branch of the same
		// name was closed.
		"main": now.Add(-30 * 24 * time.Hour),
	}
	got := domain.DefaultRetention.Expired(slices.Concat(closedLongAgo, closedRecently, open, main), closed, now)
	sameIDs(t, "expired", got, ids(closedLongAgo))
}

func TestRetentionClosedBranchGraceIsExactlySevenDays(t *testing.T) {
	// The clock is the test's: the boundary is checked to the second,
	// not with a slack of hours.
	closedAt := now.Add(-20 * 24 * time.Hour)
	builds := history("feat", application, domain.SourcePlugin, "feat/x", false, 2, closedAt.Add(-time.Hour))
	closed := map[domain.Branch]time.Time{"feat/x": closedAt}
	grace := domain.DefaultRetention.ClosedBranchGrace

	for _, tc := range []struct {
		name string
		at   time.Time
		want []uuid.UUID
	}{
		// A closed branch's latest build is current in its branch's view
		// and within the three-build window, so nothing goes early.
		{"a second before the grace ends", closedAt.Add(grace - time.Second), nil},
		{"exactly when it ends", closedAt.Add(grace), ids(builds)},
		{"a second after", closedAt.Add(grace + time.Second), ids(builds)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sameIDs(t, "expired", domain.DefaultRetention.Expired(builds, closed, tc.at), tc.want)
		})
	}
	// Catalog's proposal retention is a different rule over different
	// data (RFC 0004 §4.1) and stays at 14 days; catalog/domain pins it.
	if grace != 7*24*time.Hour {
		t.Errorf("ClosedBranchGrace = %v, want 7 days (RFC 0004 §2.3)", grace)
	}
}

func TestRetentionOfNothingIsNothing(t *testing.T) {
	if got := domain.DefaultRetention.Expired(nil, nil, now); len(got) != 0 {
		t.Errorf("expired = %v", got)
	}
}

func TestCurrentBuilds(t *testing.T) {
	start := now.Add(-5 * 24 * time.Hour)
	main := history("main", application, domain.SourcePlugin, "main", true, 3, start)
	mainExtract := history("main-x", application, domain.SourceExtract, "main", true, 2, start)
	mainOther := history("main-o", otherApplication, domain.SourcePlugin, "main", true, 2, start)
	feature := history("feat", application, domain.SourcePlugin, "feat/x", false, 2, start)
	all := slices.Concat(main, mainExtract, mainOther, feature)

	sameIDs(t, "default view", domain.CurrentBuilds(all, ""),
		[]uuid.UUID{main[2].ID, mainExtract[1].ID, mainOther[1].ID})
	// The branch rebuilt only the web plugin source: the rest falls back
	// to the default branch.
	sameIDs(t, "branch view", domain.CurrentBuilds(all, "feat/x"),
		[]uuid.UUID{feature[1].ID, mainExtract[1].ID, mainOther[1].ID})
	sameIDs(t, "unknown branch", domain.CurrentBuilds(all, "feat/none"), domain.CurrentBuilds(all, ""))
}
