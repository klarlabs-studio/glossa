package httpapi

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// TestLinguisticErrorsMapToTheDocumentedCodes: the codes in
// api/openapi.yaml are the contract, so a rename here is a breaking
// change and this is where it is caught.
func TestLinguisticErrorsMapToTheDocumentedCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   problem.Code
	}{
		{app.ErrLinguisticJobNotFound, 404, "not_found"},
		{app.ErrLinguistUnavailable, 503, "linguistic_unavailable"},
		{app.ErrLinguisticLayerOff, 409, "linguistic_layer_off"},
		{domain.ErrLinguisticJobNotCancellable, 409, "linguistic_job_not_cancellable"},
		{domain.ErrInvalidLinguisticScope, 400, "invalid_linguistic_scope"},
		{app.ErrIdempotencyReuse, 422, "idempotency_key_reused"},
		{idempotency.ErrInvalidKey, 400, "invalid_request"},
	}
	for _, tc := range cases {
		var d *problem.Details
		if !errors.As(mapError(tc.err), &d) || d.Status != tc.status || d.Code != tc.code {
			t.Errorf("mapError(%v) = %v, want %d %s", tc.err, d, tc.status, tc.code)
		}
	}
}

// TestJobRendersWhatIsThereAndNothingElse: an absent member stays
// absent, so a client can tell "no check run yet" from "check run zero"
// and "still running" from "failed with no reason".
func TestJobRendersWhatIsThereAndNothingElse(t *testing.T) {
	id, run := uuid.New(), uuid.New()
	at := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)

	running := toLinguisticJob(domain.LinguisticJob{
		ID: id, Ref: "main", State: domain.LinguisticRunning, CreatedBy: "token:1",
		Scope:     domain.LinguisticScope{Locales: []string{"de"}},
		CreatedAt: at, UpdatedAt: at, StartedAt: &at,
	})
	switch {
	case running.CheckRun != nil:
		t.Error("a running job names a check run")
	case running.FailureCode != nil || running.FailureDetail != nil:
		t.Error("a running job carries a failure")
	case running.FinishedAt != nil:
		t.Error("a running job has finished")
	case running.Scope.Namespace != nil || running.Scope.KeyPrefix != nil || running.Scope.Keys != nil:
		t.Errorf("scope = %+v, want only the locales it named", running.Scope)
	}

	failed := toLinguisticJob(domain.LinguisticJob{
		ID: id, Ref: "main", State: domain.LinguisticFailed,
		Scope:       domain.LinguisticScope{Locales: []string{"de"}, Namespace: "legal"},
		FailureCode: domain.LinguisticFailureSensitive, LastError: "legal is sensitive",
		CreatedAt: at, UpdatedAt: at, FinishedAt: &at,
	})
	if failed.FailureCode == nil || *failed.FailureCode != apiv1.LinguisticFailureCode("sensitive") {
		t.Errorf("failure_code = %v, want sensitive", failed.FailureCode)
	}
	if failed.FailureDetail == nil || *failed.FailureDetail == "" {
		t.Error("a failure with no sentence saying what to change")
	}

	done := toLinguisticJob(domain.LinguisticJob{
		ID: id, Ref: "main", State: domain.LinguisticSucceeded, CheckRun: &run, Findings: 2,
		Scope:     domain.LinguisticScope{Locales: []string{"de"}, Keys: []string{"checkout.pay"}},
		CreatedAt: at, UpdatedAt: at, FinishedAt: &at,
	})
	if done.CheckRun == nil || *done.CheckRun != run.String() {
		t.Errorf("check_run = %v, want the run its findings are in", done.CheckRun)
	}
	if done.Scope.Keys == nil || len(*done.Scope.Keys) != 1 {
		t.Errorf("scope.keys = %v, want the key it named", done.Scope.Keys)
	}
}

// TestTheLayerAndItsCodesAreTheContract: the four codes of RFC 0005
// §3.8 and the one severity they may carry are what Studio, the CLI and
// the pull-request check read. A code added here without the schema
// knowing it would be a finding nothing can render.
func TestTheLayerAndItsCodesAreTheContract(t *testing.T) {
	if !domain.LinguisticAdvisory() {
		t.Fatal("the kernel no longer calls `linguistic` advisory")
	}
	for _, code := range domain.LinguisticCodes {
		f, err := domain.SealLinguistic(domain.LinguisticFinding{
			Code: code, Key: "checkout.pay", Locale: "de", Explanation: "…",
		})
		if err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		out := toFinding(f)
		if out.Layer != apiv1.FindingLayer(domain.LayerLinguistic) {
			t.Errorf("%s: layer = %s", code, out.Layer)
		}
		if out.Severity != apiv1.FindingSeverity(domain.Warning) {
			t.Errorf("%s: severity = %s, want warning", code, out.Severity)
		}
	}
}
