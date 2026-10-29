package domain_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

func TestValidateBaseURL(t *testing.T) {
	tests := []struct {
		url          string
		allowPrivate bool
		ok           bool
	}{
		{"https://api.mistral.ai/v1", false, true},
		{"http://api.example.com/v1", false, false},
		{"https://127.0.0.1:8443/v1", false, false},
		{"https://10.1.2.3/v1", false, false},
		{"https://[::1]/v1", false, false},
		{"https://169.254.169.254/latest", false, false},
		{"https://100.64.1.1/", false, false},
		{"http://localhost:11434/v1", false, false},
		{"http://localhost:11434/v1", true, true},
		{"http://10.0.0.5:8000/v1", true, true},
		{"https://user:pw@api.example.com/v1", false, false},
		{"https://api.example.com/v1?key=x", false, false},
		{"not a url", false, false},
		{"ftp://api.example.com", false, false},
	}
	for _, tc := range tests {
		err := domain.ValidateBaseURL(tc.url, tc.allowPrivate)
		if (err == nil) != tc.ok {
			t.Errorf("%s (private %v): err = %v, want ok %v", tc.url, tc.allowPrivate, err, tc.ok)
		}
		if err != nil && !errors.Is(err, domain.ErrInvalidProvider) {
			t.Errorf("%s: %v is not ErrInvalidProvider", tc.url, err)
		}
	}
}

func TestProviderConfigValidate(t *testing.T) {
	ok := domain.ProviderConfig{Name: "anthropic", Kind: domain.KindAnthropic, Models: []string{"claude-sonnet-5"}}
	if err := ok.Validate(false); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]domain.ProviderConfig{
		"bad name":               {Name: "Anthropic", Kind: domain.KindAnthropic},
		"bad kind":               {Name: "x", Kind: "cohere"},
		"compatible needs url":   {Name: "mistral", Kind: domain.KindOpenAICompatible},
		"blank model":            {Name: "x", Kind: domain.KindGemini, Models: []string{" m"}},
		"private url by default": {Name: "x", Kind: domain.KindOpenAICompatible, BaseURL: "https://192.168.1.2/v1"},
	} {
		if err := p.Validate(false); !errors.Is(err, domain.ErrInvalidProvider) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if !ok.Allows("claude-sonnet-5") || ok.Allows("gpt-5") || !(domain.ProviderConfig{}).Allows("anything") {
		t.Error("the allow-list admits listed models only; an empty list admits any")
	}
}

func TestValidateRouting(t *testing.T) {
	providers := []domain.ProviderConfig{{Name: "anthropic", Kind: domain.KindAnthropic, Models: []string{"claude-sonnet-5", "claude-haiku-4-5-20251001"}}}
	route := func(provider, model string) domain.RoutingPolicy {
		return domain.RoutingPolicy{Rules: []domain.RoutingRule{{Task: domain.TaskTranslate, Routes: []domain.Route{{Provider: provider, Model: model, MaxTokens: 1000}}}}}
	}
	if err := domain.ValidateRouting(route("anthropic", "claude-sonnet-5"), providers); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]domain.RoutingPolicy{
		"unconfigured provider": route("openai", "gpt-5"),
		"model not allowed":     route("anthropic", "claude-opus-5"),
		"unknown task":          {Rules: []domain.RoutingRule{{Task: "summarize", Routes: route("anthropic", "claude-sonnet-5").Rules[0].Routes}}},
		"no routes":             {Rules: []domain.RoutingRule{{Task: domain.TaskTranslate}}},
	} {
		if err := domain.ValidateRouting(p, providers); !errors.Is(err, domain.ErrInvalidRouting) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestSettingsValidation(t *testing.T) {
	s := domain.DefaultTenantSettings()
	if s.ProviderConsent || s.MonthlyBudget != 0 || s.Validate() != nil {
		t.Fatalf("defaults = %+v: consent off, no budget", s)
	}
	s.MaxConcurrentJobs = 0
	if !errors.Is(s.Validate(), domain.ErrInvalidSettings) {
		t.Error("max_concurrent_jobs 0")
	}
	s = domain.DefaultTenantSettings()
	s.Prices = domain.PriceTable{"anthropic": {}}
	if !errors.Is(s.Validate(), domain.ErrInvalidPrices) {
		t.Error("a price key needs provider/model")
	}

	r := domain.DefaultReviewSettings()
	if r.AutoApprove || r.Validate() != nil {
		t.Fatalf("review defaults = %+v", r)
	}
	r.AutoApprove = true
	if !errors.Is(r.Validate(), domain.ErrInvalidReviewPolicy) {
		t.Error("auto_approve needs environments")
	}
	r.AutoApproveEnvironments = []string{"production"}
	r.AutoApproveMin = 0.5
	if !errors.Is(r.Validate(), domain.ErrInvalidReviewPolicy) {
		t.Error("auto_approve_min below recommend_min")
	}

	tags := domain.NamespaceTags{"legal": {"legal", "sensitive", "legal"}, "empty": nil}
	if err := tags.Validate(); err != nil {
		t.Fatal(err)
	}
	if n := tags.Normalized(); len(n) != 1 || !slices.Equal(n["legal"], []string{"legal", "sensitive"}) {
		t.Errorf("normalized = %v", n)
	}
	if !errors.Is(domain.NamespaceTags{"x": {"secret"}}.Validate(), domain.ErrInvalidNamespaceTags) {
		t.Error("unknown tag")
	}
}

func TestFingerprintAndBackoff(t *testing.T) {
	a := domain.Fingerprint{Prompts: "translate/v1", Styles: []string{"g2@1", "g1@3"}, Concepts: []string{"c1@2"}}.Sum()
	b := domain.Fingerprint{Prompts: "translate/v1", Styles: []string{"g1@3", "g2@1"}, Concepts: []string{"c1@2"}}.Sum()
	c := domain.Fingerprint{Prompts: "translate/v1", Styles: []string{"g1@4", "g2@1"}, Concepts: []string{"c1@2"}}.Sum()
	if a != b || a == c || len(a) != 64 {
		t.Errorf("fingerprints %s %s %s: order-independent, version-sensitive", a, b, c)
	}
	if domain.Backoff(1) != 30*time.Second || domain.Backoff(2) != time.Minute || domain.Backoff(20) != 30*time.Minute {
		t.Errorf("backoff = %v %v %v", domain.Backoff(1), domain.Backoff(2), domain.Backoff(20))
	}
}

func TestEditDiff(t *testing.T) {
	before, _, _ := domain.ParseMessage("Speichere die Datei...")
	after, _, _ := domain.ParseMessage("Speichern Sie das Dokument …")
	d := domain.Diff(before, after, []string{"Datei"}, []string{"Dokument"}, "Sie")
	if d.Distance == 0 || d.Ratio <= 0 || d.Ratio > 1 {
		t.Errorf("distance = %+v", d)
	}
	if !slices.Equal(d.TermsAdded, []string{"Dokument"}) || !slices.Equal(d.TermsRemoved, []string{"Datei"}) {
		t.Errorf("terms = %+v", d)
	}
	if !slices.Equal(d.StyleFields, []string{"ellipsis", "pronoun"}) {
		t.Errorf("style fields = %v", d.StyleFields)
	}
	same := domain.Diff(before, before, nil, nil, "")
	if same.Distance != 0 || same.Ratio != 0 || same.StyleFields != nil {
		t.Errorf("unchanged = %+v", same)
	}
}
