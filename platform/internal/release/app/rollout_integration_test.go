//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	identitydomain "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/release/adapters/metrics"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// rolloutProject is production serving v1 and staging serving v2, which
// changes the de text: v2 is the candidate a rollout serves beside v1.
func (h *harness) rolloutProject(t *testing.T) (project uuid.UUID, stable, candidate domain.Release) {
	t.Helper()
	p := h.project(t, []string{"de"}, shop)
	h.translate(t, p, "checkout.pay", "de", "{amount, number} bezahlen", "approved")
	ctx := h.as("developer")
	v1, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, "")
	if err != nil {
		t.Fatal(err)
	}
	h.translate(t, p, "checkout.pay", "de", "Jetzt {amount, number} zahlen", "approved")
	v2, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "staging"}, "")
	if err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	return p, v1, v2
}

// rawManifest is the manifest glossa-edge would serve, read from the
// object it serves it from.
func (h *harness) rawManifest(t *testing.T, project uuid.UUID, environment string) map[string]json.RawMessage {
	t.Helper()
	body, err := h.objects.Get(context.Background(), delivery.ManifestPath(project.String(), environment), delivery.MaxManifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (h *harness) actor(ctx context.Context) string {
	p, _ := authz.From(ctx)
	return p.Actor.String()
}

// rolloutEvents lists the rollout events recorded for project, oldest
// first, as "type actor end".
func rolloutEvents(t *testing.T, project uuid.UUID) []string {
	t.Helper()
	rows, err := env.Super.Query(context.Background(), `
		SELECT event_type, actor, coalesce(payload->>'end', '') FROM outbox_events
		WHERE event_type LIKE 'release.rollout.%' AND payload->>'project_id' = $1 ORDER BY occurred_at, id`, project.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var typ, actor, end string
		if err := rows.Scan(&typ, &actor, &end); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.TrimSpace(typ+" "+actor+" "+end))
	}
	return out
}

func TestRolloutWritesTheManifestMemberAndEnds(t *testing.T) {
	h := newHarness(t)
	p, v1, v2 := h.rolloutProject(t)
	ctx := h.as("developer")
	by := h.actor(ctx)

	ro, replayed, err := h.svc.StartRollout(ctx, p, "production", app.RolloutInput{Release: v2.ID, Percent: 10}, "ro-1")
	if err != nil || replayed {
		t.Fatalf("start: %v (replayed %v)", err, replayed)
	}
	if !domain.ValidSalt(ro.Salt) || ro.Stable != v1.ID || ro.Candidate != v2.ID || ro.StartedBy != by {
		t.Fatalf("rollout %+v", ro)
	}
	// The edge's object carries the rollout, signed, with v1 still the
	// top-level (stable) release.
	m, _ := h.served(t, p, "production")
	if m.Release.ID != v1.ID.String() || m.Rollout == nil || m.Rollout.ID != ro.ID.String() || m.Rollout.Percent != 10 ||
		m.Rollout.Salt != ro.Salt || m.Rollout.Candidate.Release.ID != v2.ID.String() || m.Rollout.Candidate.Release.Version != 2 {
		t.Fatalf("manifest at the edge path: %+v rollout %+v", m, m.Rollout)
	}
	if again, replayed, err := h.svc.StartRollout(ctx, p, "production", app.RolloutInput{Release: v2.ID, Percent: 10}, "ro-1"); err != nil || !replayed || again.ID != ro.ID {
		t.Errorf("replayed start: %v %v", err, replayed)
	}

	// One active rollout per environment; publish and promote wait for
	// it to end.
	if _, _, err := h.svc.StartRollout(ctx, p, "production", app.RolloutInput{Release: v2.ID, Percent: 20}, ""); !errors.Is(err, domain.ErrRolloutActive) {
		t.Errorf("a second rollout: %v", err)
	}
	if _, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, ""); !errors.Is(err, domain.ErrRolloutActive) {
		t.Errorf("publish during a rollout: %v", err)
	}
	if _, err := h.svc.Promote(ctx, p, "production", v2.ID, app.PromoteInput{}); !errors.Is(err, domain.ErrRolloutActive) {
		t.Errorf("promote during a rollout: %v", err)
	}

	// Advance up, then down; the salt stays.
	for _, pct := range []int{50, 30} {
		if ro, err = h.svc.AdvanceRollout(ctx, p, "production", ro.ID, pct, &ro.Version); err != nil {
			t.Fatal(err)
		}
		if m, _ := h.served(t, p, "production"); m.Rollout == nil || m.Rollout.Percent != pct || m.Rollout.Salt != ro.Salt {
			t.Fatalf("after advancing to %d%%: %+v", pct, m.Rollout)
		}
	}
	stale := 1
	if _, err := h.svc.AdvanceRollout(ctx, p, "production", ro.ID, 40, &stale); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("advance with a stale version: %v", err)
	}

	// Abort: the member goes, the pointer stays.
	if ro, err = h.svc.AbortRollout(ctx, p, "production", ro.ID, nil); err != nil || ro.End != domain.EndAborted {
		t.Fatalf("abort: %v %+v", err, ro)
	}
	if raw := h.rawManifest(t, p, "production"); raw["rollout"] != nil {
		t.Errorf("an aborted rollout is still in the manifest: %s", raw["rollout"])
	}
	if m, _ := h.served(t, p, "production"); m.Release.ID != v1.ID.String() {
		t.Errorf("abort moved the pointer to %s", m.Release.ID)
	}
	if _, err := h.svc.AbortRollout(ctx, p, "production", ro.ID, nil); !errors.Is(err, domain.ErrRolloutEnded) {
		t.Errorf("aborting twice: %v", err)
	}

	// A new rollout gets a new id and salt; completing it moves the
	// pointer to the candidate and drops the member.
	second, _, err := h.svc.StartRollout(ctx, p, "production", app.RolloutInput{Release: v2.ID, Percent: 5}, "")
	if err != nil || second.ID == ro.ID || second.Salt == ro.Salt {
		t.Fatalf("second rollout: %v %+v", err, second)
	}
	if second, err = h.svc.CompleteRollout(ctx, p, "production", second.ID, nil); err != nil || second.End != domain.EndCompleted {
		t.Fatalf("complete: %v %+v", err, second)
	}
	envr, err := h.svc.GetEnvironment(ctx, p, "production")
	if err != nil || envr.Current != v2.ID {
		t.Fatalf("after complete production serves %s, %v", envr.Current, err)
	}
	if m, _ := h.served(t, p, "production"); m.Release.ID != v2.ID.String() || m.Rollout != nil {
		t.Errorf("after complete the manifest serves %s, rollout %+v", m.Release.ID, m.Rollout)
	}
	deps, _, err := h.svc.ListDeployments(ctx, p, "production", firstPage())
	if err != nil || len(deps) != 2 || deps[0].ReleaseID != v2.ID || deps[0].Previous != v1.ID || deps[0].Action != domain.ActionPromote || deps[0].By != by {
		t.Errorf("deployments %+v, %v", deps, err)
	}
	h.drain(t) // the subscriber writes the same manifest again

	list, next, err := h.svc.ListRollouts(ctx, p, "production", pagination.Page{Size: 50})
	if err != nil || len(list) != 2 || list[0].ID != second.ID || list[1].ID != ro.ID || next != nil {
		t.Errorf("rollouts %+v, %v, %v", list, next, err)
	}
	// A page of one, then the next: the cursor walks newest first.
	first, token, err := h.svc.ListRollouts(ctx, p, "production", pagination.Page{Size: 1})
	if err != nil || len(first) != 1 || first[0].ID != second.ID || token == nil {
		t.Fatalf("first page %+v, %v, %v", first, token, err)
	}
	page, err := pagination.Parse(ptr(1), token)
	if err != nil {
		t.Fatal(err)
	}
	rest, end, err := h.svc.ListRollouts(ctx, p, "production", page)
	if err != nil || len(rest) != 1 || rest[0].ID != ro.ID || end != nil {
		t.Errorf("second page %+v, %v, %v", rest, end, err)
	}
	want := []string{
		"release.rollout.started " + by, "release.rollout.advanced " + by, "release.rollout.advanced " + by,
		"release.rollout.aborted " + by + " aborted", "release.rollout.started " + by, "release.rollout.completed " + by + " completed",
	}
	if got := rolloutEvents(t, p); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRollbackAbortsAnActiveRollout(t *testing.T) {
	h := newHarness(t)
	p, v1, v2 := h.rolloutProject(t)
	ctx := h.as("developer")
	if _, err := h.svc.Promote(ctx, p, "production", v2.ID, app.PromoteInput{}); err != nil {
		t.Fatal(err)
	}
	v3 := v1 // production rolls v1 out again beside v2: any earlier release is a candidate
	ro, _, err := h.svc.StartRollout(ctx, p, "production", app.RolloutInput{Release: v3.ID, Percent: 50}, "")
	if err != nil {
		t.Fatal(err)
	}
	envr, err := h.svc.Rollback(ctx, p, "production", nil)
	if err != nil || envr.Current != v1.ID {
		t.Fatalf("rollback: %v, serves %s", err, envr.Current)
	}
	got, err := h.svc.GetRollout(ctx, p, ro.ID)
	if err != nil || got.Status != domain.RolloutAborted || got.End != domain.EndRolledBack || got.EndedBy != h.actor(ctx) {
		t.Fatalf("after rollback: %+v, %v", got, err)
	}
	if m, _ := h.served(t, p, "production"); m.Release.ID != v1.ID.String() || m.Rollout != nil {
		t.Errorf("after rollback the manifest serves %s, rollout %+v", m.Release.ID, m.Rollout)
	}
}

func TestRolloutRefusals(t *testing.T) {
	h := newHarness(t)
	p, _, v2 := h.rolloutProject(t)
	ctx := h.as("developer")
	if _, _, err := h.svc.StartRollout(ctx, p, "production", app.RolloutInput{Release: v2.ID, Percent: 101}, ""); !errors.Is(err, domain.ErrInvalidPercent) {
		t.Errorf("101%%: %v", err)
	}
	if _, _, err := h.svc.StartRollout(ctx, p, "production", app.RolloutInput{Release: uuid.New(), Percent: 10}, ""); !errors.Is(err, app.ErrReleaseNotInProject) {
		t.Errorf("an unknown release: %v", err)
	}
	if _, _, err := h.svc.StartRollout(ctx, p, "development", app.RolloutInput{Release: v2.ID, Percent: 10}, ""); !errors.Is(err, domain.ErrRolloutNoStable) {
		t.Errorf("an environment serving nothing: %v", err)
	}
	if _, _, err := h.svc.StartRollout(ctx, p, "staging", app.RolloutInput{Release: v2.ID, Percent: 10}, ""); !errors.Is(err, domain.ErrRolloutCandidateServed) {
		t.Errorf("rolling out what staging serves: %v", err)
	}
	if _, _, err := h.svc.StartRollout(h.as("translator"), p, "production", app.RolloutInput{Release: v2.ID, Percent: 10}, ""); err == nil {
		t.Error("a translator started a rollout")
	}
	if _, err := h.svc.AdvanceRollout(ctx, p, "production", uuid.New(), 20, nil); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("advancing no rollout: %v", err)
	}
}

func TestAnotherTenantSeesNoRollout(t *testing.T) {
	h := newHarness(t)
	p, _, v2 := h.rolloutProject(t)
	ro, _, err := h.svc.StartRollout(h.as("developer"), p, "production", app.RolloutInput{Release: v2.ID, Percent: 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := env.SeedTenant(context.Background(), "globex")
	if err != nil {
		t.Fatal(err)
	}
	octx := authztest.Member(context.Background(), other, []string{"owner"})
	if _, err := h.svc.GetRollout(octx, p, ro.ID); err == nil {
		t.Error("another tenant read the rollout")
	}
	if _, _, err := h.svc.ListRollouts(octx, p, "production", pagination.Page{Size: 50}); err == nil {
		t.Error("another tenant listed the rollouts")
	}
	if _, err := h.svc.AbortRollout(octx, p, "production", ro.ID, nil); err == nil {
		t.Error("another tenant aborted the rollout")
	}
	// Row-level security, below the application: the other tenant's
	// scope sees no row at all.
	var n int
	err = db.NewUnitOfWork(env.App).InTenantTx(tenancy.ContextWithTenant(context.Background(), other),
		func(ctx context.Context, tx *db.TenantTx) error {
			return tx.QueryRow(ctx, "SELECT count(*) FROM release_rollouts").Scan(&n)
		})
	if err != nil || n != 0 {
		t.Errorf("another tenant's scope sees %d rollouts (%v)", n, err)
	}
	if got, err := h.svc.GetRollout(h.as("developer"), p, ro.ID); err != nil || !got.Active() {
		t.Errorf("the rollout after the other tenant's attempts: %+v, %v", got, err)
	}
}

func TestTheSweepAbortsAnExpiredRollout(t *testing.T) {
	h := newHarness(t)
	p, v1, v2 := h.rolloutProject(t)
	ctx := h.as("developer")
	ro, _, err := h.svc.StartRollout(ctx, p, "production", app.RolloutInput{Release: v2.ID, Percent: 10, MaxDuration: time.Hour}, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	sweeper := app.NewRolloutSweeper(h.svc, h.scanner, metrics.New(reg))

	h.clock.Advance(59 * time.Minute)
	if n, err := sweeper.Sweep(context.Background()); err != nil || n != 0 {
		t.Fatalf("sweep before max_duration: %d, %v", n, err)
	}
	if got := gauge(t, reg); got != 1 {
		t.Errorf("active rollouts gauge %v, want 1", got)
	}
	h.clock.Advance(2 * time.Minute)
	if n, err := sweeper.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("sweep after max_duration: %d, %v", n, err)
	}
	got, err := h.svc.GetRollout(ctx, p, ro.ID)
	if err != nil || got.Status != domain.RolloutAborted || got.End != domain.EndExpired {
		t.Fatalf("after the sweep: %+v, %v", got, err)
	}
	// The sweep acts as its own system principal, never as a person.
	if got.EndedBy != identitydomain.SystemActor("release.rollout_sweeper").String() {
		t.Errorf("ended by %q", got.EndedBy)
	}
	if m, _ := h.served(t, p, "production"); m.Release.ID != v1.ID.String() || m.Rollout != nil {
		t.Errorf("after the sweep the manifest serves %s, rollout %+v", m.Release.ID, m.Rollout)
	}
	if got := gauge(t, reg); got != 0 {
		t.Errorf("active rollouts gauge %v, want 0", got)
	}
	if n, err := sweeper.Sweep(context.Background()); err != nil || n != 0 {
		t.Errorf("a second sweep: %d, %v", n, err)
	}
	events := rolloutEvents(t, p)
	if last := events[len(events)-1]; last != "release.rollout.aborted "+got.EndedBy+" expired" {
		t.Errorf("last event %q", last)
	}
}

// gauge reads glossa_release_rollouts{state="active"} from reg.
func gauge(t *testing.T, reg *prometheus.Registry) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != "glossa_release_rollouts" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "state" && l.GetValue() == "active" {
					return m.GetGauge().GetValue()
				}
			}
		}
	}
	t.Fatal("glossa_release_rollouts{state=\"active\"} was never set")
	return 0
}

// A rollout puts a release in front of real installations, so into an
// environment that requires release approvals it is refused until a
// release request can carry one (RFC 0006 §5.2) — never started
// unapproved. The two slices were built apart, and before they met
// this check read no approval at all.
func TestARolloutIntoAnApprovalEnvironmentIsRefused(t *testing.T) {
	h := newHarness(t)
	p, _, v2 := h.rolloutProject(t)
	h.requireApproval(t, p, "production", 2)
	before := h.current(t, p, "production")
	if _, _, err := h.svc.StartRollout(h.as("developer"), p, "production", app.RolloutInput{Release: v2.ID, Percent: 10}, ""); !errors.Is(err, domain.ErrRolloutNeedsApproval) {
		t.Fatalf("starting a rollout into an environment that needs approval: err = %v, want ErrRolloutNeedsApproval", err)
	}
	if rs, _, err := h.svc.ListRollouts(h.owner(), p, "production", pagination.Page{Size: 50}); err != nil || len(rs) != 0 {
		t.Fatalf("rollouts = %+v, %v; want none", rs, err)
	}
	if after := h.current(t, p, "production"); after != before {
		t.Fatal("the refused rollout moved the pointer")
	}
}
