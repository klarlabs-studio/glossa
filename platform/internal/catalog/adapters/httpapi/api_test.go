package httpapi

import (
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func t0() time.Time { return time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC) }

func en(t *testing.T) bcp47.Tag {
	t.Helper()
	tag, err := bcp47.Parse("en")
	if err != nil {
		t.Fatal(err)
	}
	return tag
}

// The wire spells out what the domain keeps implicit: nil
// RequireComplete is "all" and an empty one is "none". A round trip has
// to preserve the difference — they are opposites.
func TestCheckPolicyRoundTripsThroughTheWire(t *testing.T) {
	tests := []struct {
		name    string
		policy  checkpolicy.Policy
		mode    apiv1.CheckPolicyRequireComplete
		locales []string
	}{
		{name: "the default", mode: apiv1.CheckPolicyRequireCompleteAll},
		{
			name:   "no locale has to be complete",
			policy: checkpolicy.Policy{RequireComplete: []string{}},
			mode:   apiv1.CheckPolicyRequireCompleteNone,
		},
		{
			name: "a named list",
			policy: checkpolicy.Policy{RequireComplete: []string{"de", "fr"},
				FailOn: checkpolicy.Warning, MissingTranslations: checkpolicy.Warning},
			mode: apiv1.CheckPolicyRequireCompleteListed, locales: []string{"de", "fr"},
		},
		{
			name:   "nothing fails the check",
			policy: checkpolicy.Policy{FailOn: checkpolicy.Never},
			mode:   apiv1.CheckPolicyRequireCompleteAll,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wire := toCheckPolicy(tc.policy)
			if wire.RequireComplete != tc.mode {
				t.Fatalf("require_complete = %q, want %q", wire.RequireComplete, tc.mode)
			}
			if wire.Locales == nil || len(*wire.Locales) != len(tc.locales) {
				t.Fatalf("locales = %v, want %v", wire.Locales, tc.locales)
			}
			// Both severities are always spelled out in a response.
			if wire.FailOn == "" || wire.MissingTranslations == "" {
				t.Fatalf("policy = %+v, want both severities named", wire)
			}
			back, err := fromCheckPolicy(&wire)
			if err != nil {
				t.Fatal(err)
			}
			want, err := tc.policy.Validate(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !back.Equal(want) || (back.RequireComplete == nil) != (want.RequireComplete == nil) {
				t.Fatalf("round trip = %+v, want %+v", *back, want)
			}
		})
	}
}

func TestCheckPolicyAbsentFromASettingsWriteKeepsTheProjects(t *testing.T) {
	s, err := fromSettings(&apiv1.ProjectSettings{DefaultSyntax: apiv1.Mf1, ReviewRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.CheckPolicy != nil {
		t.Fatalf("check_policy = %+v, want nil so the project keeps its own", s.CheckPolicy)
	}
	// And the project's is what a change to other settings leaves in place.
	p, err := domain.NewProject(tenancy.NewID(), "shop", "Shop", en(t), domain.Settings{
		DefaultSyntax: mfcontent.MF1,
		CheckPolicy:   &checkpolicy.Policy{RequireComplete: []string{}},
	}, t0())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Change(domain.ProjectChange{Settings: s}, nil, t0()); err != nil {
		t.Fatal(err)
	}
	if p.Settings.Policy().Requires("de") {
		t.Errorf("the write dropped the project's policy: %+v", p.Settings.Policy())
	}
}

func TestCheckPolicyRejectsARequireCompleteThatIsNotOne(t *testing.T) {
	_, err := fromCheckPolicy(&apiv1.CheckPolicy{RequireComplete: "some"})
	var d *problem.Details
	if !errors.As(err, &d) || d.Code != "invalid_check_policy" || d.Status != 400 {
		t.Fatalf("err = %v, want a 400 invalid_check_policy", err)
	}
	// A locale that is not a language tag is an invalid_locale, as
	// everywhere else in the contract.
	_, err = fromCheckPolicy(&apiv1.CheckPolicy{
		RequireComplete: apiv1.CheckPolicyRequireCompleteListed, Locales: &[]apiv1.Locale{"not a tag"},
	})
	if !errors.As(err, &d) || d.Code != "invalid_locale" {
		t.Fatalf("err = %v, want invalid_locale", err)
	}
}
