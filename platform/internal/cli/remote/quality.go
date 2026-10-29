package remote

import (
	"context"
	"net/http"
	"time"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// CheckPolicy reads the project's check-policy document (RFC 0005 §4.2).
//
// This is the source `glossa check` grades against. The document the
// server returns carries its rules, environments and version, so a run
// can say which version decided it — which the three fields of
// settings.check_policy could not.
func (c *Client) CheckPolicy(ctx context.Context, s Scope) (checkpolicy.Policy, error) {
	st, err := c.CheckPolicyState(ctx, s)
	if err != nil {
		return checkpolicy.Policy{}, err
	}
	return st.Policy, nil
}

// PolicyState is the policy in force and the record of who put it
// there, which `glossa policy show` prints and `glossa check` does not
// need.
type PolicyState struct {
	Policy checkpolicy.Policy
	// CreatedBy and CreatedAt are empty for a policy nobody saved
	// through the API — every project's, before the first save.
	CreatedBy string
	CreatedAt time.Time
	// PinnedVersion is the version a pull request still inside the grace
	// grades against; 0 when nothing is pinned.
	PinnedVersion int
}

// CheckPolicyState reads the project's policy with its bookkeeping.
func (c *Client) CheckPolicyState(ctx context.Context, s Scope) (PolicyState, error) {
	r, err := c.api.GetCheckPolicyWithResponse(ctx, s.Tenant, s.Project)
	if err := check(r, err, http.MethodGet, c.path("/v1/tenants/%s/projects/%s/check-policy", s.Tenant, s.Project)); err != nil {
		return PolicyState{}, err
	}
	return stateFromWire(*r.JSON200), nil
}

func stateFromWire(st apiclient.CheckPolicyState) PolicyState {
	out := PolicyState{Policy: policyFromWire(st), CreatedBy: derefOr(st.CreatedBy)}
	if st.CreatedAt != nil {
		out.CreatedAt = *st.CreatedAt
	}
	if st.PinnedVersion != nil {
		out.PinnedVersion = *st.PinnedVersion
	}
	return out
}

// SavePolicy is a write of the document: what it should say, how long
// the save pins the pull requests that predate it, and whether to store
// anything at all.
type SavePolicy struct {
	Policy checkpolicy.Policy
	// GraceDays is how long open pull requests keep grading against the
	// version they were opened under. nil is the server's default (14
	// days); 0 pins nothing.
	GraceDays *int
	// DryRun answers the impact preview and stores nothing (RFC 0005
	// §4.3).
	DryRun bool
}

// CheckPolicyImpact is RFC 0005 §4.3's impact preview, as the API
// answers it.
type CheckPolicyImpact = apiclient.CheckPolicyImpact

// PolicySaved is a save, or what one would do: the policy that now
// grades (or would), and what it changes about the findings and the
// runs the project already has.
type PolicySaved struct {
	DryRun bool
	State  PolicyState
	Impact CheckPolicyImpact
}

// SaveCheckPolicy stores a new version, or — with DryRun — answers the
// impact preview and stores nothing.
func (c *Client) SaveCheckPolicy(ctx context.Context, s Scope, in SavePolicy) (PolicySaved, error) {
	body := apiclient.SaveCheckPolicyJSONRequestBody{Policy: documentToWire(in.Policy), GraceDays: in.GraceDays}
	if in.DryRun {
		body.DryRun = ptrOf(true)
	}
	url := c.path("/v1/tenants/%s/projects/%s/check-policy", s.Tenant, s.Project)
	r, err := c.api.SaveCheckPolicyWithResponse(ctx, s.Tenant, s.Project, body)
	if err := check(r, err, http.MethodPost, url); err != nil {
		return PolicySaved{}, err
	}
	return savedFromWire(*r.JSON200), nil
}

// ExportCheckPolicy reads the document alone — what the policy says,
// without the version behind it — which is exactly what
// ImportCheckPolicy takes.
func (c *Client) ExportCheckPolicy(ctx context.Context, s Scope) (checkpolicy.Policy, error) {
	url := c.path("/v1/tenants/%s/projects/%s/check-policy/export", s.Tenant, s.Project)
	r, err := c.api.ExportCheckPolicyWithResponse(ctx, s.Tenant, s.Project)
	if err := check(r, err, http.MethodGet, url); err != nil {
		return checkpolicy.Policy{}, err
	}
	return documentFromWire(*r.JSON200), nil
}

// ImportCheckPolicy saves an exported document as the next version.
func (c *Client) ImportCheckPolicy(ctx context.Context, s Scope, in SavePolicy) (PolicySaved, error) {
	params := &apiclient.ImportCheckPolicyParams{GraceDays: in.GraceDays}
	if in.DryRun {
		params.DryRun = ptrOf(true)
	}
	url := c.path("/v1/tenants/%s/projects/%s/check-policy/import", s.Tenant, s.Project)
	r, err := c.api.ImportCheckPolicyWithResponse(ctx, s.Tenant, s.Project, params, documentToWire(in.Policy))
	if err := check(r, err, http.MethodPost, url); err != nil {
		return PolicySaved{}, err
	}
	return savedFromWire(*r.JSON200), nil
}

func savedFromWire(s apiclient.CheckPolicySaved) PolicySaved {
	return PolicySaved{DryRun: s.DryRun, State: stateFromWire(s.Policy), Impact: s.Impact}
}

// policyFromWire turns the API's document into the kernel's policy.
//
// The two disagree on one field, deliberately: the wire says
// require_complete as `all | listed | none` beside a `locales` list,
// because an enum is what an API can validate and document, while the
// kernel says it as nil (every locale), an empty slice (none) or the
// named ones. Everything else is the same shape under the same names,
// but this one cannot round-trip through JSON, so the whole conversion
// is written out rather than half-inferred.
func policyFromWire(st apiclient.CheckPolicyState) checkpolicy.Policy {
	p := documentFromWire(st.Document)
	p.Version = st.Version
	if st.EffectiveFrom != nil {
		t := *st.EffectiveFrom
		p.EffectiveFrom = &t
	}
	if st.GraceUntil != nil {
		t := *st.GraceUntil
		p.GraceUntil = &t
	}
	return p
}

// documentFromWire reads the document alone — what the policy says,
// without the bookkeeping about when it said it.
func documentFromWire(doc apiclient.CheckPolicyDocument) checkpolicy.Policy {
	p := checkpolicy.Policy{
		FailOn:              checkpolicy.Severity(doc.FailOn),
		MissingTranslations: checkpolicy.Severity(doc.MissingTranslations),
	}
	// nil is every locale; an empty slice is none. Required says the
	// first as All, so it is the one case that maps to leaving the
	// field alone.
	if req := requiredFromWire(doc.RequireComplete, doc.Locales); !req.All {
		p.RequireComplete = req.Locales
	}
	if doc.Schema != nil {
		p.Schema = string(*doc.Schema)
	}
	if doc.Rules != nil {
		p.Rules = make([]checkpolicy.Rule, 0, len(*doc.Rules))
		for _, r := range *doc.Rules {
			p.Rules = append(p.Rules, ruleFromWire(r))
		}
	}
	if doc.Environments != nil {
		p.Environments = make(map[string]checkpolicy.Environment, len(*doc.Environments))
		for name, e := range *doc.Environments {
			p.Environments[name] = environmentFromWire(e)
		}
	}
	return p
}

// documentToWire is the other direction, for a write.
//
// It sends the document and only the document: the version, when it
// takes effect and what it pins are the server's, and a write that
// carried them would be asserting a history it has no business
// asserting. The one translation is require_complete, for the reason
// policyFromWire gives.
func documentToWire(p checkpolicy.Policy) apiclient.CheckPolicyDocument {
	out := apiclient.CheckPolicyDocument{
		FailOn:              apiclient.CheckPolicyDocumentFailOn(orElse(p.FailOn, checkpolicy.Error)),
		MissingTranslations: apiclient.CheckPolicyDocumentMissingTranslations(orElse(p.MissingTranslations, checkpolicy.Error)),
		RequireComplete:     apiclient.CheckPolicyLocaleRequirementAll,
		Locales:             ptrOf([]apiclient.Locale{}),
	}
	if p.RequireComplete != nil {
		out.RequireComplete = apiclient.CheckPolicyLocaleRequirementListed
		out.Locales = ptrOf(append([]apiclient.Locale{}, p.RequireComplete...))
	}
	if len(p.Rules) > 0 {
		rules := make([]apiclient.CheckPolicyRule, 0, len(p.Rules))
		for _, r := range p.Rules {
			rules = append(rules, ruleToWire(r))
		}
		out.Rules = &rules
	}
	if len(p.Environments) > 0 {
		envs := make(map[string]apiclient.CheckPolicyEnvironment, len(p.Environments))
		for name, e := range p.Environments {
			envs[name] = environmentToWire(e)
		}
		out.Environments = &envs
	}
	return out
}

func orElse(s, fallback checkpolicy.Severity) checkpolicy.Severity {
	if s == "" {
		return fallback
	}
	return s
}

func ruleToWire(r checkpolicy.Rule) apiclient.CheckPolicyRule {
	out := apiclient.CheckPolicyRule{Severity: apiclient.CheckPolicyRuleSeverity(r.Severity)}
	if r.Mode != "" {
		out.Mode = ptrOf(apiclient.CheckPolicyRuleMode(r.Mode))
	}
	if r.Layer != "" {
		out.Layer = ptrOf(apiclient.FindingLayer(r.Layer))
	}
	if r.Code != "" {
		out.Code = ptrOf(r.Code)
	}
	if r.Locale != "" {
		out.Locale = ptrOf(apiclient.Locale(r.Locale))
	}
	if r.Namespace != "" {
		out.Namespace = ptrOf(apiclient.Namespace(r.Namespace))
	}
	if r.Environment != "" {
		out.Environment = ptrOf(r.Environment)
	}
	return out
}

// environmentToWire sends only what the block says, so a round trip
// cannot turn "I did not say" into "every locale".
func environmentToWire(e checkpolicy.Environment) apiclient.CheckPolicyEnvironment {
	out := apiclient.CheckPolicyEnvironment{}
	if e.RequireComplete.Set {
		out.RequireComplete = ptrOf(apiclient.CheckPolicyLocaleRequirementAll)
		out.Locales = ptrOf([]apiclient.Locale{})
		if !e.RequireComplete.All {
			out.RequireComplete = ptrOf(apiclient.CheckPolicyLocaleRequirementListed)
			out.Locales = ptrOf(append([]apiclient.Locale{}, e.RequireComplete.Locales...))
		}
	}
	if e.RequireReview != "" {
		out.RequireReview = ptrOf(apiclient.CheckPolicyEnvironmentRequireReview(e.RequireReview))
	}
	return out
}

// requiredFromWire reads the enum and its list as one answer.
func requiredFromWire(req apiclient.CheckPolicyLocaleRequirement, locales *[]apiclient.Locale) checkpolicy.Required {
	switch req {
	case apiclient.CheckPolicyLocaleRequirementAll:
		return checkpolicy.AllLocales()
	case apiclient.CheckPolicyLocaleRequirementListed:
		named := []string{}
		if locales != nil {
			for _, l := range *locales {
				named = append(named, string(l))
			}
		}
		return checkpolicy.RequiredLocales(named...)
	default: // none
		return checkpolicy.RequiredLocales()
	}
}

func ruleFromWire(r apiclient.CheckPolicyRule) checkpolicy.Rule {
	out := checkpolicy.Rule{Severity: checkpolicy.Severity(r.Severity)}
	if r.Mode != nil {
		out.Mode = checkpolicy.Mode(*r.Mode)
	}
	if r.Layer != nil {
		out.Selector.Layer = string(*r.Layer)
	}
	if r.Code != nil {
		out.Selector.Code = *r.Code
	}
	if r.Locale != nil {
		out.Selector.Locale = string(*r.Locale)
	}
	if r.Namespace != nil {
		out.Selector.Namespace = string(*r.Namespace)
	}
	if r.Environment != nil {
		out.Selector.Environment = *r.Environment
	}
	return out
}

// environmentFromWire leaves what the block did not say unset, so
// "I did not name a require_complete" keeps inheriting the document's
// rather than becoming "every locale".
func environmentFromWire(e apiclient.CheckPolicyEnvironment) checkpolicy.Environment {
	out := checkpolicy.Environment{}
	if e.RequireComplete != nil {
		out.RequireComplete = requiredFromWire(*e.RequireComplete, e.Locales)
	}
	if e.RequireReview != nil {
		out.RequireReview = string(*e.RequireReview)
	}
	return out
}
