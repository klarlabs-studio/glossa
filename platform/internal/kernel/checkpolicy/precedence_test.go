package checkpolicy_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// TestMostSpecific pins the shared precedence rule on its own, apart
// from any one selector: matching first, then specificity, then the
// later candidate on a tie. Workflow's bindings resolve through the same
// function (RFC 0006 §2.3), so a change here changes both on purpose.
func TestMostSpecific(t *testing.T) {
	type c struct {
		spec  int
		match bool
	}
	matches := func(x c) bool { return x.match }
	spec := func(x c) int { return x.spec }
	cases := []struct {
		name string
		in   []c
		want int
	}{
		{"nothing", nil, -1},
		{"nothing matches", []c{{2, false}, {3, false}}, -1},
		{"more specific wins wherever it stands", []c{{3, true}, {1, true}}, 0},
		{"tie goes to the later", []c{{2, true}, {2, true}}, 1},
		{"a non-match never wins however specific", []c{{1, true}, {5, false}}, 0},
		{"zero specificity still matches", []c{{0, true}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkpolicy.MostSpecific(tc.in, matches, spec); got != tc.want {
				t.Fatalf("MostSpecific = %d, want %d", got, tc.want)
			}
		})
	}
}
