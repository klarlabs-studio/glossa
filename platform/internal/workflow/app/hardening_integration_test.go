//go:build integration

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// RFC 0006 §13 wave 6 on Postgres: rebase, the retention of finished
// instances, and the limits of §9.6, against migration 0056's grants
// and policy under forced RLS.

// waitForReviewV2 keeps awaiting_review and adds a second review.
const waitForReviewV2 = `{
  "schema": "glossa.workflow/v1", "name": "wait-for-review", "subject": "translation",
  "chart": { "id": "wait-for-review", "initial": "awaiting_review", "states": {
    "awaiting_review": { "type": "atomic", "transitions": [{ "event": "translation.reviewed", "target": "second_review" }] },
    "second_review": { "type": "atomic", "transitions": [{ "event": "translation.reviewed", "target": "reviewed" }] },
    "reviewed": { "type": "final" } } },
  "guards": {}, "actions": {}
}`

// waitForReviewV3 has no awaiting_review.
const waitForReviewV3 = `{
  "schema": "glossa.workflow/v1", "name": "wait-for-review", "subject": "translation",
  "chart": { "id": "wait-for-review", "initial": "second_review", "states": {
    "second_review": { "type": "atomic", "transitions": [{ "event": "translation.reviewed", "target": "reviewed" }] },
    "reviewed": { "type": "final" } } },
  "guards": {}, "actions": {}
}`

func TestRebaseInPostgres(t *testing.T) {
	h := newRunHarness(t)
	project, _ := h.project(t)
	h.bindDoc(t, project, waitForReview)
	translator, _ := h.as([]string{"translator"}, "de")
	h.translate(t, translator, project, "Willkommen")
	h.drain(t)
	instances := h.list(t, project)
	if len(instances) != 1 || instances[0].State != "awaiting_review" || instances[0].Version != 1 {
		t.Fatalf("instances = %+v", instances)
	}
	inst := instances[0]
	manager := h.manager(t)
	if _, err := h.defs.SaveVersion(manager, inst.Definition, 1, []byte(waitForReviewV2)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.defs.SaveVersion(manager, inst.Definition, 2, []byte(waitForReviewV3)); err != nil {
		t.Fatal(err)
	}

	// The latest lacks the state: refused, and nothing changed.
	if _, err := h.runner.Rebase(manager, app.RebaseInput{Project: project, Instance: inst.ID, IfVersion: 1}); !errors.Is(err, domain.ErrRebaseStateMissing) {
		t.Fatalf("onto v3: err = %v", err)
	}
	if got := h.list(t, project)[0]; got.Version != 1 || len(h.log(t, inst.ID)) != 1 {
		t.Fatalf("a refused rebase changed the instance: %+v", got)
	}

	got, err := h.runner.Rebase(manager, app.RebaseInput{Project: project, Instance: inst.ID, IfVersion: 1, Version: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || got.State != "awaiting_review" {
		t.Fatalf("rebased %+v", got)
	}
	if _, err := h.runner.Rebase(manager, app.RebaseInput{Project: project, Instance: inst.ID, IfVersion: 1, Version: 2}); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("a second rebase from version 1: err = %v, want ErrConflict", err)
	}
	who, err := authz.EventActor(manager)
	if err != nil {
		t.Fatal(err)
	}
	log := h.log(t, inst.ID)
	last := log[len(log)-1]
	if last.Event != string(app.TransitionRebase) || last.Outcome != app.TransitionApplied || last.Actor != who.String() ||
		len(last.Actions) != 1 || last.Actions[0].Detail != "version 1 → 2" {
		t.Errorf("log entry %+v", last)
	}
	var typ, actor string
	if err := env.Super.QueryRow(context.Background(),
		"SELECT event_type, actor FROM outbox_events WHERE id = $1", last.OutboxEventID).Scan(&typ, &actor); err != nil {
		t.Fatal(err)
	}
	if typ != domain.EventInstanceRebased || actor != who.String() {
		t.Errorf("the rebase's event is %s by %s", typ, actor)
	}

	// It runs on version 2: one review is no longer enough.
	reviewer, _ := h.as([]string{"reviewer"}, "de")
	cur, err := h.localization.GetTranslation(reviewer, project, "home.title", "de")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.localization.ReviewTranslation(reviewer, project, "home.title", "de", "approved", cur.Revision); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	if got := h.list(t, project)[0]; got.State != "second_review" || got.Status != domain.StatusActive {
		t.Errorf("after one review on v2: %+v", got)
	}
}

func TestRetentionInPostgres(t *testing.T) {
	h := newRunHarness(t)
	project, _ := h.project(t)
	h.bindDoc(t, project, waitForReview)
	translator, _ := h.as([]string{"translator"}, "de")
	reviewer, _ := h.as([]string{"reviewer"}, "de")
	tr := h.translate(t, translator, project, "Willkommen")
	h.drain(t)
	if _, err := h.localization.ReviewTranslation(reviewer, project, "home.title", "de", "approved", tr.Revision); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	// A second round: a new revision starts a new instance, which stays
	// running.
	h.translate(t, translator, project, "Herzlich willkommen")
	h.drain(t)
	var finished, running app.InstanceView
	for _, i := range h.list(t, project) {
		if i.Status == domain.StatusFinished {
			finished = i
		} else {
			running = i
		}
	}
	if finished.ID == uuid.Nil || running.ID == uuid.Nil {
		t.Fatalf("instances = %+v", h.list(t, project))
	}

	// The application role cannot delete a running instance, nor a
	// transition, whatever it asks.
	err := db.NewUnitOfWork(env.App).InTenantTx(h.ctx(), func(ctx context.Context, tx *db.TenantTx) error {
		tag, err := tx.Exec(ctx, "DELETE FROM workflow_instances WHERE status = 'active'")
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 0 {
			t.Errorf("the application role deleted %d running instances", tag.RowsAffected())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.NewUnitOfWork(env.App).InTenantTx(h.ctx(), func(ctx context.Context, tx *db.TenantTx) error {
		_, err := tx.Exec(ctx, "DELETE FROM workflow_transitions")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("deleting a transition: err = %v, want permission denied", err)
	}

	keep := 30 * 24 * time.Hour
	at := time.Now().UTC()
	sweeper := func(now time.Time) *app.Runner {
		return app.NewRunner(app.RunnerDeps{Tx: h.instances, Retention: h.instances, Now: func() time.Time { return now }})
	}
	if n, err := sweeper(at.Add(keep-time.Hour)).SweepRetention(context.Background(), keep); err != nil || n != 0 {
		t.Fatalf("swept %d before the age (%v)", n, err)
	}
	n, err := sweeper(at.Add(keep+time.Hour)).SweepRetention(context.Background(), keep)
	if err != nil || n != 1 {
		t.Fatalf("swept %d, want 1 (%v)", n, err)
	}
	left := h.list(t, project)
	if len(left) != 1 || left[0].ID != running.ID {
		t.Fatalf("left %+v; only the running instance stays", left)
	}
	var logs int
	if err := env.Super.QueryRow(context.Background(),
		"SELECT count(*) FROM workflow_transitions WHERE instance_id = $1", finished.ID).Scan(&logs); err != nil || logs != 0 {
		t.Errorf("the finished instance's log has %d rows left (%v)", logs, err)
	}
	if len(h.log(t, running.ID)) == 0 {
		t.Error("the running instance's log went too")
	}
}

func TestTheLimitsInPostgres(t *testing.T) {
	t.Run("50 live definitions per tenant", func(t *testing.T) {
		h := newHarness(t)
		for i := range domain.MaxActiveDefinitions {
			doc := strings.Replace(waitForReview, `"name": "wait-for-review"`, `"name": "flow-`+string(rune('a'+i/26))+string(rune('a'+i%26))+`"`, 1)
			if _, err := h.svc.CreateDefinition(h.manager(t), app.NewDefinition{Document: []byte(doc)}); err != nil {
				t.Fatalf("definition %d: %v", i+1, err)
			}
		}
		_, err := h.svc.CreateDefinition(h.manager(t), app.NewDefinition{Document: []byte(waitForReview)})
		if !errors.Is(err, app.ErrLimit) {
			t.Fatalf("the 51st: err = %v, want ErrLimit", err)
		}
	})
	t.Run("1000 open assignments per assignee", func(t *testing.T) {
		if err := env.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		tenant := mustTenant(t, "limits")
		h := newWorkHarness(t, tenant)
		project := uuid.New()
		// 999 open and one done, inserted directly: the limit counts
		// the live ones.
		if _, err := env.Super.Exec(context.Background(), `
			INSERT INTO workflow_assignments (id, tenant_id, project_id, assignee, permission, state, version,
			                                  created_by, created_at, updated_at, closed_by, closed_at)
			SELECT gen_random_uuid(), $1, $2, 'role:reviewer', 'translations.write',
			       CASE WHEN g < $3 THEN 'open' ELSE 'done' END, 1, 'test', now(), now(),
			       CASE WHEN g < $3 THEN NULL ELSE 'test' END, CASE WHEN g < $3 THEN NULL ELSE now() END
			FROM generate_series(1, $3) g`, uuid.UUID(tenant), project, domain.MaxOpenAssignments); err != nil {
			t.Fatal(err)
		}
		to := domain.Party{Role: "reviewer"}
		h.assign(project, to, unitIn("de")) // the 1000th
		_, _, err := h.svc.Assign(h.manager(), app.AssignInput{ProjectID: project, Units: []domain.Unit{unitIn("de")}, To: to})
		if !errors.Is(err, app.ErrLimit) {
			t.Fatalf("the 1001st: err = %v, want ErrLimit", err)
		}
		h.assign(project, domain.Party{Role: "translator"}, unitIn("de")) // another assignee
	})
}
