package httpapi

import (
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// TestPolicyDocumentRoundTrips: export writes what import reads. The
// wire spells `require_complete` as `all`/`listed`/`none` where the
// document has nil and the empty slice, and the three have to survive
// the trip in both directions — `all` and `none` look alike on the wire
// and mean opposite things.
func TestPolicyDocumentRoundTrips(t *testing.T) {
	cases := map[string]checkpolicy.Policy{
		"every locale": {FailOn: checkpolicy.Error, MissingTranslations: checkpolicy.Error},
		"no locale": {
			RequireComplete: []string{}, FailOn: checkpolicy.Never, MissingTranslations: checkpolicy.Warning,
		},
		"two locales": {
			RequireComplete: []string{"de", "en"}, FailOn: checkpolicy.Warning,
			MissingTranslations: checkpolicy.Error,
		},
		"rules and environments": {
			RequireComplete: []string{"de"}, FailOn: checkpolicy.Error, MissingTranslations: checkpolicy.Error,
			Environments: map[string]checkpolicy.Environment{
				"production": {RequireComplete: checkpolicy.RequiredLocales("de", "en"), RequireReview: "approved"},
				"staging":    {RequireComplete: checkpolicy.AllLocales()},
				"preview":    {RequireReview: "approved"},
			},
			Rules: []checkpolicy.Rule{
				{Selector: checkpolicy.Selector{Layer: "source"}, Severity: checkpolicy.Off},
				{
					Selector: checkpolicy.Selector{Layer: "terminology", Namespace: "legal", Environment: "production"},
					Severity: checkpolicy.Error, Mode: checkpolicy.ModeEnforce,
				},
				{
					Selector: checkpolicy.Selector{Layer: "length", Locale: "ja", Code: "expansion-excessive"},
					Severity: checkpolicy.Warning, Mode: checkpolicy.ModeWarn,
				},
			},
		},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := fromPolicyDocument(toPolicyDocument(want))
			if err != nil {
				t.Fatalf("fromPolicyDocument: %v", err)
			}
			got.Schema = want.Schema
			if !got.Equal(want) {
				t.Errorf("round trip = %+v, want %+v", got, want)
			}
		})
	}
}

// TestPolicyDocumentKeepsInheritApart: an environment that named no
// require_complete inherits the document's, which is not the same as
// "every locale" — and the two would be one on the wire if `absent` and
// `all` collapsed.
func TestPolicyDocumentKeepsInheritApart(t *testing.T) {
	doc := toPolicyDocument(checkpolicy.Policy{
		Environments: map[string]checkpolicy.Environment{"preview": {RequireReview: "approved"}},
	})
	envs := *doc.Environments
	if envs["preview"].RequireComplete != nil {
		t.Fatalf("require_complete = %v, want absent: the block did not say", envs["preview"].RequireComplete)
	}
	back, err := fromPolicyDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	if back.Environments["preview"].RequireComplete.Set {
		t.Error("a block that said nothing came back saying something")
	}
}

// TestPolicyDocumentRefusesAnUnknownRequirement: a require_complete
// outside the three modes is refused at the edge, not read as one of
// them — guessing would silently change what must be translated.
func TestPolicyDocumentRefusesAnUnknownRequirement(t *testing.T) {
	_, err := fromPolicyDocument(apiv1.CheckPolicyDocument{RequireComplete: "most"})
	var d *problem.Details
	if !errors.As(err, &d) || d.Status != 400 || d.Code != "invalid_check_policy" {
		t.Errorf("err = %v, want 400 invalid_check_policy", err)
	}
}

// TestPolicyStateRendersTheBookkeeping: a reader has to be able to see
// which version grades, when it started to, and which version an open
// pull request is still pinned to.
func TestPolicyStateRendersTheBookkeeping(t *testing.T) {
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	until := at.Add(14 * 24 * time.Hour)
	state := app.PolicyState{
		Policy: checkpolicy.Policy{
			Schema: checkpolicy.Schema, Version: 7, EffectiveFrom: &at, GraceUntil: &until,
			Previous: &checkpolicy.Policy{Version: 6},
		},
		CreatedBy: "user_1", CreatedAt: at,
	}
	out := toPolicyState(state)
	if out.Version != 7 || out.PinnedVersion == nil || *out.PinnedVersion != 6 {
		t.Errorf("state = %+v, want version 7 pinned to 6", out)
	}
	if out.EffectiveFrom == nil || !out.EffectiveFrom.Equal(at) || out.GraceUntil == nil || !out.GraceUntil.Equal(until) {
		t.Errorf("state = %+v, want the grace spelled out", out)
	}
	if out.CreatedBy == nil || *out.CreatedBy != "user_1" {
		t.Errorf("created_by = %v, want the author", out.CreatedBy)
	}

	// A policy nobody saved through this API says so by leaving the
	// record out, rather than by inventing an author.
	bare := toPolicyState(app.PolicyState{})
	if bare.CreatedBy != nil || bare.CreatedAt != nil || bare.PinnedVersion != nil {
		t.Errorf("state = %+v, want no record for a policy nobody saved", bare)
	}
}

// TestPolicyImpactRendersEveryRule: a rule that changed nothing is
// listed too — it is exactly what a reader wants to know before saving.
func TestPolicyImpactRendersEveryRule(t *testing.T) {
	out := toPolicyImpact(app.Preview{
		Runs: 3,
		ImpactReport: checkpolicy.ImpactReport{
			Targets: 9, Raised: 2, NewlyFailing: 2,
			Rules: []checkpolicy.RuleImpact{
				{
					Rule: 0, Selector: checkpolicy.Selector{Layer: "terminology"},
					Severity: checkpolicy.Error, Mode: checkpolicy.ModeEnforce,
					Matched: 2, Changed: 2, NewlyFailing: 2,
				},
				{Rule: 1, Selector: checkpolicy.Selector{Layer: "visual"}, Severity: checkpolicy.Warning},
			},
		},
		NewlyFailingRefs: []string{"feature/pay", "feature/local"},
		OpenPullRequests: 2,
		PullRequests: []app.PullRequestImpact{
			{Ref: "feature/pay", Number: 41, URL: "https://github.com/acme/shop/pull/41"},
			{Ref: "feature/cart", Number: 42},
		},
	})
	if out.Findings != 9 || out.Runs != 3 || out.NewlyFailing != 2 || out.OpenPullRequests != 2 {
		t.Errorf("impact = %+v, want the counts as measured", out)
	}
	if len(out.Rules) != 2 || out.Rules[1].Matched != 0 {
		t.Fatalf("rules = %+v, want both, including the one that matched nothing", out.Rules)
	}
	if out.Rules[0].Selector == nil || out.Rules[0].Selector.Layer == nil ||
		*out.Rules[0].Selector.Layer != apiv1.FindingLayer(domain.LayerTerminology) {
		t.Errorf("rule 0 selector = %+v, want the terminology layer", out.Rules[0].Selector)
	}
	if got := out.NewlyFailingRefs; got == nil || len(*got) != 2 {
		t.Errorf("newly failing refs = %v, want both refs", got)
	}
	// The pull requests are named, and a link is given only where one
	// is known — never an empty string that a client would render as a
	// link to nowhere.
	prs := out.NewlyFailingPullRequests
	if prs == nil || len(*prs) != 2 {
		t.Fatalf("pull requests = %v, want the two", prs)
	}
	pay, cart := (*prs)[0], (*prs)[1]
	if pay.Ref != "feature/pay" || pay.Number != 41 || pay.Url == nil || *pay.Url != "https://github.com/acme/shop/pull/41" {
		t.Errorf("pull request = %+v", pay)
	}
	if cart.Ref != "feature/cart" || cart.Number != 42 || cart.Url != nil {
		t.Errorf("pull request = %+v, want no url", cart)
	}
	// None, and the field is absent, as the refs are.
	if none := toPolicyImpact(app.Preview{}); none.NewlyFailingPullRequests != nil {
		t.Errorf("no pull requests rendered as %v", *none.NewlyFailingPullRequests)
	}
}

// TestPolicyErrorsMapToTheDocumentedCodes: every way a policy can be
// wrong is one code with a sentence, because a caller fixing a policy
// needs the sentence and not a taxonomy to branch on.
func TestPolicyErrorsMapToTheDocumentedCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   problem.Code
	}{
		{app.ErrPolicyVersionNotFound, 404, "not_found"},
		{app.ErrPolicyConflict, 409, "check_policy_conflict"},
		{domain.ErrPolicyBookkeeping, 400, "invalid_check_policy"},
		{domain.ErrInvalidGrace, 400, "invalid_check_policy"},
		{checkpolicy.ErrUnknownLayer, 400, "invalid_check_policy"},
		{checkpolicy.ErrAdvisoryLayer, 400, "invalid_check_policy"},
		{checkpolicy.ErrUnknownLocale, 400, "invalid_check_policy"},
		{checkpolicy.ErrInvalidDocument, 400, "invalid_check_policy"},
		{checkpolicy.ErrInvalidEnvironment, 400, "invalid_check_policy"},
		{checkpolicy.ErrInvalidMode, 400, "invalid_check_policy"},
		{checkpolicy.ErrInvalidSeverity, 400, "invalid_check_policy"},
	}
	for _, tc := range cases {
		var d *problem.Details
		if !errors.As(mapError(tc.err), &d) || d.Status != tc.status || d.Code != tc.code {
			t.Errorf("mapError(%v) = %v, want %d %s", tc.err, d, tc.status, tc.code)
		}
	}
}

// TestGraceOfReadsDays: absent is the documented default, and zero
// pins nothing.
func TestGraceOfReadsDays(t *testing.T) {
	if got := graceOf(nil); got != nil {
		t.Errorf("graceOf(nil) = %v, want nil so the service applies the default", got)
	}
	zero, seven := 0, 7
	if got := graceOf(&zero); got == nil || *got != 0 {
		t.Errorf("graceOf(0) = %v, want an explicit zero", got)
	}
	if got := graceOf(&seven); got == nil || *got != 7*24*time.Hour {
		t.Errorf("graceOf(7) = %v, want 7 days", got)
	}
}
