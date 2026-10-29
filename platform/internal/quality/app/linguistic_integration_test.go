//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Linguistic-QA jobs against Postgres (migration 0038, RFC 0005 §3.8).
// The unit tests in linguistic_test.go ask what the service decides;
// these ask what the table actually holds — that a job round-trips with
// its scope, that a settled job cannot be moved again, and that another
// tenant cannot see one.
//
// Nothing here calls an AI provider: the reviewer is a fake behind the
// Linguist port, because the model call is Intelligence's.

// pgLinguist is a reviewer that finishes immediately with whatever a
// test gave it.
type pgLinguist struct {
	pre      app.LinguisticPreflight
	progress app.LinguisticProgress
}

func (l *pgLinguist) Preflight(context.Context, uuid.UUID) (app.LinguisticPreflight, error) {
	return l.pre, nil
}

func (l *pgLinguist) Start(context.Context, app.LinguisticRequest) (string, error) {
	return "batch-pg", nil
}

func (l *pgLinguist) Poll(context.Context, uuid.UUID, string) (app.LinguisticProgress, error) {
	return l.progress, nil
}

func (l *pgLinguist) Cancel(context.Context, uuid.UUID, string) error { return nil }

func consenting() app.LinguisticPreflight {
	return app.LinguisticPreflight{ProviderConsent: true, BudgetRemaining: 1_000_000}
}

func review(locales ...string) app.CreateLinguisticJob {
	return app.CreateLinguisticJob{
		Ref: "main",
		Scope: domain.LinguisticScope{
			Locales: locales, Namespace: "checkout", KeyPrefix: "checkout.", Keys: []string{"checkout.pay"},
		},
	}
}

// TestLinguisticJobRoundTripsThroughTheTable: the row holds the scope,
// the counts and the run, and a read brings a finished review's
// findings in as ordinary linguistic findings — at `warning`, which is
// the only severity the layer has.
func TestLinguisticJobRoundTripsThroughTheTable(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	h.svc.SetLinguist(&pgLinguist{pre: consenting(), progress: app.LinguisticProgress{
		Done: true, Reviewed: 4, SkippedSensitive: 2,
		Findings: []domain.LinguisticFinding{{
			Code: "meaning-divergence", Key: "checkout.pay", Locale: "de", Namespace: "checkout",
			Explanation: "The target promises a refund the source does not.",
			Subject:     "checkout.pay", Suggestion: "Jetzt bezahlen",
		}},
	}})

	job, created, err := h.svc.RequestLinguisticReview(h.developer(), project, review("de"))
	if err != nil || !created {
		t.Fatalf("request: %v, created %v", err, created)
	}
	if job.State != domain.LinguisticRunning || job.Batch != "batch-pg" {
		t.Fatalf("job = %s/%q, want running with the batch handle", job.State, job.Batch)
	}

	done, err := h.svc.GetLinguisticJob(h.developer(), project, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case done.State != domain.LinguisticSucceeded:
		t.Fatalf("state = %s, want succeeded", done.State)
	case done.CheckRun == nil:
		t.Fatal("a succeeded job names no check run")
	case done.Findings != 1 || done.Reviewed != 4 || done.SkippedSensitive != 2:
		t.Errorf("counts = %d/%d/%d, want 1 finding, 4 reviewed, 2 skipped",
			done.Findings, done.Reviewed, done.SkippedSensitive)
	case done.Scope.Namespace != "checkout" || done.Scope.KeyPrefix != "checkout.":
		t.Errorf("scope = %+v, want the one asked for", done.Scope)
	}

	got := list(t, h, project, app.FindingQuery{Run: *done.CheckRun})
	if len(got.Items) != 1 {
		t.Fatalf("the run holds %d findings, want 1", len(got.Items))
	}
	f := got.Items[0].Finding
	if f.Layer != domain.LayerLinguistic || f.Severity != domain.Warning {
		t.Errorf("finding = %s/%s, want linguistic/warning", f.Layer, f.Severity)
	}
	if f.Fix == nil || f.Fix.Hint != "Jetzt bezahlen" {
		t.Errorf("fix = %+v, want the model's suggestion as a hint", f.Fix)
	}
	if got.Counts.Errors != 0 {
		t.Errorf("the run counted %d errors; a build never fails on an opinion", got.Counts.Errors)
	}
}

// TestASettledJobIsNeverMovedAgain: the UPDATE is guarded on the state,
// so a late poll of a cancelled job cannot resurrect it.
func TestASettledJobIsNeverMovedAgain(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	h.svc.SetLinguist(&pgLinguist{pre: consenting(), progress: app.LinguisticProgress{Done: true}})

	job, _, err := h.svc.RequestLinguisticReview(h.developer(), project, review("de"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CancelLinguisticJob(h.developer(), project, job.ID); err != nil {
		t.Fatal(err)
	}
	read, err := h.svc.GetLinguisticJob(h.developer(), project, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.State != domain.LinguisticCancelled {
		t.Fatalf("state = %s, want it to stay cancelled", read.State)
	}
	if _, err := h.svc.CancelLinguisticJob(h.developer(), project, job.ID); !errors.Is(
		err, domain.ErrLinguisticJobNotCancellable,
	) {
		t.Errorf("cancelling it again = %v, want ErrLinguisticJobNotCancellable", err)
	}
}

// TestAJobIsTenantData: findings can contain product copy, so the rows
// that produce them are tenant data under forced RLS (RFC 0005 §10).
// Another tenant's job is simply not there.
func TestAJobIsTenantData(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	h.svc.SetLinguist(&pgLinguist{pre: consenting()})
	job, _, err := h.svc.RequestLinguisticReview(h.developer(), project, review("de"))
	if err != nil {
		t.Fatal(err)
	}

	other := harnessFor(t, "globex")
	other.svc.SetLinguist(&pgLinguist{pre: consenting()})
	if _, err := other.svc.GetLinguisticJob(other.developer(), project, job.ID); err == nil {
		t.Fatal("another tenant read the job")
	}
	jobs, _, err := h.svc.ListLinguisticJobs(h.developer(), project, "running",
		pagination.Page{Size: pagination.DefaultPageSize})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("list = %d jobs, want the one this tenant owns", len(jobs))
	}
}

// TestAnEmptyReviewRecordsNoRun: a review that found nothing stores
// nothing, and a job with no findings names no run. "A model looked and
// found nothing" is a succeeded job with zero findings, which is a
// different statement from a run full of clean layers.
func TestAnEmptyReviewRecordsNoRun(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	h.svc.SetLinguist(&pgLinguist{pre: consenting(), progress: app.LinguisticProgress{Done: true, Reviewed: 9}})

	job, _, err := h.svc.RequestLinguisticReview(h.developer(), project, review("de"))
	if err != nil {
		t.Fatal(err)
	}
	done, err := h.svc.GetLinguisticJob(h.developer(), project, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.State != domain.LinguisticSucceeded || done.CheckRun != nil || done.Findings != 0 {
		t.Fatalf("job = %+v, want succeeded with no run and no findings", done)
	}
	if done.Reviewed != 9 {
		t.Errorf("reviewed = %d, want 9: the model did look", done.Reviewed)
	}
}
