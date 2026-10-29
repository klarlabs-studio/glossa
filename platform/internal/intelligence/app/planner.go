package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	agentdomain "go.klarlabs.de/agent/domain/agent"
	agentapi "go.klarlabs.de/agent/interfaces/api"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// The translation agent runs RFC 0003 §3.2's phases on agent-go's
// canonical state machine (agent-go v0.16 has custom states in its
// machine builder but its engine only runs the canonical graph):
//
//	gather   = explore:  tm_lookup, term_lookup, style_rules, message_context
//	           decide:   an exact TM hit is validated and, if clean, reused
//	draft    = act:      draft (the only way a message reaches a provider)
//	validate = validate: validate — the structural hard gate
//	repair   = validate → explore → decide → act: draft with the findings, ≤ 2×
//	assess   = validate → explore → decide → act: assess the valid draft
//	done     = validate → done (or decide → done for TM reuse)
//
// The planner is deterministic Go, not a model: the model writes the
// translation, never the plan. Its decisions depend only on the evidence
// (the tools' outputs), so a run can be replayed from its ledger.

// Failure reasons the planner ends a run with.
const (
	reasonInvalidOutput   = "invalid_output"
	reasonProviderConsent = "provider_consent"
)

// eligibility confines each tool to its phase; agent-go also refuses the
// side-effecting draft and assess outside act, whatever this says.
func eligibility() agentapi.EligibilityRules {
	return agentapi.EligibilityRules{
		agentapi.StateExplore:  {ToolTMLookup, ToolTermLookup, ToolStyleRules, ToolMessageContext},
		agentapi.StateDecide:   {ToolValidate},
		agentapi.StateAct:      {ToolDraft, ToolAssess},
		agentapi.StateValidate: {ToolValidate},
	}
}

type planner struct {
	consent    bool
	maxRepairs int
	assess     bool
}

// trail is the run's evidence, decoded.
type trail struct {
	tm       *tmOutput
	terms    *termOutput
	style    *styleOutput
	context  *contextOutput
	tmCheck  *validateOutput
	drafts   []draftOutput
	checks   []validateOutput
	assessed *assessOutput
}

func readTrail(evidence []agentdomain.Evidence) (trail, error) {
	var t trail
	for _, e := range evidence {
		if e.Type != agentdomain.EvidenceToolResult {
			continue
		}
		var err error
		switch e.Source {
		case ToolTMLookup:
			t.tm = new(tmOutput)
			err = json.Unmarshal(e.Content, t.tm)
		case ToolTermLookup:
			t.terms = new(termOutput)
			err = json.Unmarshal(e.Content, t.terms)
		case ToolStyleRules:
			t.style = new(styleOutput)
			err = json.Unmarshal(e.Content, t.style)
		case ToolMessageContext:
			t.context = new(contextOutput)
			err = json.Unmarshal(e.Content, t.context)
		case ToolValidate:
			var v validateOutput
			if err = json.Unmarshal(e.Content, &v); err == nil {
				if v.Origin == string(domain.OriginTranslationMemory) {
					t.tmCheck = &v
				} else {
					t.checks = append(t.checks, v)
				}
			}
		case ToolDraft:
			var d draftOutput
			if err = json.Unmarshal(e.Content, &d); err == nil {
				t.drafts = append(t.drafts, d)
			}
		case ToolAssess:
			t.assessed = new(assessOutput)
			err = json.Unmarshal(e.Content, t.assessed)
		}
		if err != nil {
			return trail{}, fmt.Errorf("evidence from %s: %w", e.Source, err)
		}
	}
	return t, nil
}

func (t trail) knowledge() knowledge {
	var k knowledge
	if t.tm != nil {
		k.TM = *t.tm
	}
	if t.terms != nil {
		k.Terms = *t.terms
	}
	if t.style != nil {
		k.Style = *t.style
	}
	if t.context != nil {
		k.Context = *t.context
	}
	return k
}

func (t trail) maxLength() *int {
	if t.context == nil {
		return nil
	}
	return t.context.Context.MaxLength
}

// exactTM returns the best exact (100/101) match, if any.
func (t trail) exactTM() *domain.TMMatch {
	if t.tm == nil {
		return nil
	}
	var best *domain.TMMatch
	for i, m := range t.tm.Matches {
		if m.IsExact() && (best == nil || m.Score > best.Score) {
			best = &t.tm.Matches[i]
		}
	}
	return best
}

// tmReusable: an exact hit that is structurally valid, has every plural
// category the target needs and no terminology findings is reused
// without a model call.
func (t trail) tmReusable() bool {
	return t.tmCheck != nil && !t.tmCheck.Structure.NeedsRepair() && len(t.tmCheck.TermFindings) == 0
}

// repairing reports whether the last draft goes back to the model: it
// needs repair (structural errors or missing plural categories) and
// repairs remain.
func (t trail) repairing(maxRepairs int) bool {
	last := t.lastCheck()
	return last != nil && last.Structure.NeedsRepair() && len(t.drafts)-1 < maxRepairs
}

func (t trail) lastCheck() *validateOutput {
	if len(t.checks) == 0 || len(t.checks) < len(t.drafts) {
		return nil
	}
	return &t.checks[len(t.checks)-1]
}

// Plan implements agent-go's Planner.
func (p *planner) Plan(_ context.Context, req agentapi.PlanRequest) (agentapi.Decision, error) {
	t, err := readTrail(req.Evidence)
	if err != nil {
		return agentapi.Decision{}, err
	}
	switch req.CurrentState {
	case agentapi.StateIntake:
		return agentapi.NewTransitionDecision(agentapi.StateExplore, "gather: collect the knowledge about the message"), nil
	case agentapi.StateExplore:
		return p.explore(t), nil
	case agentapi.StateDecide:
		return p.decide(t)
	case agentapi.StateAct:
		return p.act(t)
	case agentapi.StateValidate:
		return p.validate(t)
	}
	return agentapi.Decision{}, fmt.Errorf("translation planner: unexpected state %s", req.CurrentState)
}

func (p *planner) explore(t trail) agentapi.Decision {
	for _, g := range []struct {
		tool string
		done bool
	}{
		{ToolTMLookup, t.tm != nil},
		{ToolTermLookup, t.terms != nil},
		{ToolStyleRules, t.style != nil},
		{ToolMessageContext, t.context != nil},
	} {
		if !g.done {
			return agentapi.NewCallToolDecision(g.tool, []byte(`{}`), "gather: "+g.tool)
		}
	}
	reason := "gather: knowledge complete"
	switch last := t.lastCheck(); {
	case t.repairing(p.maxRepairs):
		reason = fmt.Sprintf("repair %d of %d: %s", len(t.drafts), p.maxRepairs, repairReason(last.Structure))
	case last != nil && last.Valid:
		reason = "assess: the draft passed validation"
	}
	return agentapi.NewTransitionDecision(agentapi.StateDecide, reason)
}

func (p *planner) decide(t trail) (agentapi.Decision, error) {
	if exact := t.exactTM(); exact != nil && t.tmCheck == nil && len(t.drafts) == 0 {
		return call(ToolValidate, validateInput{
			Translation: exact.Target, Origin: string(domain.OriginTranslationMemory), UnitID: exact.UnitID, MaxLength: t.maxLength(),
		}, fmt.Sprintf("decide: check the exact TM match %s (%d) for reuse", exact.UnitID, exact.Score))
	}
	if t.tmReusable() && len(t.drafts) == 0 {
		return finish(domain.OriginTranslationMemory, "done: reuse the exact translation-memory match without a model call")
	}
	if !p.consent {
		return agentapi.NewFailDecision(reasonProviderConsent, nil), nil
	}
	reason := "decide: draft a translation"
	if last := t.lastCheck(); last != nil {
		reason = "decide: assess the valid draft"
		if t.repairing(p.maxRepairs) {
			reason = "decide: repair the draft"
		}
	}
	return agentapi.NewTransitionDecision(agentapi.StateAct, reason), nil
}

func (p *planner) act(t trail) (agentapi.Decision, error) {
	switch last := t.lastCheck(); {
	case len(t.drafts) == 0:
		return call(ToolDraft, draftInput{Knowledge: t.knowledge()}, "draft: translate with the gathered knowledge")
	case last == nil:
		return agentapi.NewTransitionDecision(agentapi.StateValidate, "validate the new draft"), nil
	case t.repairing(p.maxRepairs):
		attempts := make([]attempt, len(t.drafts))
		for i, d := range t.drafts {
			attempts[i] = attempt{Answer: d.Answer, Findings: slices.Concat(t.checks[i].Structure.Errors, t.checks[i].Structure.Warnings)}
		}
		return call(ToolDraft, draftInput{Knowledge: t.knowledge(), Attempts: attempts},
			fmt.Sprintf("repair %d of %d", len(t.drafts), p.maxRepairs))
	case p.assess && t.assessed == nil:
		return call(ToolAssess, assessInput{Translation: last.Structure.Canonical, Knowledge: t.knowledge()}, "assess the valid draft")
	default:
		return agentapi.NewTransitionDecision(agentapi.StateValidate, "done with the model"), nil
	}
}

func (p *planner) validate(t trail) (agentapi.Decision, error) {
	last := t.lastCheck()
	if last == nil {
		d := t.drafts[len(t.drafts)-1]
		in := validateInput{Translation: d.Message, Origin: "draft", Attempt: d.Attempt, MaxLength: t.maxLength(), AnswerError: d.AnswerError}
		return call(ToolValidate, in, fmt.Sprintf("validate draft %d", d.Attempt))
	}
	switch {
	case t.repairing(p.maxRepairs):
		return agentapi.NewTransitionDecision(agentapi.StateExplore,
			fmt.Sprintf("repair %d of %d: %s", len(t.drafts), p.maxRepairs, repairReason(last.Structure))), nil
	case !last.Valid:
		return agentapi.NewFailDecision(reasonInvalidOutput, nil), nil
	case p.assess && t.assessed == nil:
		return agentapi.NewTransitionDecision(agentapi.StateExplore, "assess: the draft passed validation"), nil
	}
	return finish(domain.OriginAI, "done: the draft passed validation")
}

// repairReason says why a draft goes back to the model.
func repairReason(s Structure) string {
	if !s.Valid() {
		return fmt.Sprintf("%d structural errors", len(s.Errors))
	}
	return "missing plural categories " + strings.Join(s.MissingPluralCategories(), ", ")
}

func call(tool string, in any, reason string) (agentapi.Decision, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return agentapi.Decision{}, err
	}
	return agentapi.NewCallToolDecision(tool, raw, reason), nil
}

func finish(origin domain.Origin, summary string) (agentapi.Decision, error) {
	raw, err := json.Marshal(map[string]string{"origin": string(origin)})
	if err != nil {
		return agentapi.Decision{}, err
	}
	return agentapi.NewFinishDecision(summary, raw), nil
}
