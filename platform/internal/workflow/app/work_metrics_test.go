package app_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

type recordingDecisions map[[2]string]int

func (r recordingDecisions) Decision(subject, verdict string) { r[[2]string{subject, verdict}]++ }

// glossa_approvals_decisions_total{subject,decision} moves once per
// recorded decision: a repeated grant records nothing and is not
// counted, and a refused one is not either (RFC 0006 §10.1).
func TestRecordedDecisionsAreCounted(t *testing.T) {
	w := newWorld(t)
	m := recordingDecisions{}
	clock := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	w.svc = app.NewWorkService(w.work, w.dir, w.authors,
		app.WithWorkClock(func() time.Time { return clock }), app.WithWorkMetrics(m))
	author := w.person([]string{"translator"}, []string{"de"}, nil, uuid.Nil)
	a, _ := w.requestApproval(2, domain.Party{Role: "reviewer"}, actorOf(author))

	first := w.person([]string{"reviewer"}, []string{"de"}, nil, uuid.Nil)
	for range 2 { // the second click records nothing
		if _, err := w.svc.Decide(first, a.ID, domain.VerdictGranted, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.svc.Decide(w.token("read", "write"), a.ID, domain.VerdictGranted, ""); err == nil {
		t.Fatal("a token decided")
	}
	if _, err := w.svc.Decide(w.person([]string{"reviewer"}, []string{"de"}, nil, uuid.Nil), a.ID, domain.VerdictDenied, "no"); err != nil {
		t.Fatal(err)
	}
	want := recordingDecisions{{"translation", "granted"}: 1, {"translation", "denied"}: 1}
	if len(m) != len(want) || m[[2]string{"translation", "granted"}] != 1 || m[[2]string{"translation", "denied"}] != 1 {
		t.Errorf("decisions = %v, want %v", m, want)
	}
}
