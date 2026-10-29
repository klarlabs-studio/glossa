package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// HasReportedRun is what the Glossa pull-request check asks to decide
// whether a commit without a run yet is worth waiting for: has this
// project's CI ever recorded one (RFC 0005 §12.3, §14 decision 11)?

// TestHasReportedRunCountsOnlyReportedTriggers: a capture upload and
// the write-time job record runs of their own, and neither says the
// project's CI runs `glossa check`. Only the reportable triggers do.
func TestHasReportedRunCountsOnlyReportedTriggers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger domain.Trigger
		want    bool
	}{
		{"none", "", false},
		{"capture", domain.TriggerCapture, false},
		{"write", domain.TriggerWrite, false},
		{"cli", domain.TriggerCLI, true},
		{"pull_request", domain.TriggerPullRequest, true},
		{"api", domain.TriggerAPI, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			if tc.trigger != "" {
				store.runs = []domain.CheckRun{{Trigger: tc.trigger}}
			}
			svc, project := serviceFor(store)
			got, err := svc.HasReportedRun(readCtx(t), project)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("HasReportedRun = %v with a %q run, want %v", got, tc.trigger, tc.want)
			}
			if !slices.Equal(store.lastTriggers, app.ReportableTriggers) {
				t.Errorf("asked the store about %v, want the reportable triggers %v",
					store.lastTriggers, app.ReportableTriggers)
			}
		})
	}
}

// TestHasReportedRunNeedsCatalogRead: it reads the project's runs, so
// it needs what reading them needs.
func TestHasReportedRunNeedsCatalogRead(t *testing.T) {
	store := &fakeStore{runs: []domain.CheckRun{{Trigger: domain.TriggerCLI}}}
	svc, project := serviceFor(store)
	if _, err := svc.HasReportedRun(context.Background(), project); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Fatalf("err = %v, want unauthenticated", err)
	}
	if store.lastTriggers != nil {
		t.Error("the store was read before the permission was checked")
	}
}
