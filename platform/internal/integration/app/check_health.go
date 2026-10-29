package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
)

// CheckHealth is the pull-request check's record for one project over a
// window (RFC 0005 §8, the quality dashboard's seventh number).
//
// Both halves already exist as Prometheus series — `glossa_github_
// checks_total{conclusion}` and `glossa_github_check_latency_seconds` —
// and that is exactly why this query has to exist as well: a dashboard
// cannot ask Prometheus about one project of one tenant, and the seven
// numbers are per project.
type CheckHealth struct {
	// Concluded is every check that reached a verdict in the window;
	// Succeeded, Failed and Neutral partition it.
	Concluded int
	Succeeded int
	Failed    int
	Neutral   int
	// P50 and P90 are from the pull-request event to the conclusion.
	// They stand for something only while Concluded > 0.
	P50, P90 time.Duration
}

// PassRate is successes over the checks that passed or failed, and
// false where none did.
//
// Neutral is in neither half. It is what a check concludes when it had
// nothing to grade, and folding it into either would move the rate for
// a reason nobody chose.
func (h CheckHealth) PassRate() (rate float64, measured bool) {
	graded := h.Succeeded + h.Failed
	if graded == 0 {
		return 0, false
	}
	return float64(h.Succeeded) / float64(graded), true
}

// ProjectCheckHealth is the project's concluded pull-request checks
// since a time, with how long each took to conclude.
//
// The checks of a project are the checks of the repositories its Git
// connections name — one repository can feed several projects, so the
// join is through the connection. Needs `integration.read`, the
// permission that already governs reading a project's Git integration.
func (s *Service) ProjectCheckHealth(ctx context.Context, project uuid.UUID, since time.Time) (CheckHealth, error) {
	if err := authz.RequireIn(ctx, authz.IntegrationRead, project); err != nil {
		return CheckHealth{}, err
	}
	if _, err := s.catalog.Project(ctx, project); err != nil {
		return CheckHealth{}, err
	}
	if since.IsZero() {
		since = s.now().Add(-30 * 24 * time.Hour)
	}
	var out CheckHealth
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		out, err = st.CheckHealth(ctx, project, since.UTC())
		return err
	})
	return out, err
}
