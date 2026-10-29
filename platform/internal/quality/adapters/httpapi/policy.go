package httpapi

import (
	"context"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
)

// The check policy as a resource (RFC 0005 §4, §13 wave 3): read it,
// write it with an impact preview, read how it got to be what it is,
// and take it in and out as a file.
//
// The wire spells `require_complete` as it has always been spelled on
// this API — `all`, `listed` or `none` with a `locales` list — where the
// document has nil for "every locale" and an empty slice for "none".
// Two spellings of one thing on one API would be worse than the one
// translation here.

// ── the document ────────────────────────────────────────────────────

func toPolicyDocument(p checkpolicy.Policy) apiv1.CheckPolicyDocument {
	out := apiv1.CheckPolicyDocument{
		Schema:              apiconv.Ptr(apiv1.CheckPolicyDocumentSchema(checkpolicy.Schema)),
		RequireComplete:     apiv1.CheckPolicyLocaleRequirement(requirementOf(p.RequireComplete)),
		Locales:             apiconv.Ptr(localesOf(p.RequireComplete)),
		FailOn:              apiv1.CheckPolicyDocumentFailOn(orDefault(p.FailOn, checkpolicy.Error)),
		MissingTranslations: apiv1.CheckPolicyDocumentMissingTranslations(orDefault(p.MissingTranslations, checkpolicy.Error)),
	}
	if len(p.Environments) > 0 {
		envs := make(map[string]apiv1.CheckPolicyEnvironment, len(p.Environments))
		for name, e := range p.Environments {
			envs[name] = toPolicyEnvironment(e)
		}
		out.Environments = &envs
	}
	if len(p.Rules) > 0 {
		rules := make([]apiv1.CheckPolicyRule, len(p.Rules))
		for i, r := range p.Rules {
			rules[i] = toPolicyRule(r)
		}
		out.Rules = &rules
	}
	return out
}

// requirementOf names the three modes the wire spells out, where the
// document has nil for "every locale" and an empty slice for "none".
func requirementOf(required []string) string {
	switch {
	case required == nil:
		return "all"
	case len(required) == 0:
		return "none"
	}
	return "listed"
}

func localesOf(required []string) []apiv1.Locale {
	out := make([]apiv1.Locale, 0, len(required))
	return append(out, required...)
}

func orDefault(s, fallback checkpolicy.Severity) checkpolicy.Severity {
	if s == "" {
		return fallback
	}
	return s
}

func toPolicyEnvironment(e checkpolicy.Environment) apiv1.CheckPolicyEnvironment {
	out := apiv1.CheckPolicyEnvironment{}
	if e.RequireComplete.Set {
		required := e.RequireComplete.Locales
		if e.RequireComplete.All {
			required = nil
		}
		out.RequireComplete = apiconv.Ptr(apiv1.CheckPolicyLocaleRequirement(requirementOf(required)))
		out.Locales = apiconv.Ptr(localesOf(e.RequireComplete.Locales))
	}
	if e.RequireReview != "" {
		out.RequireReview = apiconv.Ptr(apiv1.CheckPolicyEnvironmentRequireReview(e.RequireReview))
	}
	return out
}

func toPolicyRule(r checkpolicy.Rule) apiv1.CheckPolicyRule {
	out := apiv1.CheckPolicyRule{Severity: apiv1.CheckPolicyRuleSeverity(r.Severity)}
	if r.Layer != "" {
		out.Layer = apiconv.Ptr(apiv1.FindingLayer(r.Layer))
	}
	if r.Code != "" {
		out.Code = apiconv.Ptr(r.Code)
	}
	if r.Locale != "" {
		out.Locale = apiconv.Ptr(apiv1.Locale(r.Locale))
	}
	if r.Namespace != "" {
		out.Namespace = apiconv.Ptr(apiv1.Namespace(r.Namespace))
	}
	if r.Environment != "" {
		out.Environment = apiconv.Ptr(r.Environment)
	}
	if r.Mode != "" {
		out.Mode = apiconv.Ptr(apiv1.CheckPolicyRuleMode(r.Mode))
	}
	return out
}

// fromPolicyDocument reads a document from a request. It fills in only
// what the request says: the version, when it takes effect and what it
// pins are the server's, and a document that carried them would be
// refused by the domain rather than quietly stripped here.
func fromPolicyDocument(d apiv1.CheckPolicyDocument) (checkpolicy.Policy, error) {
	out := checkpolicy.Policy{
		FailOn: checkpolicy.Severity(d.FailOn), MissingTranslations: checkpolicy.Severity(d.MissingTranslations),
	}
	required, err := fromRequirement(string(d.RequireComplete), d.Locales)
	if err != nil {
		return checkpolicy.Policy{}, err
	}
	out.RequireComplete = required
	if d.Schema != nil && string(*d.Schema) != "" {
		out.Schema = string(*d.Schema)
	}
	if d.Environments != nil {
		out.Environments = make(map[string]checkpolicy.Environment, len(*d.Environments))
		for name, e := range *d.Environments {
			env, err := fromPolicyEnvironment(e)
			if err != nil {
				return checkpolicy.Policy{}, err
			}
			out.Environments[name] = env
		}
	}
	if d.Rules != nil {
		out.Rules = make([]checkpolicy.Rule, 0, len(*d.Rules))
		for _, r := range *d.Rules {
			rule, err := fromPolicyRule(r)
			if err != nil {
				return checkpolicy.Policy{}, err
			}
			out.Rules = append(out.Rules, rule)
		}
	}
	return out, nil
}

// fromRequirement reads the three modes back. `listed` with no locales
// is `none`: an empty list of locales that must be complete is exactly
// no locale having to be.
func fromRequirement(requirement string, locales *[]apiv1.Locale) ([]string, error) {
	switch requirement {
	case "all":
		return nil, nil
	case "none":
		return []string{}, nil
	case "listed":
		out := []string{}
		if locales != nil {
			for _, l := range *locales {
				tag, err := bcp47.Parse(l)
				if err != nil {
					return nil, mapError(err)
				}
				out = append(out, tag.String())
			}
		}
		return out, nil
	}
	return nil, invalidPolicy("require_complete is all, listed or none")
}

func fromPolicyEnvironment(e apiv1.CheckPolicyEnvironment) (checkpolicy.Environment, error) {
	out := checkpolicy.Environment{}
	if e.RequireReview != nil {
		out.RequireReview = string(*e.RequireReview)
	}
	if e.RequireComplete == nil {
		// The block did not say, which is "inherit the document's" — not
		// the same as "every locale", and the distinction is the whole
		// point of the field being optional.
		return out, nil
	}
	required, err := fromRequirement(string(*e.RequireComplete), e.Locales)
	if err != nil {
		return checkpolicy.Environment{}, err
	}
	if required == nil {
		out.RequireComplete = checkpolicy.AllLocales()
		return out, nil
	}
	out.RequireComplete = checkpolicy.RequiredLocales(required...)
	return out, nil
}

func fromPolicyRule(r apiv1.CheckPolicyRule) (checkpolicy.Rule, error) {
	out := checkpolicy.Rule{Severity: checkpolicy.Severity(r.Severity), Mode: checkpolicy.Mode(deref(r.Mode))}
	out.Layer, out.Code = string(deref(r.Layer)), deref(r.Code)
	out.Namespace, out.Environment = string(deref(r.Namespace)), deref(r.Environment)
	if locale := deref(r.Locale); locale != "" {
		tag, err := bcp47.Parse(locale)
		if err != nil {
			return checkpolicy.Rule{}, mapError(err)
		}
		out.Locale = tag.String()
	}
	return out, nil
}

// ── the state around it ─────────────────────────────────────────────

func toPolicyState(s app.PolicyState) apiv1.CheckPolicyState {
	out := apiv1.CheckPolicyState{Version: s.Policy.Version, Document: toPolicyDocument(s.Policy)}
	if s.Policy.EffectiveFrom != nil {
		out.EffectiveFrom = apiconv.Ptr(*s.Policy.EffectiveFrom)
	}
	if s.Policy.GraceUntil != nil {
		out.GraceUntil = apiconv.Ptr(*s.Policy.GraceUntil)
	}
	if s.Policy.Previous != nil {
		out.PinnedVersion = apiconv.Ptr(s.Policy.Previous.Version)
	}
	if s.CreatedBy != "" {
		out.CreatedBy = apiconv.Ptr(s.CreatedBy)
	}
	if !s.CreatedAt.IsZero() {
		out.CreatedAt = apiconv.Ptr(s.CreatedAt)
	}
	return out
}

func toPolicyImpact(p app.Preview) apiv1.CheckPolicyImpact {
	out := apiv1.CheckPolicyImpact{
		Findings: p.Targets, Runs: p.Runs, Raised: p.Raised, Lowered: p.Lowered, Silenced: p.Silenced,
		NewlyFailing: p.NewlyFailing, NoLongerFailing: p.NoLongerFailing,
		OpenPullRequests: p.OpenPullRequests, Rules: make([]apiv1.CheckPolicyRuleImpact, len(p.Rules)),
	}
	for i, r := range p.Rules {
		selector := toPolicyRule(checkpolicy.Rule{Selector: r.Selector, Severity: r.Severity, Mode: r.Mode})
		out.Rules[i] = apiv1.CheckPolicyRuleImpact{
			Rule: r.Rule, Selector: &selector,
			Matched: r.Matched, Changed: r.Changed, NewlyFailing: r.NewlyFailing,
		}
	}
	if len(p.NewlyFailingRefs) > 0 {
		out.NewlyFailingRefs = apiconv.Ptr(p.NewlyFailingRefs)
	}
	if len(p.NoLongerFailingRefs) > 0 {
		out.NoLongerFailingRefs = apiconv.Ptr(p.NoLongerFailingRefs)
	}
	if len(p.PullRequests) > 0 {
		prs := make([]apiv1.CheckPolicyPullRequest, len(p.PullRequests))
		for i, pr := range p.PullRequests {
			prs[i] = apiv1.CheckPolicyPullRequest{Ref: pr.Ref, Number: pr.Number}
			if pr.URL != "" {
				prs[i].Url = apiconv.Ptr(pr.URL)
			}
		}
		out.NewlyFailingPullRequests = &prs
	}
	return out
}

func toPolicyVersion(v app.PolicyVersion) apiv1.CheckPolicyVersion {
	out := apiv1.CheckPolicyVersion{
		Version: v.Version, Document: toPolicyDocument(v.Policy),
		CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt,
	}
	if v.Policy.EffectiveFrom != nil {
		out.EffectiveFrom = apiconv.Ptr(*v.Policy.EffectiveFrom)
	}
	if v.Policy.GraceUntil != nil {
		out.GraceUntil = apiconv.Ptr(*v.Policy.GraceUntil)
	}
	return out
}

// graceOf reads grace_days. Absent is checkpolicy.DefaultGrace — the
// default protects the people who did not cause the change — and zero
// pins nothing.
func graceOf(days *int) *time.Duration {
	if days == nil {
		return nil
	}
	d := time.Duration(*days) * 24 * time.Hour
	return &d
}

// ── the operations ──────────────────────────────────────────────────

// GetCheckPolicy reads the project's check policy.
func (a *API) GetCheckPolicy(
	ctx context.Context, req apiv1.GetCheckPolicyRequestObject,
) (apiv1.GetCheckPolicyResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	state, err := a.svc.CheckPolicy(ctx, project)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetCheckPolicy200JSONResponse(toPolicyState(state)), nil
}

// SaveCheckPolicy stores a new version, or — with `dry_run` — answers
// what doing so would change and stores nothing.
func (a *API) SaveCheckPolicy(
	ctx context.Context, req apiv1.SaveCheckPolicyRequestObject,
) (apiv1.SaveCheckPolicyResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	doc, err := fromPolicyDocument(req.Body.Policy)
	if err != nil {
		return nil, err
	}
	saved, err := a.svc.SavePolicy(ctx, project, app.SavePolicy{
		Policy: doc, Grace: graceOf(req.Body.GraceDays), DryRun: deref(req.Body.DryRun),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.SaveCheckPolicy200JSONResponse(toPolicySaved(saved)), nil
}

func toPolicySaved(s app.PolicySave) apiv1.CheckPolicySaved {
	return apiv1.CheckPolicySaved{
		DryRun: s.DryRun, Policy: toPolicyState(s.State), Impact: toPolicyImpact(s.Impact),
	}
}

// ListCheckPolicyVersions pages the project's policy versions, newest
// first.
func (a *API) ListCheckPolicyVersions(
	ctx context.Context, req apiv1.ListCheckPolicyVersionsRequestObject,
) (apiv1.ListCheckPolicyVersionsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	vs, next, err := a.svc.ListPolicyVersions(ctx, project, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListCheckPolicyVersions200JSONResponse{
		Items: make([]apiv1.CheckPolicyVersion, len(vs)), NextPageToken: next,
	}
	for i, v := range vs {
		out.Items[i] = toPolicyVersion(v)
	}
	return out, nil
}

// GetCheckPolicyVersion reads one version of the project's policy.
func (a *API) GetCheckPolicyVersion(
	ctx context.Context, req apiv1.GetCheckPolicyVersionRequestObject,
) (apiv1.GetCheckPolicyVersionResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	v, err := a.svc.PolicyVersion(ctx, project, req.Version)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetCheckPolicyVersion200JSONResponse(toPolicyVersion(v)), nil
}

// ExportCheckPolicy writes the document alone, which is exactly what
// ImportCheckPolicy reads.
func (a *API) ExportCheckPolicy(
	ctx context.Context, req apiv1.ExportCheckPolicyRequestObject,
) (apiv1.ExportCheckPolicyResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	state, err := a.svc.CheckPolicy(ctx, project)
	if err != nil {
		return nil, mapError(err)
	}
	// The document as it grades, without the version behind it: an
	// export carries what the policy says, and importing another
	// project's history would be asserting it.
	return apiv1.ExportCheckPolicy200JSONResponse(toPolicyDocument(state.Policy.Current())), nil
}

// ImportCheckPolicy saves an exported document as the next version.
func (a *API) ImportCheckPolicy(
	ctx context.Context, req apiv1.ImportCheckPolicyRequestObject,
) (apiv1.ImportCheckPolicyResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	doc, err := fromPolicyDocument(*req.Body)
	if err != nil {
		return nil, err
	}
	saved, err := a.svc.SavePolicy(ctx, project, app.SavePolicy{
		Policy: doc, Grace: graceOf(req.Params.GraceDays), DryRun: deref(req.Params.DryRun),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.ImportCheckPolicy200JSONResponse(toPolicySaved(saved)), nil
}
