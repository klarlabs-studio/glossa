package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// ── output shapes (cmd/glossa/README.md, "JSON output") ─────────────

type factorJSON struct {
	Factor       string  `json:"factor"`
	Value        float64 `json:"value"`
	Contribution float64 `json:"contribution"`
	Reason       string  `json:"reason"`
}

type suggestionFindingJSON struct {
	Code     string `json:"code"`
	Severity string `json:"severity,omitempty"`
	Message  string `json:"message"`
	Term     string `json:"term,omitempty"`
}

// suggestionJSON is an AI suggestion with its confidence explanation.
type suggestionJSON struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Locale    string `json:"locale"`
	Namespace string `json:"namespace"`
	// Text is the suggestion in MF2.
	Text        string       `json:"text"`
	Score       float64      `json:"score"`
	Action      string       `json:"action"` // auto_approve, approve_recommended, review_required
	ActionNote  string       `json:"action_note,omitempty"`
	RiskTags    []string     `json:"risk_tags"`
	Status      string       `json:"status"`
	Origin      string       `json:"origin"` // ai or translation_memory
	Provider    string       `json:"provider,omitempty"`
	Model       string       `json:"model,omitempty"`
	Explanation []factorJSON `json:"explanation"`
	// Findings are structural and terminology findings.
	Findings            []suggestionFindingJSON `json:"findings"`
	TranslationRevision *int                    `json:"translation_revision,omitempty"`
	CreatedAt           time.Time               `json:"created_at"`
}

func toSuggestion(s remote.AISuggestion) suggestionJSON {
	out := suggestionJSON{ID: s.Id, Key: s.MessageKey, Locale: s.Locale, Namespace: s.Namespace, Text: s.Message, Score: s.Score,
		Action: string(s.Action), ActionNote: derefStr(s.ActionNote), RiskTags: nonNilList(s.RiskTags), Status: string(s.Status),
		Origin: string(s.Provenance.Origin), Provider: derefStr(s.Provenance.Provider), Model: derefStr(s.Provenance.Model),
		Explanation: []factorJSON{}, Findings: []suggestionFindingJSON{}, TranslationRevision: s.TranslationRevision, CreatedAt: s.CreatedAt}
	for _, f := range s.Explanation {
		out.Explanation = append(out.Explanation, factorJSON{Factor: f.Factor, Value: f.Value, Contribution: f.Contribution, Reason: f.Reason})
	}
	for _, f := range s.Findings {
		out.Findings = append(out.Findings, suggestionFindingJSON{Code: f.Code, Severity: string(f.Severity), Message: f.Message})
	}
	for _, f := range s.TermFindings {
		out.Findings = append(out.Findings, suggestionFindingJSON{Code: string(f.Code), Message: f.Message, Term: f.Term})
	}
	return out
}

type reviewListJSON struct {
	Schema      string           `json:"schema"`
	Suggestions []suggestionJSON `json:"suggestions"`
}

type reviewDecisionJSON struct {
	Schema string `json:"schema"`
	// Decision is accepted or rejected.
	Decision   string         `json:"decision"`
	Edited     bool           `json:"edited"`
	Suggestion suggestionJSON `json:"suggestion"`
}

const reviewUsage = `review <action> [flags]

Actions:
  list [--locale L]... [--limit N]
                  the AI review queue, riskiest first (lowest confidence, most risk tags)
  accept <suggestion-id | key> [--locale L] [--text TEXT [--syntax mf1|mf2]]
                  write the suggestion as the translation; --text accepts your edit instead
  reject <suggestion-id | key> [--locale L] [--reason TEXT]
                  reject it; nothing is written

A key names the pending suggestion for that message (--locale when it has several).`

type reviewArgs struct {
	action, ref, text, syntax, reason string
	locales                           listFlag
	limit                             int
}

func parseReviewArgs(inv *invocation, args []string) (reviewArgs, error) {
	fs := inv.flags(reviewUsage)
	var a reviewArgs
	fs.Var(&a.locales, "locale", "only suggestions in this locale (repeatable)")
	fs.StringVar(&a.text, "text", "", "accept: your edited translation instead of the suggestion")
	fs.StringVar(&a.syntax, "syntax", "", "accept: the syntax of --text, mf1 (ICU) or mf2 (default: glossa.yaml's syntax)")
	fs.StringVar(&a.reason, "reason", "", "reject: why")
	fs.IntVar(&a.limit, "limit", 50, "list: at most this many (0: all)")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return a, err
	}
	if len(pos) == 0 {
		return a, usageError(inv.name, "missing action: list, accept or reject")
	}
	a.action, pos = pos[0], pos[1:]
	switch a.action {
	case "list":
		if a.limit < 0 {
			return a, usageError(inv.name, "--limit must be 0 (all) or more")
		}
		return a, noMore(inv, pos)
	case "accept", "reject":
		if len(pos) != 1 {
			return a, usageError(inv.name, "%s takes a suggestion ID or a message key", a.action)
		}
		a.ref = pos[0]
		if a.syntax != "" && a.syntax != "mf1" && a.syntax != "mf2" {
			return a, usageError(inv.name, "--syntax must be mf1 or mf2, not %q", a.syntax)
		}
		return a, nil
	}
	return a, usageError(inv.name, "unknown action %q (list, accept, reject)", a.action)
}

func runReview(ctx context.Context, inv *invocation, args []string) error {
	a, err := parseReviewArgs(inv, args)
	if err != nil {
		return err
	}
	locales, err := normalizeLocales(inv, "--locale", a.locales)
	if err != nil {
		return err
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	if a.action == "list" {
		return inv.reviewList(ctx, p, locales, a.limit)
	}
	id, err := inv.resolveSuggestion(ctx, p, a.ref, locales)
	if err != nil {
		return err
	}
	_, etag, err := p.client.AISuggestion(ctx, p.scope.Tenant, id)
	if err != nil {
		return inv.m2Error(err, "can't read suggestion "+id)
	}
	var s remote.AISuggestion
	if a.action == "accept" {
		syntax := ""
		if a.text != "" {
			syntax = orDefault(a.syntax, orDefault(p.cfg.Syntax, "mf1"))
		}
		s, err = p.client.AcceptAISuggestion(ctx, p.scope.Tenant, id, etag, a.text, syntax)
	} else {
		s, err = p.client.RejectAISuggestion(ctx, p.scope.Tenant, id, etag, a.reason)
	}
	if err != nil {
		return inv.m2Error(err, fmt.Sprintf("can't %s suggestion %s", a.action, id))
	}
	decision := a.action + "ed"
	out := reviewDecisionJSON{Schema: "glossa.cli.review.decision/v1", Decision: decision, Edited: a.text != "", Suggestion: toSuggestion(s)}
	return inv.emit(out, func(pr *printer) {
		what := decision
		if out.Edited {
			what = "accepted with your edit"
		}
		pr.line("%s %s %s %s %s", pr.pass(), s.MessageKey, s.Locale, what, pr.dim("("+s.Id+")"))
		if s.TranslationRevision != nil {
			pr.line("  %s", pr.dim(fmt.Sprintf("translation revision %d written", *s.TranslationRevision)))
		}
	})
}

// resolveSuggestion takes a suggestion ID, or finds the pending
// suggestion for a message key in the review queue.
func (inv *invocation) resolveSuggestion(ctx context.Context, p *project, ref string, locales []string) (string, error) {
	if idPattern.MatchString(ref) {
		return ref, nil
	}
	queue, err := p.client.ReviewQueue(ctx, p.scope, locales, 0)
	if err != nil {
		return "", inv.m2Error(err, "can't read the review queue")
	}
	var found []remote.AISuggestion
	for _, s := range queue {
		if s.MessageKey == ref {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 1:
		return found[0].Id, nil
	case 0:
		return "", &Error{Exit: ExitNetwork, Code: "suggestion_not_found", What: "no pending suggestion for " + ref,
			Where: "the review queue" + localesSuffix(locales),
			Fix:   "`glossa review list` shows the pending suggestions; `glossa translate` asks for new ones"}
	}
	var ls []string
	for _, s := range found {
		ls = append(ls, s.Locale)
	}
	return "", &Error{Exit: ExitUsage, Code: "suggestion_ambiguous", What: fmt.Sprintf("%s has pending suggestions in %s", ref, strings.Join(ls, ", ")),
		Fix: "pass --locale, or the suggestion's ID (`glossa review list` shows it)"}
}

func localesSuffix(ls []string) string {
	if len(ls) == 0 {
		return ""
	}
	return " (" + strings.Join(ls, ", ") + ")"
}

func (inv *invocation) reviewList(ctx context.Context, p *project, locales []string, limit int) error {
	queue, err := p.client.ReviewQueue(ctx, p.scope, locales, limit)
	if err != nil {
		return inv.m2Error(err, "can't read the review queue")
	}
	out := reviewListJSON{Schema: "glossa.cli.review.list/v1", Suggestions: []suggestionJSON{}}
	for _, s := range queue {
		out.Suggestions = append(out.Suggestions, toSuggestion(s))
	}
	return inv.emit(out, func(pr *printer) {
		if len(out.Suggestions) == 0 {
			pr.line("%s Nothing to review%s.", pr.pass(), localesSuffix(locales))
			return
		}
		pr.line("%s to review%s, riskiest first", plural(len(out.Suggestions), "suggestion", "suggestions"), localesSuffix(locales))
		rows := [][]string{{"SCORE", "LOCALE", "KEY", "SUGGESTION", "WHY", "ID"}}
		for _, s := range out.Suggestions {
			rows = append(rows, []string{fmt.Sprintf("%.2f", s.Score), s.Locale, s.Key, s.Text, why(s), s.ID})
		}
		pr.table(rows)
		pr.line("%s", pr.dim("`glossa review accept <id>` (--text for an edit) or `glossa review reject <id>`"))
	})
}

// why names the risk tags and the factors that lowered the score most.
func why(s suggestionJSON) string {
	parts := append([]string{}, s.RiskTags...)
	factors := append([]factorJSON{}, s.Explanation...)
	sort.SliceStable(factors, func(i, j int) bool { return factors[i].Contribution < factors[j].Contribution })
	for _, f := range factors {
		if len(parts) >= 3 || f.Contribution >= 0 {
			break
		}
		parts = append(parts, f.Factor)
	}
	return dash(strings.Join(parts, ", "))
}

// ── ai status ───────────────────────────────────────────────────────

type aiProviderJSON struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Enabled   bool     `json:"enabled"`
	APIKeySet bool     `json:"api_key_set"`
	BaseURL   string   `json:"base_url,omitempty"`
	Models    []string `json:"models"`
}

type aiStatusJSON struct {
	Schema  string `json:"schema"`
	Consent struct {
		Enabled   bool       `json:"enabled"`
		ChangedAt *time.Time `json:"changed_at,omitempty"`
		ChangedBy string     `json:"changed_by,omitempty"`
	} `json:"consent"`
	MaxConcurrentJobs int `json:"max_concurrent_jobs"`
	Budget            struct {
		MonthlyMicroUSD   int64                    `json:"monthly_micro_usd"`
		SpentMicroUSD     int64                    `json:"spent_micro_usd"`
		RemainingMicroUSD int64                    `json:"remaining_micro_usd"`
		MonthStart        time.Time                `json:"month_start"`
		Calls             int                      `json:"calls"`
		ByProvider        []remote.AIProviderSpend `json:"by_provider"`
	} `json:"budget"`
	Providers []aiProviderJSON `json:"providers"`
	Project   struct {
		AutoTranslateLocales []string              `json:"auto_translate_locales"`
		NamespaceTags        map[string][]string   `json:"namespace_tags"`
		Review               remote.AIReviewPolicy `json:"review"`
	} `json:"project"`
}

func runAI(ctx context.Context, inv *invocation, args []string) error {
	fs := inv.flags("ai status")
	pos, err := inv.parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] != "status" {
		return usageError(inv.name, "ai takes one action: status")
	}
	p, err := inv.connect(ctx)
	if err != nil {
		return err
	}
	settings, err := p.client.AISettings(ctx, p.scope.Tenant)
	if err != nil {
		return inv.m2Error(err, "can't read the AI settings")
	}
	budget, err := p.client.AIBudget(ctx, p.scope.Tenant)
	if err != nil {
		return inv.m2Error(err, "can't read the AI budget")
	}
	providers, err := p.client.AIProviders(ctx, p.scope.Tenant)
	if err != nil {
		return inv.m2Error(err, "can't list the AI providers")
	}
	ps, err := p.client.ProjectAISettings(ctx, p.scope)
	if err != nil {
		return inv.m2Error(err, "can't read the project's AI settings")
	}
	out := aiStatusJSON{Schema: "glossa.cli.ai.status/v1", MaxConcurrentJobs: settings.MaxConcurrentJobs, Providers: []aiProviderJSON{}}
	out.Consent.Enabled, out.Consent.ChangedAt, out.Consent.ChangedBy = settings.ProviderConsent, settings.ConsentChangedAt, derefStr(settings.ConsentChangedBy)
	out.Budget.MonthlyMicroUSD, out.Budget.SpentMicroUSD, out.Budget.RemainingMicroUSD = budget.MonthlyBudgetMicroUsd, budget.SpentMicroUsd, budget.RemainingMicroUsd
	out.Budget.MonthStart, out.Budget.Calls, out.Budget.ByProvider = budget.MonthStart, budget.Calls, nonNilList(budget.ByProvider)
	for _, pr := range providers {
		out.Providers = append(out.Providers, aiProviderJSON{Name: pr.Name, Kind: string(pr.Kind), Enabled: pr.Enabled,
			APIKeySet: pr.ApiKeySet, BaseURL: derefStr(pr.BaseUrl), Models: nonNilList(pr.Models)})
	}
	out.Project.AutoTranslateLocales = nonNilList(ps.AutoTranslateLocales)
	out.Project.NamespaceTags = map[string][]string{}
	for ns, tags := range ps.NamespaceTags {
		for _, t := range tags {
			out.Project.NamespaceTags[ns] = append(out.Project.NamespaceTags[ns], string(t))
		}
	}
	out.Project.Review = ps.Review
	return inv.emit(out, func(pr *printer) { printAIStatus(pr, out, string(p.info.Slug)) })
}

func printAIStatus(pr *printer, s aiStatusJSON, project string) {
	if s.Consent.Enabled {
		by := ""
		if s.Consent.ChangedAt != nil {
			by = fmt.Sprintf(" (since %s by %s)", when(*s.Consent.ChangedAt), dash(s.Consent.ChangedBy))
		}
		pr.line("%s provider consent on%s", pr.pass(), by)
	} else {
		pr.line("%s provider consent off: no text is sent to AI providers (only exact translation-memory matches are reused)", pr.caution())
	}
	b := s.Budget
	mark := pr.pass()
	if b.MonthlyMicroUSD == 0 || b.RemainingMicroUSD <= 0 {
		mark = pr.caution()
	}
	pr.line("%s budget %s this month, %s spent, %s left (%s since %s)", mark, usd(b.MonthlyMicroUSD), usd(b.SpentMicroUSD),
		usd(b.RemainingMicroUSD), plural(b.Calls, "call", "calls"), b.MonthStart.UTC().Format("2006-01-02"))
	for _, sp := range b.ByProvider {
		pr.line("    %s/%s  %s, %s", sp.Provider, sp.Model, plural(sp.Calls, "call", "calls"), usd(sp.CostMicroUsd))
	}
	if len(s.Providers) == 0 {
		pr.line("%s no AI providers configured", pr.caution())
	} else {
		rows := [][]string{{"  PROVIDER", "KIND", "ENABLED", "KEY", "MODELS"}}
		for _, p := range s.Providers {
			key := "not set"
			if p.APIKeySet {
				key = "set"
			}
			rows = append(rows, []string{"  " + p.Name, p.Kind, fmt.Sprint(p.Enabled), key, dash(strings.Join(p.Models, ", "))})
		}
		pr.table(rows)
	}
	pr.line("%s %s", pr.bold("Project"), project)
	pr.line("  auto-translate  %s", dash(strings.Join(s.Project.AutoTranslateLocales, ", ")))
	var tags []string
	for ns, ts := range s.Project.NamespaceTags {
		tags = append(tags, ns+": "+strings.Join(ts, ", "))
	}
	sort.Strings(tags)
	pr.line("  namespaces      %s", dash(strings.Join(tags, "; ")))
	r := s.Project.Review
	auto := "off"
	if r.AutoApprove {
		auto = fmt.Sprintf("from %.2f", r.AutoApproveMin)
	}
	pr.line("  review          approve recommended from %.2f, auto-approve %s", r.RecommendMin, auto)
}
