package app

import (
	"context"
	"encoding/json"
	"fmt"

	"go.klarlabs.de/agent/domain/tool"
	agentapi "go.klarlabs.de/agent/interfaces/api"
	"go.klarlabs.de/axi"
	axidomain "go.klarlabs.de/axi/domain"
)

// The toolset is an axi-go kernel per job: every tool is an axi action
// with a typed input contract and an effect profile (the knowledge tools
// and validate read locally; draft and assess read externally — they send
// the message to a provider), and every execution is an axi session with
// its evidence. agent-go's engine sees each action as a tool through a
// small adapter (agent-go has no built-in axi-action bridge; see the
// follow-ups).

type toolSpec struct {
	name        string
	description string
	fields      []axidomain.ContractField
	effect      axidomain.EffectLevel
	// external tools call a model provider: a side effect for agent-go,
	// so its state machine only allows them in the act state.
	external bool
	run      func(ctx context.Context, input json.RawMessage) (any, error)
}

func specs(j *job) []toolSpec {
	return []toolSpec{
		{
			name: ToolTMLookup, description: "Exact and fuzzy translation-memory matches of the source, with scores and provenance.",
			effect: axidomain.EffectReadLocal,
			run:    func(ctx context.Context, _ json.RawMessage) (any, error) { return j.tmLookup(ctx) },
		},
		{
			name: ToolTermLookup, description: "Concepts recognized in the source with the target locale's terms and their status.",
			effect: axidomain.EffectReadLocal,
			run:    func(ctx context.Context, _ json.RawMessage) (any, error) { return j.termLookup(ctx) },
		},
		{
			name: ToolStyleRules, description: "The effective style guide for the target locale and namespace.",
			effect: axidomain.EffectReadLocal,
			run:    func(ctx context.Context, _ json.RawMessage) (any, error) { return j.styleRules(ctx) },
		},
		{
			name: ToolMessageContext, description: "Description, max length, arguments, usages, neighbouring messages and the target's plural categories.",
			effect: axidomain.EffectReadLocal,
			run:    func(ctx context.Context, _ json.RawMessage) (any, error) { return j.messageContext(ctx) },
		},
		{
			name: ToolValidate, description: "Structural check (MessageFormat CheckCompat), terminology QA and max length of a candidate.",
			effect: axidomain.EffectNone,
			fields: []axidomain.ContractField{
				{Name: "translation", Type: "string", Description: "Candidate in MF2 syntax."},
				{Name: "origin", Type: "string", Required: true, Description: "translation_memory or draft."},
			},
			run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var in validateInput
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				return j.validate(ctx, in)
			},
		},
		{
			name: ToolDraft, description: "Drafts (or repairs) the translation with a model provider from the gathered knowledge.",
			effect: axidomain.EffectReadExternal, external: true,
			fields: []axidomain.ContractField{{Name: "knowledge", Type: "object", Required: true, Description: "The gather tools' results."}},
			run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var in draftInput
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				return j.draft(ctx, in)
			},
		},
		{
			name: ToolAssess, description: "A model's calibrated self-assessment of a valid draft (a light confidence signal).",
			effect: axidomain.EffectReadExternal, external: true,
			fields: []axidomain.ContractField{
				{Name: "translation", Type: "string", Required: true},
				{Name: "knowledge", Type: "object", Required: true},
			},
			run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var in assessInput
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				return j.assess(ctx, in)
			},
		},
	}
}

// executor adapts a tool function to an axi action executor. The JSON
// round trip keeps the action's input exactly what the planner sent.
type executor struct{ spec toolSpec }

func (e executor) Execute(ctx context.Context, input any, _ axidomain.CapabilityInvoker) (axidomain.ExecutionResult, []axidomain.EvidenceRecord, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return axidomain.ExecutionResult{}, nil, err
	}
	out, err := e.spec.run(ctx, raw)
	if err != nil {
		return axidomain.ExecutionResult{}, nil, err
	}
	var evidence []axidomain.EvidenceRecord
	if c, ok := callOf(out); ok {
		evidence = append(evidence, axidomain.EvidenceRecord{
			Kind: "provider.call", Source: "glossa.intelligence." + e.spec.name,
			Value:      map[string]any{"provider": c.Provider, "model": c.Model, "prompt_version": c.PromptVersion, "system_sha256": c.Sent.SystemSHA256},
			TokensUsed: c.Usage.InputTokens + c.Usage.OutputTokens,
		})
	}
	return axidomain.ExecutionResult{Data: out, ContentType: "application/json", Summary: e.spec.name + " done"}, evidence, nil
}

func callOf(out any) (callOutput, bool) {
	switch o := out.(type) {
	case draftOutput:
		return o.callOutput, true
	case assessOutput:
		return o.callOutput, o.Provider != ""
	}
	return callOutput{}, false
}

// newTools registers the job's actions on a fresh axi kernel and returns
// them as agent-go tools.
func newTools(j *job) ([]tool.Tool, error) {
	kernel := axi.New()
	all := specs(j)
	actions := make([]*axidomain.ActionDefinition, 0, len(all))
	executors := map[axidomain.ActionExecutorRef]axidomain.ActionExecutor{}
	for _, s := range all {
		a, err := axidomain.NewActionDefinition(axidomain.ActionName(s.name), s.description,
			axidomain.NewContract(s.fields), axidomain.EmptyContract(), axidomain.RequirementSet{},
			axidomain.EffectProfile{Level: s.effect}, axidomain.IdempotencyProfile{IsIdempotent: !s.external})
		if err != nil {
			return nil, err
		}
		ref := axidomain.ActionExecutorRef("exec." + s.name)
		if err := a.BindExecutor(ref); err != nil {
			return nil, err
		}
		actions = append(actions, a)
		executors[ref] = executor{spec: s}
	}
	contribution, err := axidomain.NewPluginContribution("glossa.intelligence.translation", actions, nil)
	if err != nil {
		return nil, err
	}
	bundle, err := axidomain.NewPluginBundle(contribution, executors, nil)
	if err != nil {
		return nil, err
	}
	if err := kernel.RegisterBundle(bundle); err != nil {
		return nil, err
	}

	tools := make([]tool.Tool, 0, len(all))
	for _, s := range all {
		ann := agentapi.Annotations{ReadOnly: !s.external, Idempotent: !s.external, RiskLevel: agentapi.RiskLow}
		if s.external {
			ann.RiskLevel = agentapi.RiskMedium
		}
		t, err := agentapi.NewToolBuilder(s.name).
			WithDescription(s.description).
			WithAnnotations(ann).
			WithHandler(axiHandler(kernel, s.name)).
			Build()
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, nil
}

func axiHandler(kernel *axi.Kernel, action string) tool.Handler {
	return func(ctx context.Context, input json.RawMessage) (tool.Result, error) {
		in := map[string]any{}
		if len(input) > 0 && string(input) != "null" {
			if err := json.Unmarshal(input, &in); err != nil {
				return tool.Result{}, fmt.Errorf("%s: input: %w", action, err)
			}
		}
		res, err := kernel.Execute(ctx, axi.Invocation{Action: action, Input: in})
		if err != nil {
			return tool.Result{}, fmt.Errorf("%s: %w", action, err)
		}
		if res.Status != axidomain.StatusSucceeded || res.Result == nil {
			msg := string(res.Status)
			if res.Failure != nil {
				msg = res.Failure.Code + ": " + res.Failure.Message
			}
			return tool.Result{}, fmt.Errorf("%s: %s", action, msg)
		}
		out, err := json.Marshal(res.Result.Data)
		if err != nil {
			return tool.Result{}, err
		}
		return tool.NewResult(out), nil
	}
}
