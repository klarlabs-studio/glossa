package domain_test

import (
	"math"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

func ptr[T any](v T) *T { return &v }

func TestScore(t *testing.T) {
	cfg := domain.DefaultScoring()
	clean := domain.Signals{Origin: domain.OriginAI, SourceLocale: "en", TargetLocale: "de"}
	tests := []struct {
		name    string
		signals domain.Signals
		want    float64
		factors map[string]float64 // factor → contribution
	}{
		{
			name:    "clean AI draft without TM support",
			signals: clean,
			want:    0.75,
			factors: map[string]float64{domain.FactorOrigin: -0.20, domain.FactorTMMatch: -0.05},
		},
		{
			name:    "exact TM reuse is capped below certainty",
			signals: domain.Signals{Origin: domain.OriginTranslationMemory, TMScore: 101},
			want:    0.99,
			factors: map[string]float64{domain.FactorOrigin: -0.05, domain.FactorTMMatch: 0.04},
		},
		{
			name:    "exact TM reuse",
			signals: domain.Signals{Origin: domain.OriginTranslationMemory, TMScore: 100},
			want:    0.97,
			factors: map[string]float64{domain.FactorOrigin: -0.05, domain.FactorTMMatch: 0.02},
		},
		{
			name:    "strong fuzzy match supports the draft",
			signals: with(clean, func(s *domain.Signals) { s.TMScore = 90 }),
			want:    0.872,
			factors: map[string]float64{domain.FactorTMMatch: 0.072},
		},
		{
			name:    "weak fuzzy match is neutral",
			signals: with(clean, func(s *domain.Signals) { s.TMScore = 60 }),
			want:    0.8,
		},
		{
			name: "forbidden and missing terms, capped",
			signals: with(clean, func(s *domain.Signals) {
				s.TMScore = 60
				s.TermForbidden, s.TermMissing = 3, 1
			}),
			want:    0,
			factors: map[string]float64{domain.FactorTermForbidden: -0.70, domain.FactorTermMissing: -0.20},
		},
		{
			name:    "repairs and warnings",
			signals: with(clean, func(s *domain.Signals) { s.Repairs, s.QAWarnings, s.TMScore = 2, 4, 60 }),
			want:    0.48,
			factors: map[string]float64{domain.FactorRepairs: -0.20, domain.FactorQAWarnings: -0.12},
		},
		{
			name:    "high self-assessment helps a little",
			signals: with(clean, func(s *domain.Signals) { s.SelfAssessment = ptr(0.9) }),
			want:    0.81,
			factors: map[string]float64{domain.FactorSelfAssessment: 0.06},
		},
		{
			name:    "low self-assessment hurts a little",
			signals: with(clean, func(s *domain.Signals) { s.SelfAssessment = ptr(0.1) }),
			want:    0.69,
			factors: map[string]float64{domain.FactorSelfAssessment: -0.06},
		},
		{
			name:    "wrong formality",
			signals: with(clean, func(s *domain.Signals) { s.FormalityOK = ptr(false) }),
			want:    0.6,
			factors: map[string]float64{domain.FactorFormality: -0.15},
		},
		{
			name: "max_length exceeded",
			signals: with(clean, func(s *domain.Signals) {
				s.SourceLength, s.TextLength, s.MaxLength = 10, 30, ptr(20)
			}),
			want:    0.35,
			factors: map[string]float64{domain.FactorMaxLength: -0.40},
		},
		{
			name:    "German at its expected length is fine",
			signals: with(clean, func(s *domain.Signals) { s.SourceLength, s.TextLength = 40, 52 }),
			want:    0.75,
		},
		{
			name:    "German far too short",
			signals: with(clean, func(s *domain.Signals) { s.SourceLength, s.TextLength = 40, 20 }),
			want:    0.6,
			factors: map[string]float64{domain.FactorLengthRatio: -0.15},
		},
		{
			name:    "German somewhat long",
			signals: with(clean, func(s *domain.Signals) { s.SourceLength, s.TextLength = 40, 80 }),
			want:    0.68,
			factors: map[string]float64{domain.FactorLengthRatio: -0.07},
		},
		{
			// "Alle Arbeitsbereiche" → "Todos los espacios de trabajo":
			// labels vary too much for the mild band.
			name: "a short label somewhat long is fine",
			signals: with(clean, func(s *domain.Signals) {
				s.SourceLocale, s.TargetLocale, s.SourceLength, s.TextLength = "de", "es", 20, 29
			}),
			want: 0.75,
		},
		{
			// "Alle Einladungen" → "招待一覧".
			name: "a short label somewhat short is fine",
			signals: with(clean, func(s *domain.Signals) {
				s.SourceLocale, s.TargetLocale, s.SourceLength, s.TextLength = "de", "ja", 16, 4
			}),
			want: 0.75,
		},
		{
			name: "a short label far too short",
			signals: with(clean, func(s *domain.Signals) {
				s.SourceLocale, s.TargetLocale, s.SourceLength, s.TextLength = "de", "ja", 16, 2
			}),
			want:    0.6,
			factors: map[string]float64{domain.FactorLengthRatio: -0.15},
		},
		{
			name:    "short sources are not length-judged",
			signals: with(clean, func(s *domain.Signals) { s.SourceLength, s.TextLength = 5, 20 }),
			want:    0.75,
		},
		{
			name:    "legal and marketing tags",
			signals: with(clean, func(s *domain.Signals) { s.Tags = []string{domain.TagMarketing, domain.TagLegal} }),
			want:    0.3,
		},
		{
			name:    "dense markup",
			signals: with(clean, func(s *domain.Signals) { s.MarkupCount = 6 }),
			want:    0.67,
			factors: map[string]float64{domain.FactorMarkup: -0.08},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.Score(tc.signals)
			if math.Abs(got.Score-tc.want) > 1e-9 {
				t.Errorf("score = %v, want %v (%+v)", got.Score, tc.want, got.Explanation)
			}
			if got.Score < 0 || got.Score > 1 {
				t.Errorf("score out of range: %v", got.Score)
			}
			for factor, want := range tc.factors {
				if c, ok := contribution(got, factor); !ok || math.Abs(c-want) > 1e-9 {
					t.Errorf("%s contribution = %v (present %v), want %v", factor, c, ok, want)
				}
			}
			again := cfg.Score(tc.signals)
			if len(again.Explanation) != len(got.Explanation) || again.Score != got.Score {
				t.Error("scoring is not deterministic")
			}
		})
	}
}

func TestReviewPolicyRoute(t *testing.T) {
	def := domain.DefaultReviewPolicy()
	auto := def
	auto.AutoApprove = true
	forbidden := domain.Confidence{Score: 0.95, Explanation: []domain.Factor{{Factor: domain.FactorTermForbidden, Contribution: -0.35}}}
	missing := domain.Confidence{Score: 0.99, Explanation: []domain.Factor{{Factor: domain.FactorMissingPluralCategories, Value: 2, Contribution: -0.6}}}
	helpedByTM := domain.Confidence{Score: 0.95, Explanation: []domain.Factor{{Factor: domain.FactorTMMatch, Contribution: 0.04}}}
	tests := []struct {
		name   string
		policy domain.ReviewPolicy
		conf   domain.Confidence
		want   domain.Action
	}{
		{"auto-approve is off by default", def, domain.Confidence{Score: 0.99}, domain.ActionApproveRecommended},
		{"auto-approve when enabled", auto, domain.Confidence{Score: 0.95}, domain.ActionAutoApprove},
		{"below auto band", auto, domain.Confidence{Score: 0.8}, domain.ActionApproveRecommended},
		{"recommend band boundary", def, domain.Confidence{Score: 0.75}, domain.ActionApproveRecommended},
		{"below recommend", def, domain.Confidence{Score: 0.7499}, domain.ActionReviewRequired},
		{"forced review wins", auto, forbidden, domain.ActionReviewRequired},
		{"a helping factor does not force review", auto, helpedByTM, domain.ActionAutoApprove},
		{"missing plural categories force review under any policy", domain.ReviewPolicy{AutoApprove: true}, missing, domain.ActionReviewRequired},
	}
	for _, tc := range tests {
		if got := tc.policy.Route(tc.conf); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestMissingPluralCategoriesLowerTheScore(t *testing.T) {
	c := domain.DefaultScoring().Score(domain.Signals{
		Origin: domain.OriginAI, TMScore: 0, SourceLocale: "en", TargetLocale: "pl",
		MissingPluralCategories: []string{"few", "many"},
	})
	if c.Score > 0.2 || !c.Has(domain.FactorMissingPluralCategories) {
		t.Fatalf("confidence = %+v", c)
	}
	last := c.Explanation[len(c.Explanation)-1]
	if last.Value != 2 || last.Reason != "the translation lacks the pl plural categories few, many" {
		t.Errorf("factor = %+v", last)
	}
}

func TestTranslationRequestValidate(t *testing.T) {
	ok := domain.TranslationRequest{
		Scope: domain.Scope{TenantID: "t"}, MessageID: "m", SourceLocale: "en", TargetLocale: "de", Source: "Hi",
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*domain.TranslationRequest){
		"tenant":  func(r *domain.TranslationRequest) { r.Scope.TenantID = "" },
		"message": func(r *domain.TranslationRequest) { r.MessageID = "" },
		"locale":  func(r *domain.TranslationRequest) { r.TargetLocale = "" },
		"source":  func(r *domain.TranslationRequest) { r.Source = "" },
	} {
		r := ok
		mut(&r)
		if r.Validate() == nil {
			t.Errorf("missing %s: want error", name)
		}
	}
	ok.Tags = []string{domain.TagLegal, domain.TagSensitive}
	if !ok.Sensitive() {
		t.Error("tagged sensitive")
	}
}

func with(s domain.Signals, mut func(*domain.Signals)) domain.Signals {
	mut(&s)
	return s
}

func contribution(c domain.Confidence, factor string) (float64, bool) {
	for _, f := range c.Explanation {
		if f.Factor == factor {
			return f.Contribution, true
		}
	}
	return 0, false
}
