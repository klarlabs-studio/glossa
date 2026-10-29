package domain

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Configuration errors.
var (
	ErrInvalidProvider      = errors.New("intelligence: invalid provider")
	ErrInvalidRouting       = errors.New("intelligence: invalid routing policy")
	ErrInvalidPrices        = errors.New("intelligence: invalid price table")
	ErrInvalidSettings      = errors.New("intelligence: invalid settings")
	ErrInvalidNamespaceTags = errors.New("intelligence: invalid namespace tags")
	ErrInvalidReviewPolicy  = errors.New("intelligence: invalid review policy")
)

// ProviderKind is the API a configured provider speaks (RFC 0003 §3.1).
type ProviderKind string

// Provider kinds.
const (
	KindAnthropic        ProviderKind = "anthropic"
	KindOpenAICompatible ProviderKind = "openai_compatible"
	KindGemini           ProviderKind = "gemini"
)

// ParseProviderKind validates s.
func ParseProviderKind(s string) (ProviderKind, error) {
	switch k := ProviderKind(s); k {
	case KindAnthropic, KindOpenAICompatible, KindGemini:
		return k, nil
	}
	return "", fmt.Errorf("%w: kind must be anthropic, openai_compatible or gemini, not %q", ErrInvalidProvider, s)
}

var providerName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// Limits on provider configuration.
const (
	MaxProviderModels = 50
	MaxModelNameLen   = 200
	MaxBaseURLLen     = 500
	MaxAPIKeyLen      = 1000
)

// ProviderConfig is a provider a tenant configured with its own
// credentials (BYO keys). Name is what routing refers to. The API key is
// write-only: it is sealed at rest and never returned; HasKey says
// whether one is set.
type ProviderConfig struct {
	ID      uuid.UUID
	Name    string
	Kind    ProviderKind
	BaseURL string
	// Models is the allow-list; empty allows any model the routing names.
	Models    []string
	Enabled   bool
	HasKey    bool
	Version   int
	CreatedBy string
	CreatedAt time.Time
	UpdatedBy string
	UpdatedAt time.Time
}

// Validate checks the configuration. allowPrivate permits base URLs on
// loopback and private networks (self-hosted models inside the cluster);
// deployments turn it on explicitly.
func (p ProviderConfig) Validate(allowPrivate bool) error {
	if !providerName.MatchString(p.Name) {
		return fmt.Errorf("%w: name must match %s", ErrInvalidProvider, providerName)
	}
	if _, err := ParseProviderKind(string(p.Kind)); err != nil {
		return err
	}
	if p.Kind == KindOpenAICompatible && p.BaseURL == "" {
		return fmt.Errorf("%w: an openai_compatible provider needs a base_url", ErrInvalidProvider)
	}
	if p.BaseURL != "" {
		if err := ValidateBaseURL(p.BaseURL, allowPrivate); err != nil {
			return err
		}
	}
	if len(p.Models) > MaxProviderModels {
		return fmt.Errorf("%w: at most %d models", ErrInvalidProvider, MaxProviderModels)
	}
	for _, m := range p.Models {
		if m == "" || len(m) > MaxModelNameLen || strings.TrimSpace(m) != m {
			return fmt.Errorf("%w: model %q", ErrInvalidProvider, m)
		}
	}
	return nil
}

// Allows reports whether the allow-list admits model.
func (p ProviderConfig) Allows(model string) bool {
	return len(p.Models) == 0 || slices.Contains(p.Models, model)
}

// ValidateBaseURL checks a provider endpoint: https (http only for
// allowed private hosts), no credentials, query or fragment, and — unless
// allowPrivate — no loopback, private or link-local address literal. The
// HTTP client re-checks resolved addresses when it dials, so a hostname
// can't smuggle a private address in (SSRF).
func ValidateBaseURL(raw string, allowPrivate bool) error {
	if len(raw) > MaxBaseURLLen {
		return fmt.Errorf("%w: base_url is longer than %d characters", ErrInvalidProvider, MaxBaseURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: base_url must be an absolute URL without credentials, query or fragment", ErrInvalidProvider)
	}
	host := u.Hostname()
	private := host == "localhost" || strings.HasSuffix(host, ".localhost")
	if ip := net.ParseIP(host); ip != nil {
		private = PrivateIP(ip)
	}
	switch {
	case private && !allowPrivate:
		return fmt.Errorf("%w: base_url points at a private or loopback address; this deployment doesn't allow that (GLOSSA_AI_ALLOW_PRIVATE_ENDPOINTS)", ErrInvalidProvider)
	case u.Scheme == "https":
	case u.Scheme == "http" && private && allowPrivate:
	default:
		return fmt.Errorf("%w: base_url must use https", ErrInvalidProvider)
	}
	return nil
}

// PrivateIP reports addresses a tenant-configured endpoint must not
// reach by default: loopback, private, link-local, unspecified, CGNAT
// and unique-local ranges.
func PrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	cgnat := net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	return cgnat.Contains(ip)
}

// TenantSettings are a tenant's AI settings (RFC 0003 §7).
type TenantSettings struct {
	// ProviderConsent is the explicit permission to send text to AI
	// providers. Off by default; ConsentChangedBy/At record who changed
	// it last.
	ProviderConsent  bool
	ConsentChangedBy string
	ConsentChangedAt *time.Time
	// MaxConcurrentJobs caps the tenant's running jobs across replicas.
	MaxConcurrentJobs int
	// MonthlyBudget caps provider spend per calendar month (UTC); 0
	// allows none (a hard stop).
	MonthlyBudget MicroUSD
	// Prices override the default price table by "provider/model".
	Prices    PriceTable
	Version   int
	UpdatedBy string
	UpdatedAt time.Time
}

// Setting defaults and limits.
const (
	DefaultMaxConcurrentJobs = 4
	MaxConcurrentJobsLimit   = 64
	// MaxMonthlyBudget is 1 000 000 USD.
	MaxMonthlyBudget MicroUSD = 1_000_000_000_000
)

// DefaultTenantSettings are the settings of a tenant that saved none.
func DefaultTenantSettings() TenantSettings {
	return TenantSettings{MaxConcurrentJobs: DefaultMaxConcurrentJobs, Prices: PriceTable{}}
}

// Validate checks the settings.
func (s TenantSettings) Validate() error {
	if s.MaxConcurrentJobs < 1 || s.MaxConcurrentJobs > MaxConcurrentJobsLimit {
		return fmt.Errorf("%w: max_concurrent_jobs must be 1 to %d", ErrInvalidSettings, MaxConcurrentJobsLimit)
	}
	if s.MonthlyBudget < 0 || s.MonthlyBudget > MaxMonthlyBudget {
		return fmt.Errorf("%w: the monthly budget must be 0 to %d micro-USD", ErrInvalidSettings, MaxMonthlyBudget)
	}
	return s.Prices.Validate()
}

// Validate checks a price table: "provider/model" keys and finite,
// non-negative prices.
func (t PriceTable) Validate() error {
	if len(t) > 500 {
		return fmt.Errorf("%w: at most 500 prices", ErrInvalidPrices)
	}
	for k, p := range t {
		provider, model, ok := strings.Cut(k, "/")
		if !ok || !providerName.MatchString(provider) || model == "" || len(model) > MaxModelNameLen {
			return fmt.Errorf("%w: key %q is not <provider>/<model>", ErrInvalidPrices, k)
		}
		for _, v := range []float64{p.InputPerMTok, p.OutputPerMTok, p.CacheReadPerMTok, p.CacheWritePerMTok} {
			if v < 0 || v > 10_000 || math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("%w: %s has a price outside 0–10000 USD per million tokens", ErrInvalidPrices, k)
			}
		}
	}
	return nil
}

// Merged returns base with overrides applied.
func (t PriceTable) Merged(overrides PriceTable) PriceTable {
	out := make(PriceTable, len(t)+len(overrides))
	for k, v := range t {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

// ReviewSettings are a project's review routing (RFC 0003 §3.3): the
// score bands and, for auto_approve, the environments auto-approved text
// is meant to ship to.
type ReviewSettings struct {
	ReviewPolicy
	// AutoApproveEnvironments must be non-empty when AutoApprove is on.
	// auto_approve is honoured only while every one of them exists and
	// its eligibility policy ships approved text.
	AutoApproveEnvironments []string `json:"auto_approve_environments,omitempty"`
}

// DefaultReviewSettings is the M2 default: no auto-approval.
func DefaultReviewSettings() ReviewSettings {
	return ReviewSettings{ReviewPolicy: DefaultReviewPolicy()}
}

// Validate checks the bands.
func (r ReviewSettings) Validate() error {
	inBand := func(v float64) bool { return v > 0 && v <= 1 && !math.IsNaN(v) }
	switch {
	case !inBand(r.RecommendMin) || !inBand(r.AutoApproveMin):
		return fmt.Errorf("%w: auto_approve_min and recommend_min must be in (0, 1]", ErrInvalidReviewPolicy)
	case r.AutoApproveMin < r.RecommendMin:
		return fmt.Errorf("%w: auto_approve_min must be at least recommend_min", ErrInvalidReviewPolicy)
	case r.AutoApprove && len(r.AutoApproveEnvironments) == 0:
		return fmt.Errorf("%w: auto_approve needs auto_approve_environments", ErrInvalidReviewPolicy)
	case len(r.AutoApproveEnvironments) > 20 || len(r.ForceReview) > 20:
		return fmt.Errorf("%w: at most 20 environments and 20 forced factors", ErrInvalidReviewPolicy)
	}
	for _, f := range r.ForceReview {
		if !slices.Contains(ReviewableFactors(), f) {
			return fmt.Errorf("%w: unknown factor %q in force_review", ErrInvalidReviewPolicy, f)
		}
	}
	return nil
}

// ReviewableFactors are the factors a policy may force review on.
func ReviewableFactors() []string {
	return []string{
		FactorTermForbidden, FactorTermMissing, FactorRepairs, FactorQAWarnings, FactorFormality,
		FactorMaxLength, FactorLengthRatio, FactorRiskTag, FactorMarkup, FactorMissingPluralCategories,
	}
}

// NamespaceTags maps a namespace to its policy tags (sensitive, legal,
// marketing).
type NamespaceTags map[string][]string

var namespaceName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Validate checks namespaces and tags.
func (n NamespaceTags) Validate() error {
	if len(n) > 500 {
		return fmt.Errorf("%w: at most 500 namespaces", ErrInvalidNamespaceTags)
	}
	for ns, tags := range n {
		if !namespaceName.MatchString(ns) {
			return fmt.Errorf("%w: %q is not a namespace", ErrInvalidNamespaceTags, ns)
		}
		for _, t := range tags {
			if t != TagSensitive && t != TagLegal && t != TagMarketing {
				return fmt.Errorf("%w: tag %q (use sensitive, legal or marketing)", ErrInvalidNamespaceTags, t)
			}
		}
	}
	return nil
}

// Of returns ns's tags.
func (n NamespaceTags) Of(ns string) []string { return slices.Clone(n[ns]) }

// Normalized sorts and de-duplicates every namespace's tags and drops
// namespaces without tags.
func (n NamespaceTags) Normalized() NamespaceTags {
	out := NamespaceTags{}
	for ns, tags := range n {
		t := slices.Clone(tags)
		slices.Sort(t)
		if t = slices.Compact(t); len(t) > 0 {
			out[ns] = t
		}
	}
	return out
}

// ProjectSettings are a project's AI settings.
type ProjectSettings struct {
	ProjectID     uuid.UUID
	NamespaceTags NamespaceTags
	// AutoTranslateLocales are the locales new and outdated messages are
	// translated into automatically (off for every locale by default).
	AutoTranslateLocales []string
	Review               ReviewSettings
	Version              int
	UpdatedBy            string
	UpdatedAt            time.Time
}

// DefaultProjectSettings are the settings of a project that saved none.
func DefaultProjectSettings(project uuid.UUID) ProjectSettings {
	return ProjectSettings{ProjectID: project, NamespaceTags: NamespaceTags{}, Review: DefaultReviewSettings()}
}

// AutoTranslates reports whether auto-translate is on for locale.
func (p ProjectSettings) AutoTranslates(locale string) bool {
	return slices.ContainsFunc(p.AutoTranslateLocales, func(l string) bool { return strings.EqualFold(l, locale) })
}

// RoutingRecord is a stored routing policy: the tenant's (ProjectID
// nil) or a project's.
type RoutingRecord struct {
	ProjectID *uuid.UUID
	Policy    RoutingPolicy
	Version   int
	UpdatedBy string
	UpdatedAt time.Time
}

// Limits on routing policies.
const (
	MaxRoutingRules = 100
	MaxRoutes       = 5
	MaxRouteTokens  = 64_000
)

// ValidateRouting checks a policy's shape and, against the tenant's
// providers, that every route names a configured provider whose
// allow-list admits the model.
func ValidateRouting(p RoutingPolicy, providers []ProviderConfig) error {
	if len(p.Rules) > MaxRoutingRules {
		return fmt.Errorf("%w: at most %d rules", ErrInvalidRouting, MaxRoutingRules)
	}
	byName := map[string]ProviderConfig{}
	for _, pc := range providers {
		byName[pc.Name] = pc
	}
	for i, r := range p.Rules {
		switch r.Task {
		case TaskTranslate, TaskReview, TaskExplain, TaskAssess:
		default:
			return fmt.Errorf("%w: rule %d: unknown task %q", ErrInvalidRouting, i, r.Task)
		}
		if len(r.Routes) == 0 || len(r.Routes) > MaxRoutes {
			return fmt.Errorf("%w: rule %d: 1 to %d routes", ErrInvalidRouting, i, MaxRoutes)
		}
		for _, l := range r.Locales {
			if l == "" || len(l) > 35 {
				return fmt.Errorf("%w: rule %d: locale %q", ErrInvalidRouting, i, l)
			}
		}
		for j, rt := range r.Routes {
			pc, ok := byName[rt.Provider]
			switch {
			case !ok:
				return fmt.Errorf("%w: rule %d route %d: no provider named %q is configured", ErrInvalidRouting, i, j, rt.Provider)
			case rt.Model == "" || len(rt.Model) > MaxModelNameLen:
				return fmt.Errorf("%w: rule %d route %d: a model is required", ErrInvalidRouting, i, j)
			case !pc.Allows(rt.Model):
				return fmt.Errorf("%w: rule %d route %d: %s does not allow model %q", ErrInvalidRouting, i, j, rt.Provider, rt.Model)
			case rt.MaxTokens < 1 || rt.MaxTokens > MaxRouteTokens:
				return fmt.Errorf("%w: rule %d route %d: max_tokens must be 1 to %d", ErrInvalidRouting, i, j, MaxRouteTokens)
			case rt.Temperature != nil && (*rt.Temperature < 0 || *rt.Temperature > 2):
				return fmt.Errorf("%w: rule %d route %d: temperature must be 0 to 2", ErrInvalidRouting, i, j)
			}
			switch rt.Effort {
			case "", "low", "medium", "high", "max":
			default:
				return fmt.Errorf("%w: rule %d route %d: effort must be low, medium, high or max", ErrInvalidRouting, i, j)
			}
		}
	}
	return nil
}
