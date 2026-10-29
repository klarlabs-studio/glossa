package remote

import (
	"reflect"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// A finding crosses the wire whole. Every pre-M4 conversion dropped
// something on the way — the span, the subject, the file and the line —
// and the point of one shape is that this one cannot.
func TestAFindingCrossesTheWireWhole(t *testing.T) {
	t.Parallel()
	to, rev := 28, 7
	got := FindingFromWire(apiclient.Finding{
		Schema: "glossa.finding/v1", Fingerprint: "f_7c1a3e9b40d2f815",
		Layer: "length", Code: "expansion-excessive", Severity: "warning",
		Message: "74 % longer", Subject: ptr("Payer"), Detail: ptr("ratio"),
		Evidence: ptr(map[string]any{"ratio": 1.74}), SourceRevision: &rev, Waiver: ptr("wv_1"),
		Fix: &apiclient.FindingFix{Kind: "shorten", To: &to, Term: ptr("cn_1"), Hint: ptr("Payer maintenant")},
		Locus: apiclient.FindingLocus{
			Message: ptr("msg_1"), Key: ptr("checkout.pay"), Locale: ptr("fr"), Revision: ptr("rv_1"),
			Namespace: ptr("checkout"), File: ptr("src/Pay.vue"), Line: ptr(42), Column: ptr(7),
			Route: ptr("/checkout"), Component: ptr("PaymentFooter"), Capture: ptr("cap_1"), Region: ptr("r_18"),
			Span: &apiclient.FindingSpan{Side: "target", Start: 12, End: 19},
		},
	})
	want := domain.Finding{
		Schema: "glossa.finding/v1", Fingerprint: "f_7c1a3e9b40d2f815",
		Layer: domain.LayerLength, Code: "expansion-excessive", Severity: domain.Warning,
		Message: "74 % longer", Subject: "Payer", Detail: "ratio",
		Evidence: map[string]any{"ratio": 1.74}, SourceRevision: &rev, Waiver: "wv_1",
		Fix: &domain.Fix{Kind: domain.FixShorten, To: &to, Term: "cn_1", Hint: "Payer maintenant"},
		Locus: domain.Locus{
			Message: "msg_1", Key: "checkout.pay", Locale: "fr", Revision: "rv_1", Namespace: "checkout",
			File: "src/Pay.vue", Line: 42, Column: 7, Route: "/checkout", Component: "PaymentFooter",
			Capture: "cap_1", Region: "r_18",
			Span: &domain.Span{Side: domain.SideTarget, Start: 12, End: 19},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("finding = %+v\nwant     %+v", got, want)
	}
}

// The fingerprint is carried over, never recomputed. The two values are
// the same — that is wave 4's property — but a reader that recomputed
// would turn it into an assumption, and a waiver is addressed by the
// number the server stored.
func TestAFindingKeepsTheServersFingerprint(t *testing.T) {
	t.Parallel()
	got := FindingFromWire(apiclient.Finding{
		Fingerprint: "f_notwhatwewouldcompute", Layer: "structure", Code: "invalid-message",
		Severity: "error", Locus: apiclient.FindingLocus{Key: ptr("a.b"), Locale: ptr("de")},
	})
	if got.Fingerprint != "f_notwhatwewouldcompute" {
		t.Errorf("fingerprint = %q, want the server's verbatim", got.Fingerprint)
	}
}

// A policy document goes out the way it came in. require_complete is
// the field worth pinning in both directions: nil is every locale and
// an empty slice is none, and they are opposite policies.
func TestAPolicyDocumentRoundTrips(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		required []string
		wantReq  apiclient.CheckPolicyLocaleRequirement
		wantLen  int
	}{
		{"every locale", nil, apiclient.CheckPolicyLocaleRequirementAll, 0},
		{"no locale", []string{}, apiclient.CheckPolicyLocaleRequirementListed, 0},
		{"the named ones", []string{"de", "en"}, apiclient.CheckPolicyLocaleRequirementListed, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wire := documentToWire(checkpolicy.Policy{RequireComplete: tc.required})
			if wire.RequireComplete != tc.wantReq || len(*wire.Locales) != tc.wantLen {
				t.Fatalf("wire = %v %v", wire.RequireComplete, *wire.Locales)
			}
			back := documentFromWire(wire)
			if (back.RequireComplete == nil) != (tc.required == nil) || len(back.RequireComplete) != tc.wantLen {
				t.Errorf("back = %#v, want %#v", back.RequireComplete, tc.required)
			}
		})
	}
}

func TestAPolicysRulesAndEnvironmentsRoundTrip(t *testing.T) {
	t.Parallel()
	in := checkpolicy.Policy{
		FailOn: checkpolicy.Warning, MissingTranslations: checkpolicy.Warning,
		Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "visual"}, Severity: checkpolicy.Warning, Mode: checkpolicy.ModeWarn},
			{Selector: checkpolicy.Selector{Layer: "terminology", Code: "term_forbidden", Locale: "de",
				Namespace: "legal", Environment: "production"}, Severity: checkpolicy.Error},
		},
		Environments: map[string]checkpolicy.Environment{
			"production": {RequireComplete: checkpolicy.RequiredLocales("de", "en"), RequireReview: "approved"},
			// This block names only a review state, so its
			// require_complete must stay unsaid — not become "every
			// locale" on the way out and back.
			"staging": {RequireReview: "approved"},
			"preview": {RequireComplete: checkpolicy.AllLocales()},
		},
	}
	back := documentFromWire(documentToWire(in))
	if len(back.Rules) != 2 || !reflect.DeepEqual(back.Rules, in.Rules) {
		t.Fatalf("rules = %+v", back.Rules)
	}
	for name, want := range in.Environments {
		if got := back.Environments[name]; !got.Equal(want) {
			t.Errorf("environments.%s = %+v, want %+v", name, got, want)
		}
	}
	if back.Environments["staging"].RequireComplete.Set {
		t.Error("a block that said nothing came back saying something")
	}
}

// A write sends the document and only the document: the version, when
// it took effect and what it pins are the server's to assign.
func TestAPolicyWriteCarriesNoBookkeeping(t *testing.T) {
	t.Parallel()
	wire := documentToWire(checkpolicy.Policy{Version: 7, Schema: checkpolicy.Schema})
	if wire.Schema != nil {
		t.Errorf("schema = %v, want the server's own", *wire.Schema)
	}
	back := documentFromWire(wire)
	if back.Version != 0 || back.EffectiveFrom != nil || back.GraceUntil != nil || back.Previous != nil {
		t.Errorf("back = %+v", back)
	}
}
