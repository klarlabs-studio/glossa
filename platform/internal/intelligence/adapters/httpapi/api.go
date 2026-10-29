// Package httpapi is Intelligence's HTTP edge: its operations of the
// generated /v1 strict server (provider configuration, routing, prices,
// budget, privacy and auto-translate settings, fills and jobs,
// suggestions and the review queue, disclosures, metrics and the eval
// baseline).
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/intelligence/evals"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// API serves Intelligence's operations.
type API struct {
	svc      *app.Service
	baseline evals.Baseline
}

// New returns the API; it serves the eval baseline committed with this
// build.
func New(svc *app.Service) (*API, error) {
	b, err := evals.CommittedBaseline()
	if err != nil {
		return nil, fmt.Errorf("intelligence: eval baseline: %w", err)
	}
	return &API{svc: svc, baseline: b}, nil
}

// pathID parses an ID in a URL; a malformed one is simply not found.
func pathID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, mapError(app.ErrNotFound)
	}
	return id, nil
}

// optionalID parses an optional ID filter.
func optionalID(name string, s *string) (*uuid.UUID, error) {
	if s == nil {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil || id == uuid.Nil {
		return nil, mapError(fmt.Errorf("%w: %s is not an id this API issued", app.ErrInvalidQuery, name))
	}
	return &id, nil
}

func tenantPath(ctx context.Context, sub string) string {
	t, _ := tenancy.FromContext(ctx)
	return "/v1/tenants/" + t.String() + sub
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func idPtr(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	return apiconv.Ptr(id.String())
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func page(size *int, token *string) (pagination.Page, error) { return pagination.Parse(size, token) }

func timePtr(t *time.Time) *time.Time { return t }

func zeroTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ── converters ───────────────────────────────────────────────────────

func toProvider(p domain.ProviderConfig) apiv1.AIProvider {
	return apiv1.AIProvider{
		Id: p.ID.String(), Name: p.Name, Kind: apiv1.AIProviderKind(p.Kind), BaseUrl: nonEmpty(p.BaseURL),
		Models: nonNil(p.Models), Enabled: p.Enabled, ApiKeySet: p.HasKey, Version: p.Version,
		CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt, UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt,
	}
}

func toSettings(s domain.TenantSettings) apiv1.AISettings {
	return apiv1.AISettings{
		ProviderConsent: s.ProviderConsent, ConsentChangedBy: nonEmpty(s.ConsentChangedBy), ConsentChangedAt: timePtr(s.ConsentChangedAt),
		MaxConcurrentJobs: s.MaxConcurrentJobs, MonthlyBudgetMicroUsd: int64(s.MonthlyBudget), Version: s.Version,
		UpdatedBy: nonEmpty(s.UpdatedBy), UpdatedAt: zeroTime(s.UpdatedAt),
	}
}

func toPriceTable(t domain.PriceTable) apiv1.AIPriceTable {
	out := apiv1.AIPriceTable{}
	for k, p := range t {
		out[k] = apiv1.AIPrice{
			InputPerMtok: p.InputPerMTok, OutputPerMtok: p.OutputPerMTok,
			CacheReadPerMtok: apiconv.Ptr(p.CacheReadPerMTok), CacheWritePerMtok: apiconv.Ptr(p.CacheWritePerMTok),
		}
	}
	return out
}

func fromPriceTable(t map[string]apiv1.AIPrice) domain.PriceTable {
	out := domain.PriceTable{}
	for k, p := range t {
		out[k] = domain.Price{
			InputPerMTok: p.InputPerMtok, OutputPerMTok: p.OutputPerMtok,
			CacheReadPerMTok: deref(p.CacheReadPerMtok), CacheWritePerMTok: deref(p.CacheWritePerMtok),
		}
	}
	return out
}

func toPrices(p app.Prices) apiv1.AIPrices {
	return apiv1.AIPrices{Defaults: toPriceTable(p.Defaults), Overrides: toPriceTable(p.Overrides), Effective: toPriceTable(p.Effective), Version: p.Version}
}

func toUsage(u domain.Usage) apiv1.AIUsage {
	out := apiv1.AIUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
	if u.CacheReadTokens > 0 {
		out.CacheReadTokens = &u.CacheReadTokens
	}
	if u.CacheWriteTokens > 0 {
		out.CacheWriteTokens = &u.CacheWriteTokens
	}
	return out
}

func toRoutingPolicy(p domain.RoutingPolicy) apiv1.AIRoutingPolicy {
	out := apiv1.AIRoutingPolicy{Rules: make([]apiv1.AIRoutingRule, len(p.Rules))}
	for i, r := range p.Rules {
		rule := apiv1.AIRoutingRule{Task: apiv1.AITask(r.Task), Routes: make([]apiv1.AIRoute, len(r.Routes))}
		if len(r.Locales) > 0 {
			rule.Locales = apiconv.Ptr(r.Locales)
		}
		for j, rt := range r.Routes {
			route := apiv1.AIRoute{Provider: rt.Provider, Model: rt.Model, MaxTokens: rt.MaxTokens, Temperature: rt.Temperature}
			if rt.Effort != "" {
				route.Effort = apiconv.Ptr(apiv1.AIRouteEffort(rt.Effort))
			}
			rule.Routes[j] = route
		}
		out.Rules[i] = rule
	}
	return out
}

func fromRoutingPolicy(p apiv1.AIRoutingPolicy) domain.RoutingPolicy {
	out := domain.RoutingPolicy{Rules: make([]domain.RoutingRule, len(p.Rules))}
	for i, r := range p.Rules {
		rule := domain.RoutingRule{Task: domain.Task(r.Task), Locales: deref(r.Locales), Routes: make([]domain.Route, len(r.Routes))}
		for j, rt := range r.Routes {
			rule.Routes[j] = domain.Route{Provider: rt.Provider, Model: rt.Model, MaxTokens: rt.MaxTokens, Temperature: rt.Temperature, Effort: string(deref(rt.Effort))}
		}
		out.Rules[i] = rule
	}
	return out
}

func toRoutingView(v app.RoutingView) apiv1.AIRoutingPolicyView {
	return apiv1.AIRoutingPolicyView{
		Policy: toRoutingPolicy(v.Record.Policy), Source: apiv1.AIRoutingPolicyViewSource(v.Source), Version: v.Record.Version,
		UpdatedBy: nonEmpty(v.Record.UpdatedBy), UpdatedAt: zeroTime(v.Record.UpdatedAt),
	}
}

func toReview(r domain.ReviewSettings) apiv1.AIReviewPolicy {
	return apiv1.AIReviewPolicy{
		AutoApprove: r.AutoApprove, AutoApproveMin: r.AutoApproveMin, RecommendMin: r.RecommendMin,
		ForceReview: apiconv.Ptr(nonNil(r.ForceReview)), AutoApproveEnvironments: apiconv.Ptr(nonNil(r.AutoApproveEnvironments)),
	}
}

func fromReview(r apiv1.AIReviewPolicy) domain.ReviewSettings {
	return domain.ReviewSettings{
		ReviewPolicy: domain.ReviewPolicy{
			AutoApprove: r.AutoApprove, AutoApproveMin: r.AutoApproveMin, RecommendMin: r.RecommendMin, ForceReview: deref(r.ForceReview),
		},
		AutoApproveEnvironments: deref(r.AutoApproveEnvironments),
	}
}

func toProjectSettings(s domain.ProjectSettings) apiv1.AIProjectSettings {
	tags := map[string][]apiv1.AINamespaceTag{}
	for ns, ts := range s.NamespaceTags {
		for _, t := range ts {
			tags[ns] = append(tags[ns], apiv1.AINamespaceTag(t))
		}
	}
	return apiv1.AIProjectSettings{
		NamespaceTags: tags, AutoTranslateLocales: nonNil(s.AutoTranslateLocales), Review: toReview(s.Review),
		Version: s.Version, UpdatedBy: nonEmpty(s.UpdatedBy), UpdatedAt: zeroTime(s.UpdatedAt),
	}
}

func toFill(r app.FillResult) apiv1.AIFill {
	f := r.Fill
	out := apiv1.AIFill{
		Id: f.ID.String(), ProjectId: f.ProjectID.String(), Trigger: apiv1.AIFillTrigger(f.Trigger), Locales: nonNil(f.Locales),
		Namespace: nonEmpty(f.Filter.Namespace), KeyPrefix: nonEmpty(f.Filter.KeyPrefix), Select: apiv1.AIFillSelect(f.Filter.Selection()),
		JobsCreated: f.JobsCreated, JobsExisting: f.JobsExisting, Skipped: map[string]int{}, JobStates: map[string]int{},
		Warnings: nonNil(r.Warnings), RequestedBy: f.RequestedBy, CreatedAt: f.CreatedAt,
	}
	if len(f.Filter.Keys) > 0 {
		out.Keys = apiconv.Ptr(f.Filter.Keys)
	}
	if f.Filter.IncludeOutdated {
		out.IncludeOutdated = apiconv.Ptr(true)
	}
	if len(f.JobIDs) > 0 {
		ids := make([]string, len(f.JobIDs))
		for i, id := range f.JobIDs {
			ids[i] = id.String()
		}
		out.JobIds = &ids
	}
	for k, n := range f.Skipped {
		out.Skipped[k] = n
	}
	for s, n := range r.Counts {
		out.JobStates[string(s)] = n
	}
	return out
}

func toCost(c app.CostEstimate) apiv1.AICostEstimate {
	return apiv1.AICostEstimate{EstimatedMicroUsd: apiv1.MicroUSD(c.Estimated), MaxMicroUsd: apiv1.MicroUSD(c.Max), Unpriced: c.Unpriced}
}

func toFillPreview(p app.FillPreview) apiv1.AIFillPreview {
	out := apiv1.AIFillPreview{
		ProjectId: p.ProjectID.String(), Select: apiv1.AIFillSelect(p.Select), Warnings: nonNil(p.Warnings),
		Locales: make([]apiv1.AIFillPreviewLocale, len(p.Locales)), Cost: toCost(p.Cost),
	}
	for i, l := range p.Locales {
		out.Locales[i] = apiv1.AIFillPreviewLocale{
			Locale: l.Locale, Keys: nonNil(l.Keys), Existing: l.Existing, TmExact: l.TMExact, Provider: l.Provider,
			Refused: l.Refused, Skipped: l.Skipped, Cost: toCost(l.Cost),
		}
	}
	return out
}

func toJob(j domain.Job, audit json.RawMessage) apiv1.AIJob {
	out := apiv1.AIJob{
		Id: j.ID.String(), ProjectId: j.ProjectID.String(), MessageId: j.MessageID.String(), MessageKey: j.MessageKey,
		Namespace: j.Namespace, Locale: j.Locale, SourceRevision: j.SourceRevision, KnowledgeFingerprint: j.Fingerprint,
		Trigger: apiv1.AITrigger(j.Trigger), FillId: idPtr(j.FillID), State: apiv1.AIJobState(j.State),
		Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, AvailableAt: j.AvailableAt, FailureCode: nonEmpty(j.FailureCode),
		LastError: nonEmpty(j.LastError), SuggestionId: idPtr(j.SuggestionID), CreatedBy: j.CreatedBy, CreatedAt: j.CreatedAt,
		StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, UpdatedAt: j.UpdatedAt,
	}
	if len(audit) > 0 {
		var entries []app.AuditEntry
		if json.Unmarshal(audit, &entries) == nil {
			a := make([]apiv1.AIAuditEntry, len(entries))
			for i, e := range entries {
				a[i] = apiv1.AIAuditEntry{Tool: e.Tool, Output: e.Output, At: e.At}
			}
			out.Audit = &a
		}
	}
	return out
}

// toSuggestion maps a suggestion with its message's current source
// (when sources has it).
func toSuggestion(r domain.SuggestionRecord, sources map[uuid.UUID]app.MessageSource) apiv1.AISuggestion {
	pv := r.Provenance
	out := apiv1.AISuggestion{
		Id: r.ID.String(), JobId: r.JobID.String(), ProjectId: r.ProjectID.String(), MessageId: r.MessageID.String(),
		MessageKey: r.MessageKey, Namespace: r.Namespace, Locale: r.Locale, SourceRevision: r.SourceRevision,
		Message: r.Message, Model: model(r), Findings: apiconv.Findings(r.Findings), TermFindings: []apiv1.AITermFinding{},
		Provenance: apiv1.AIProvenance{
			Origin: apiv1.AIProvenanceOrigin(pv.Origin), Provider: nonEmpty(pv.Provider), Model: nonEmpty(pv.Model),
			PromptVersion: nonEmpty(pv.PromptVersion), StyleVersion: nonEmpty(pv.StyleVersion), Repairs: pv.Repairs,
			TmUnitIds: apiconv.Ptr(nonNil(pv.TMUnitIDs)), TermIds: apiconv.Ptr(nonNil(pv.TermIDs)),
		},
		Score: r.Confidence.Score, Explanation: make([]apiv1.AIConfidenceFactor, len(r.Confidence.Explanation)),
		Action: apiv1.AIAction(r.Action), ActionNote: nonEmpty(r.ActionNote), RiskTags: nonNil(r.RiskTags),
		Calls: make([]apiv1.AICall, len(r.Calls)), Usage: toUsage(r.Usage), CostMicroUsd: int64(r.Cost),
		Status: apiv1.AISuggestionStatus(r.Status), TranslationRevision: r.TranslationRevision, DecidedBy: nonEmpty(r.DecidedBy),
		DecidedAt: r.DecidedAt, Version: r.Version, CreatedAt: r.CreatedAt,
	}
	for _, f := range r.TermFindings {
		out.TermFindings = append(out.TermFindings, apiv1.AITermFinding{
			Code: apiv1.AITermFindingCode(f.Code), ConceptId: f.ConceptID, TermId: nonEmpty(f.TermID), Term: f.Term, Message: f.Message,
		})
	}
	for i, f := range r.Confidence.Explanation {
		out.Explanation[i] = apiv1.AIConfidenceFactor{Factor: f.Factor, Value: f.Value, Contribution: f.Contribution, Reason: f.Reason}
	}
	for i, c := range r.Calls {
		out.Calls[i] = apiv1.AICall{Task: apiv1.AITask(c.Task), Provider: c.Provider, Model: c.Model, PromptVersion: c.PromptVersion,
			Usage: toUsage(c.Usage), CostMicroUsd: int64(c.Cost)}
	}
	if src, ok := sources[r.MessageID]; ok {
		state := apiv1.AISuggestionSourceStateActive
		if !src.Active {
			state = apiv1.AISuggestionSourceStateObsolete
		}
		out.Source = &apiv1.AISuggestionSource{
			MessageKey: src.Key, Namespace: src.Namespace, State: state, SourceRevision: src.Revision,
			Text: src.Text, Syntax: apiv1.Syntax(src.Syntax), Mf2: src.MF2, Model: mf2Model(src.Model),
		}
	}
	if d := r.Decision; d != nil {
		out.Decision = &apiv1.AIDecision{Reason: nonEmpty(d.Reason)}
		if e := d.Edit; e != nil {
			out.Decision.Edit = &apiv1.AIEditDiff{
				Distance: e.Distance, Ratio: e.Ratio, TermsAdded: apiconv.Ptr(nonNil(e.TermsAdded)),
				TermsRemoved: apiconv.Ptr(nonNil(e.TermsRemoved)), StyleFields: apiconv.Ptr(nonNil(e.StyleFields)),
			}
		}
	}
	return out
}

// model renders a suggestion's MF2 data model.
func model(r domain.SuggestionRecord) apiv1.MF2Message { return mf2Model(r.Model) }

func mf2Model(m mf.Message) apiv1.MF2Message {
	out := apiv1.MF2Message{}
	if b, err := json.Marshal(m); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func toSuggestions(rs []domain.SuggestionRecord, sources map[uuid.UUID]app.MessageSource) []apiv1.AISuggestion {
	out := make([]apiv1.AISuggestion, len(rs))
	for i, r := range rs {
		out[i] = toSuggestion(r, sources)
	}
	return out
}

func toDisclosure(d app.DisclosureRecord) apiv1.AIDisclosure {
	out := apiv1.AIDisclosure{
		Id: d.ID.String(), JobId: d.JobID.String(), ProjectId: d.ProjectID.String(), MessageId: d.MessageID.String(),
		Locale: d.Locale, Task: apiv1.AITask(d.Task), Provider: d.Provider, Model: d.Model, SystemSha256: d.SystemSHA256,
		Sent: make([]apiv1.AISentMessage, len(d.Sent)), OccurredAt: d.OccurredAt,
	}
	for i, m := range d.Sent {
		out.Sent[i] = apiv1.AISentMessage{Role: apiv1.AISentMessageRole(m.Role), Text: m.Text}
	}
	return out
}

// toUnitSuggestions maps suggestions to the unit workspace's view: the
// text, score and explanation, and nothing of the job behind them.
func toUnitSuggestions(rs []app.UnitSuggestion) []apiv1.UnitAISuggestion {
	out := make([]apiv1.UnitAISuggestion, len(rs))
	for i, u := range rs {
		r := u.Record
		v := apiv1.UnitAISuggestion{
			Id: r.ID.String(), Locale: r.Locale, SourceRevision: r.SourceRevision, Message: r.Message,
			Findings: apiconv.Findings(r.Findings), TermFindings: []apiv1.AITermFinding{}, Score: r.Confidence.Score,
			Explanation: make([]apiv1.AIConfidenceFactor, len(r.Confidence.Explanation)), Action: apiv1.AIAction(r.Action),
			ActionNote: nonEmpty(r.ActionNote), RiskTags: nonNil(r.RiskTags), Status: apiv1.AISuggestionStatus(r.Status),
			Outdated: u.Outdated, Decidable: u.Decidable, CreatedAt: r.CreatedAt,
		}
		for _, f := range r.TermFindings {
			v.TermFindings = append(v.TermFindings, apiv1.AITermFinding{
				Code: apiv1.AITermFindingCode(f.Code), ConceptId: f.ConceptID, TermId: nonEmpty(f.TermID), Term: f.Term, Message: f.Message,
			})
		}
		for j, f := range r.Confidence.Explanation {
			v.Explanation[j] = apiv1.AIConfidenceFactor{Factor: f.Factor, Value: f.Value, Contribution: f.Contribution, Reason: f.Reason}
		}
		out[i] = v
	}
	return out
}
