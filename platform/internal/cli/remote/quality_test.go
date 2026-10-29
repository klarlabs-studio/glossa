package remote

import (
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

func ptr[T any](v T) *T { return &v }

// require_complete is the one field the wire and the kernel say
// differently, so it is the one worth pinning in all three shapes.
func TestRequireCompleteCrossesTheWire(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		req     apiclient.CheckPolicyLocaleRequirement
		locales *[]apiclient.Locale
		want    []string
	}{
		{"all is every locale, which the kernel says as nil", apiclient.CheckPolicyLocaleRequirementAll, nil, nil},
		{"none is no locale, which is an empty slice and not nil", apiclient.CheckPolicyLocaleRequirementNone, nil, []string{}},
		{
			"listed names exactly those locales",
			apiclient.CheckPolicyLocaleRequirementListed,
			&[]apiclient.Locale{"de-DE", "fr-FR"},
			[]string{"de-DE", "fr-FR"},
		},
		{
			"listed with no locales is none, not every locale",
			apiclient.CheckPolicyLocaleRequirementListed, nil, []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := policyFromWire(apiclient.CheckPolicyState{
				Document: apiclient.CheckPolicyDocument{RequireComplete: tc.req, Locales: tc.locales},
			})
			if tc.want == nil {
				if got.RequireComplete != nil {
					t.Fatalf("require_complete = %#v, want nil (every locale)", got.RequireComplete)
				}
				return
			}
			if got.RequireComplete == nil {
				t.Fatalf("require_complete = nil (every locale), want %#v", tc.want)
			}
			if len(got.RequireComplete) != len(tc.want) {
				t.Fatalf("require_complete = %#v, want %#v", got.RequireComplete, tc.want)
			}
			for i, l := range tc.want {
				if got.RequireComplete[i] != l {
					t.Fatalf("require_complete = %#v, want %#v", got.RequireComplete, tc.want)
				}
			}
		})
	}
}

func TestTheDocumentCrossesTheWireWhole(t *testing.T) {
	t.Parallel()
	effective := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	grace := effective.Add(14 * 24 * time.Hour)
	got := policyFromWire(apiclient.CheckPolicyState{
		Version:       7,
		EffectiveFrom: &effective,
		GraceUntil:    &grace,
		Document: apiclient.CheckPolicyDocument{
			Schema:              ptr(apiclient.CheckPolicyDocumentSchema("glossa.check-policy/v1")),
			FailOn:              apiclient.CheckPolicyDocumentFailOn("warning"),
			MissingTranslations: apiclient.CheckPolicyDocumentMissingTranslations("warning"),
			RequireComplete:     apiclient.CheckPolicyLocaleRequirementAll,
			Rules: &[]apiclient.CheckPolicyRule{{
				Severity:    apiclient.CheckPolicyRuleSeverity("warning"),
				Mode:        ptr(apiclient.CheckPolicyRuleMode("warn")),
				Layer:       ptr(apiclient.FindingLayer("length")),
				Code:        ptr("length.too-long"),
				Locale:      ptr(apiclient.Locale("de-DE")),
				Namespace:   ptr(apiclient.Namespace("checkout")),
				Environment: ptr("production"),
			}},
			Environments: &map[string]apiclient.CheckPolicyEnvironment{
				"production": {
					RequireComplete: ptr(apiclient.CheckPolicyLocaleRequirementListed),
					Locales:         &[]apiclient.Locale{"de-DE"},
					RequireReview:   ptr(apiclient.CheckPolicyEnvironmentRequireReview("approved")),
				},
			},
		},
	})

	if got.Version != 7 {
		t.Errorf("version = %d, want 7", got.Version)
	}
	if got.Schema != "glossa.check-policy/v1" {
		t.Errorf("schema = %q", got.Schema)
	}
	if got.FailOn != checkpolicy.Severity("warning") || got.MissingTranslations != checkpolicy.Severity("warning") {
		t.Errorf("severities = %q / %q", got.FailOn, got.MissingTranslations)
	}
	if got.EffectiveFrom == nil || !got.EffectiveFrom.Equal(effective) {
		t.Errorf("effective_from = %v, want %v", got.EffectiveFrom, effective)
	}
	if got.GraceUntil == nil || !got.GraceUntil.Equal(grace) {
		t.Errorf("grace_until = %v, want %v", got.GraceUntil, grace)
	}

	if len(got.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(got.Rules))
	}
	r := got.Rules[0]
	// Every selector field crosses: a rule that loses one silently
	// widens to messages it was never meant to decide.
	if r.Layer != "length" || r.Code != "length.too-long" || r.Locale != "de-DE" ||
		r.Namespace != "checkout" || r.Environment != "production" {
		t.Errorf("selector = %#v", r.Selector)
	}
	if r.Severity != checkpolicy.Severity("warning") || r.Mode != checkpolicy.Mode("warn") {
		t.Errorf("rule severity/mode = %q / %q", r.Severity, r.Mode)
	}

	env, ok := got.Environments["production"]
	if !ok {
		t.Fatalf("environments = %#v, want a production block", got.Environments)
	}
	if !env.RequireComplete.Equal(checkpolicy.RequiredLocales("de-DE")) {
		t.Errorf("production require_complete = %#v", env.RequireComplete)
	}
	if env.RequireReview != "approved" {
		t.Errorf("production require_review = %q", env.RequireReview)
	}
}

// An environment block that names no require_complete inherits the
// document's. Reading it as "every locale" would quietly make
// production stricter than anyone wrote.
func TestAnEnvironmentInheritsWhatItDoesNotSay(t *testing.T) {
	t.Parallel()
	got := policyFromWire(apiclient.CheckPolicyState{
		Document: apiclient.CheckPolicyDocument{
			RequireComplete: apiclient.CheckPolicyLocaleRequirementNone,
			Environments: &map[string]apiclient.CheckPolicyEnvironment{
				"production": {RequireReview: ptr(apiclient.CheckPolicyEnvironmentRequireReview("approved"))},
			},
		},
	})
	env := got.Environments["production"]
	if env.RequireComplete.Set {
		t.Errorf("production named a require_complete it never wrote: %#v", env.RequireComplete)
	}
	if env.RequireReview != "approved" {
		t.Errorf("require_review = %q, want approved", env.RequireReview)
	}
}
