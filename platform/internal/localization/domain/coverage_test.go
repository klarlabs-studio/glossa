package domain_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

func TestLocaleCoverage(t *testing.T) {
	tests := []struct {
		name     string
		messages int
		states   domain.StateCounts
		outdated int
		want     domain.Coverage
	}{
		{
			name: "empty locale", messages: 5,
			want: domain.Coverage{Messages: 5, Missing: 5},
		},
		{
			name: "rejected text is missing", messages: 5,
			states: domain.StateCounts{Draft: 1, NeedsReview: 1, Approved: 1, Rejected: 1}, outdated: 1,
			want: domain.Coverage{Messages: 5, Translated: 3, Missing: 2, Outdated: 1,
				States: domain.StateCounts{Draft: 1, NeedsReview: 1, Approved: 1, Rejected: 1}},
		},
		{
			name: "fully approved", messages: 2, states: domain.StateCounts{Approved: 2},
			want: domain.Coverage{Messages: 2, Translated: 2, States: domain.StateCounts{Approved: 2}},
		},
		{
			// The projection can briefly count a translation of a message
			// whose activation it hasn't seen; missing never goes negative.
			name: "never negative", messages: 1, states: domain.StateCounts{Approved: 2},
			want: domain.Coverage{Messages: 1, Translated: 2, States: domain.StateCounts{Approved: 2}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.LocaleCoverage(tc.messages, tc.states, tc.outdated); got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSourceCoverageIsComplete(t *testing.T) {
	got := domain.SourceCoverage(7)
	want := domain.Coverage{Messages: 7, Translated: 7, States: domain.StateCounts{Approved: 7}}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
