package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"go.klarlabs.de/statekit"
	"go.klarlabs.de/statekit/lint"
)

// SchemaV1 is the only definition schema in M5.
const SchemaV1 = "glossa.workflow/v1"

// Limits on a definition (RFC 0006 §9.6).
const (
	// MaxStates bounds a chart.
	MaxStates = 200
	// MaxDocumentBytes bounds the saved document.
	MaxDocumentBytes = 256 << 10
)

var (
	namePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	localPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// ErrInvalidWorkflow is a definition the platform refuses to save. Its
// errors are *InvalidError, which carries every finding (422
// invalid_workflow at the API, wave 2).
var ErrInvalidWorkflow = errors.New("workflow: invalid definition")

// Severities of a Finding. Errors and warnings refuse a save; infos are
// kept with the version for the editor to show.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info"
)

// Rules a Finding may carry besides statekit's lint rules (unreachable,
// dead-end, non-determinism, …), which keep statekit's names.
const (
	RuleJSON              = "json"
	RuleSchema            = "schema"
	RuleEnvelope          = "envelope"
	RuleUnknownPrimitive  = "unknown-primitive"
	RuleParams            = "params"
	RuleSubject           = "subject"
	RuleChart             = "chart"
	RuleUnknownEvent      = "unknown-event"
	RuleDelayedTransition = "delayed-transition"
	RuleUndeclared        = "undeclared-name"
	RuleUnusedDeclaration = "unused-declaration"
	RuleNoFinalState      = "no-final-state"
	RuleTooManyStates     = "too-many-states"
	RuleStatechart        = "statechart"
)

// Finding is one thing wrong with, or worth knowing about, a definition.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	// Path locates it in the document ("guards.two_approvals",
	// "chart.states.reviewing"); empty for the whole document.
	Path    string `json:"path,omitempty"`
	State   string `json:"state,omitempty"`
	Event   string `json:"event,omitempty"`
	Message string `json:"message"`
}

func (f Finding) String() string {
	loc := f.Path
	if loc == "" {
		loc = "(definition)"
	}
	return fmt.Sprintf("[%s] %s: %s (%s)", f.Severity, loc, f.Message, f.Rule)
}

// refuses reports whether f stops a save.
func (f Finding) refuses() bool { return f.Severity == SeverityError || f.Severity == SeverityWarning }

// InvalidError is a refused definition with everything wrong with it.
type InvalidError struct{ Findings []Finding }

func (e *InvalidError) Error() string {
	parts := make([]string, 0, len(e.Findings))
	for _, f := range e.Findings {
		if f.refuses() {
			parts = append(parts, f.String())
		}
	}
	return ErrInvalidWorkflow.Error() + ": " + strings.Join(parts, "; ")
}

// Is makes errors.Is(err, ErrInvalidWorkflow) hold.
func (e *InvalidError) Is(target error) bool { return target == ErrInvalidWorkflow }

// Bound is one of a definition's local names bound to a primitive.
type Bound struct {
	Use    string
	Params any
}

// Definition is a compiled, linted glossa.workflow/v1 document: what one
// stored version loads into. It is immutable.
type Definition struct {
	Schema  string
	Name    string
	Subject SubjectKind
	// Document is the document as it is stored: the bytes that were
	// saved, compacted.
	Document json.RawMessage
	Guards   map[string]Bound
	Actions  map[string]Bound
	// Findings are statekit's informational notes; anything worse
	// refused the save.
	Findings []Finding

	machine  *statekit.MachineConfig[Step]
	guardFns map[string]GuardFunc
}

// Compile loads a definition document. It is lint-on-save: the same
// call that a save makes, and a document it refuses is never stored.
//
// Loading is per schema version — the document's "schema" picks the
// loader — so a version stored under glossa.workflow/v1 keeps loading
// exactly as it did after a v2 exists. Each call builds a fresh
// statekit.ActionRegistry for this document alone, whose entries are
// the vocabulary's primitives closed over the document's parameters,
// and hands it to statekit.FromJSON, whose strict resolution makes any
// name the registry does not hold a load error.
func Compile(doc []byte) (*Definition, error) {
	if len(doc) > MaxDocumentBytes {
		return nil, invalid(Finding{Rule: RuleEnvelope, Severity: SeverityError,
			Message: fmt.Sprintf("the document is %d bytes; at most %d", len(doc), MaxDocumentBytes)})
	}
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(doc, &head); err != nil {
		return nil, invalid(Finding{Rule: RuleJSON, Severity: SeverityError, Message: jsonMessage(err)})
	}
	load, ok := loaders[head.Schema]
	if !ok {
		return nil, invalid(Finding{Rule: RuleSchema, Severity: SeverityError, Path: "schema",
			Message: fmt.Sprintf("schema must be one of %s, not %q", strings.Join(Schemas(), ", "), head.Schema)})
	}
	return load(doc)
}

// loaders are the definition schemas the platform reads, by name.
var loaders = map[string]func([]byte) (*Definition, error){SchemaV1: compileV1}

// Schemas lists the schemas Compile reads.
func Schemas() []string { return slices.Sorted(maps.Keys(loaders)) }

func invalid(fs ...Finding) error { return &InvalidError{Findings: fs} }

func jsonMessage(err error) string { return strings.TrimPrefix(err.Error(), "json: ") }

// ── glossa.workflow/v1 ──────────────────────────────────────────────

type envelopeV1 struct {
	Schema  string                     `json:"schema"`
	Name    string                     `json:"name"`
	Subject SubjectKind                `json:"subject"`
	Chart   json.RawMessage            `json:"chart"`
	Guards  map[string]json.RawMessage `json:"guards"`
	Actions map[string]json.RawMessage `json:"actions"`
}

// chartV1 is the part of statekit's Native JSON a definition may use:
// nested states with transitions, entry and exit actions, and tags.
// Everything else Native JSON can say is refused by name — delayed
// transitions (§2.3), invoked services and eventless transitions (a
// definition reaches outside the vocabulary or loops on its own),
// history and parallel states (an instance's stored snapshot is one
// state id), and the flat and XState "on" spellings (one way to write a
// chart is enough to lint, diff and review).
type chartV1 struct {
	ID      string              `json:"id"`
	Initial string              `json:"initial"`
	States  map[string]*stateV1 `json:"states"`
}

type stateV1 struct {
	ID          string              `json:"id"`
	Type        string              `json:"type"`
	Initial     string              `json:"initial"`
	States      map[string]*stateV1 `json:"states"`
	Transitions []transitionV1      `json:"transitions"`
	Entry       []string            `json:"entry"`
	Exit        []string            `json:"exit"`
	Tags        []string            `json:"tags"`
}

type transitionV1 struct {
	Event   string   `json:"event"`
	Target  string   `json:"target"`
	Guard   string   `json:"guard"`
	Actions []string `json:"actions"`
}

// delayKeys are how Native JSON (and XState) spell a delayed transition.
var delayKeys = []string{"after", "isDelayed", "delayMs"}

type compiler struct {
	findings []Finding
	subject  SubjectKind
	guards   map[string]Bound
	actions  map[string]Bound
	guardFns map[string]GuardFunc
	used     map[string]bool // "guards.x" / "actions.y"
	// tried are declarations that were refused: a use of one is not
	// reported again as undeclared.
	tried map[string]bool
}

func (c *compiler) add(rule, path, msg string) {
	c.findings = append(c.findings, Finding{Rule: rule, Severity: SeverityError, Path: path, Message: msg})
}

func (c *compiler) refused() bool { return slices.ContainsFunc(c.findings, Finding.refuses) }

func compileV1(doc []byte) (*Definition, error) {
	var env envelopeV1
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return nil, invalid(Finding{Rule: RuleEnvelope, Severity: SeverityError, Message: jsonMessage(err)})
	}
	c := &compiler{subject: env.Subject, guards: map[string]Bound{}, actions: map[string]Bound{},
		guardFns: map[string]GuardFunc{}, used: map[string]bool{}, tried: map[string]bool{}}

	if !namePattern.MatchString(env.Name) {
		c.add(RuleEnvelope, "name", fmt.Sprintf("name must be lowercase letters, digits and dashes, at most %d characters, not %q", maxNameLength, env.Name))
	}
	if !env.Subject.Valid() {
		c.add(RuleEnvelope, "subject", fmt.Sprintf("subject must be translation or release_request, not %q", env.Subject))
	}
	c.declare(env)
	chart := c.readChart(env.Chart)
	if chart != nil {
		c.checkChart(chart)
	}
	if c.refused() {
		return nil, invalid(c.findings...)
	}

	machine, err := c.build(env.Chart)
	if err != nil {
		return nil, err
	}
	c.lint(machine)
	if c.refused() {
		return nil, invalid(c.findings...)
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, doc); err != nil {
		return nil, invalid(Finding{Rule: RuleJSON, Severity: SeverityError, Message: jsonMessage(err)})
	}
	return &Definition{
		Schema: SchemaV1, Name: env.Name, Subject: env.Subject, Document: compact.Bytes(),
		Guards: c.guards, Actions: c.actions, Findings: c.findings, machine: machine, guardFns: c.guardFns,
	}, nil
}

// declare binds every local guard and action name to its primitive and
// compiles the parameters.
func (c *compiler) declare(env envelopeV1) {
	for _, name := range slices.Sorted(maps.Keys(env.Guards)) {
		path := "guards." + name
		c.tried[path] = true
		use, params, ok := c.entry(path, name, env.Guards[name])
		if !ok {
			continue
		}
		p, known := guardPrimitives[use]
		if !known {
			c.add(RuleUnknownPrimitive, path, fmt.Sprintf("uses %q, which is not a guard the platform has; guards are %s",
				use, strings.Join(GuardPrimitives(), ", ")))
			continue
		}
		if !c.fits(path, use, p.subjects) {
			continue
		}
		fn, typed, err := p.compile(params)
		if err != nil {
			c.add(RuleParams, path, err.Error())
			continue
		}
		c.guards[name], c.guardFns[name] = Bound{Use: use, Params: typed}, fn
	}
	for _, name := range slices.Sorted(maps.Keys(env.Actions)) {
		path := "actions." + name
		c.tried[path] = true
		use, params, ok := c.entry(path, name, env.Actions[name])
		if !ok {
			continue
		}
		p, known := actionPrimitives[use]
		if !known {
			c.add(RuleUnknownPrimitive, path, fmt.Sprintf("uses %q, which is not an action the platform has; actions are %s",
				use, strings.Join(ActionPrimitives(), ", ")))
			continue
		}
		if !c.fits(path, use, p.subjects) {
			continue
		}
		typed, err := p.compile(params)
		if err != nil {
			c.add(RuleParams, path, err.Error())
			continue
		}
		c.actions[name] = Bound{Use: use, Params: typed}
	}
}

// entry splits a declaration into its "use" and the rest, its
// parameters.
func (c *compiler) entry(path, name string, raw json.RawMessage) (string, []byte, bool) {
	if !localPattern.MatchString(name) {
		c.add(RuleEnvelope, path, "a local name must be lowercase letters, digits and underscores, starting with a letter")
		return "", nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		c.add(RuleEnvelope, path, `must be an object with "use" and the primitive's parameters`)
		return "", nil, false
	}
	var use string
	if err := json.Unmarshal(fields["use"], &use); err != nil || use == "" {
		c.add(RuleEnvelope, path, `must say which primitive it uses, as "use"`)
		return "", nil, false
	}
	delete(fields, "use")
	params, _ := json.Marshal(fields)
	return use, params, true
}

func (c *compiler) fits(path, use string, subjects []SubjectKind) bool {
	if c.subject.Valid() && !slices.Contains(subjects, c.subject) {
		c.add(RuleSubject, path, fmt.Sprintf("%s does not apply to a %s", use, c.subject))
		return false
	}
	return true
}

// readChart refuses delayed transitions by name, then reads the chart
// strictly.
func (c *compiler) readChart(raw json.RawMessage) *chartV1 {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		c.add(RuleEnvelope, "chart", "a definition needs a chart")
		return nil
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		c.add(RuleChart, "chart", jsonMessage(err))
		return nil
	}
	before := len(c.findings)
	c.findDelays(generic, "chart")
	if len(c.findings) > before {
		return nil
	}
	var chart chartV1
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&chart); err != nil {
		c.add(RuleChart, "chart", jsonMessage(err)+
			"; a chart has id, initial and states, and a state has type, initial, states, transitions, entry, exit and tags")
		return nil
	}
	return &chart
}

// findDelays reports every delayed transition anywhere in the chart.
// statekit's After timers live in the interpreter's process and die with
// it; a due date is a stored due_at and a timer.* event from the sweep,
// so a restart can never lose one (RFC 0006 §2.3, §14 decision 3).
func (c *compiler) findDelays(v any, path string) {
	switch x := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			if slices.Contains(delayKeys, k) {
				c.findings = append(c.findings, Finding{Rule: RuleDelayedTransition, Severity: SeverityError, Path: path + "." + k,
					Message: "delayed transitions are refused: a timer in the chart dies with the process. " +
						"Give the assignment or approval a due period and react to timer.due or timer.overdue instead"})
				continue
			}
			c.findDelays(x[k], path+"."+k)
		}
	case []any:
		for i, e := range x {
			c.findDelays(e, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}

// checkChart checks what statekit does not: that every event is in the
// vocabulary and concerns this subject, that every name is declared and
// every declaration used, and that the chart has somewhere to finish.
func (c *compiler) checkChart(chart *chartV1) {
	if chart.ID == "" {
		c.add(RuleChart, "chart.id", "a chart needs an id")
	}
	if _, ok := chart.States[chart.Initial]; !ok {
		c.add(RuleChart, "chart.initial", fmt.Sprintf("initial %q is not a top-level state", chart.Initial))
	}
	seen, finals := map[string]bool{}, 0
	var walk func(states map[string]*stateV1, path string)
	walk = func(states map[string]*stateV1, path string) {
		for _, id := range slices.Sorted(maps.Keys(states)) {
			s, p := states[id], path+"."+id
			if s == nil {
				c.add(RuleChart, p, "a state must be an object")
				continue
			}
			if seen[id] {
				c.add(RuleChart, p, fmt.Sprintf("state id %q is used twice; ids are unique across the whole chart", id))
			}
			seen[id] = true
			if s.ID != "" && s.ID != id {
				c.add(RuleChart, p+".id", fmt.Sprintf("id %q differs from its key %q", s.ID, id))
			}
			c.checkState(s, id, p)
			if s.Type == "final" {
				finals++
			}
			walk(s.States, p+".states")
		}
	}
	walk(chart.States, "chart.states")
	if len(seen) > MaxStates {
		c.add(RuleTooManyStates, "chart.states", fmt.Sprintf("the chart has %d states; at most %d", len(seen), MaxStates))
	}
	if finals == 0 {
		c.add(RuleNoFinalState, "chart.states",
			"the chart has no final state, so no instance could ever finish; mark where the work is done with type final")
	}
	for _, kind := range []string{"guards", "actions"} {
		declared := slices.Collect(maps.Keys(c.guards))
		if kind == "actions" {
			declared = slices.Collect(maps.Keys(c.actions))
		}
		slices.Sort(declared)
		for _, name := range declared {
			if !c.used[kind+"."+name] {
				c.findings = append(c.findings, Finding{Rule: RuleUnusedDeclaration, Severity: SeverityWarning,
					Path: kind + "." + name, Message: "is declared but the chart never uses it"})
			}
		}
	}
}

func (c *compiler) checkState(s *stateV1, id, p string) {
	switch s.Type {
	case "", "atomic", "compound":
	case "final":
		if len(s.Transitions) > 0 || len(s.States) > 0 {
			c.add(RuleChart, p, "a final state has no transitions and no child states")
		}
	default:
		c.add(RuleChart, p+".type", fmt.Sprintf("type must be atomic, compound or final, not %q", s.Type))
	}
	c.useActions(s.Entry, p+".entry")
	c.useActions(s.Exit, p+".exit")
	for i, t := range s.Transitions {
		tp := fmt.Sprintf("%s.transitions[%d]", p, i)
		subjects, known := events[EventName(t.Event)]
		switch {
		case t.Event == "":
			c.add(RuleUnknownEvent, tp+".event", "a transition needs an event; eventless transitions are refused")
		case !known:
			c.findings = append(c.findings, Finding{Rule: RuleUnknownEvent, Severity: SeverityError, Path: tp + ".event",
				State: id, Event: t.Event, Message: fmt.Sprintf("%q is not an event the platform raises; events are %s", t.Event, eventList())})
		case c.subject.Valid() && !slices.Contains(subjects, c.subject):
			c.add(RuleSubject, tp+".event", fmt.Sprintf("%s never concerns a %s", t.Event, c.subject))
		}
		if t.Target == "" {
			c.add(RuleChart, tp+".target", "a transition needs a target")
		}
		if t.Guard != "" {
			c.use("guards", t.Guard, tp+".guard")
		}
		c.useActions(t.Actions, tp+".actions")
	}
}

func (c *compiler) useActions(names []string, path string) {
	for _, n := range names {
		c.use("actions", n, path)
	}
}

func (c *compiler) use(kind, name, path string) {
	c.used[kind+"."+name] = true
	_, ok := c.guards[name]
	if kind == "actions" {
		_, ok = c.actions[name]
	}
	if !ok && !c.tried[kind+"."+name] {
		c.add(RuleUndeclared, path, fmt.Sprintf("%q is not declared under %s", name, kind))
	}
}

func eventList() string {
	names := make([]string, 0, len(events))
	for _, e := range Events() {
		names = append(names, string(e))
	}
	return strings.Join(names, ", ")
}

// build hands the chart to statekit.FromJSON with a registry built for
// this document alone.
func (c *compiler) build(chart json.RawMessage) (*statekit.MachineConfig[Step], error) {
	reg := statekit.NewActionRegistry[Step]()
	for name, fn := range c.guardFns {
		reg.WithGuard(statekit.GuardType(name), func(s Step, _ statekit.Event) bool { return fn(s) })
	}
	for name, b := range c.actions {
		effect := Effect{Name: name, Use: b.Use, Params: b.Params}
		reg.WithAction(statekit.ActionType(name), func(s *Step, _ statekit.Event) {
			s.Effects = append(s.Effects, effect)
		})
	}
	machine, err := statekit.FromJSON[Step](chart, reg)
	if err != nil {
		return nil, invalid(Finding{Rule: RuleStatechart, Severity: SeverityError, Path: "chart", Message: err.Error()})
	}
	// statekit honours a delay however the chart spelt it. readChart
	// refuses every spelling it knows; this refuses the rest.
	for id, s := range machine.States {
		for _, t := range s.Transitions {
			if t.Delay != 0 {
				return nil, invalid(Finding{Rule: RuleDelayedTransition, Severity: SeverityError, State: string(id),
					Path: "chart.states." + string(id), Message: "delayed transitions are refused"})
			}
		}
	}
	return machine, nil
}

// lint runs statekit's linter. Errors and warnings — unreachable states,
// non-final dead ends, non-deterministic transitions — refuse the save
// (§2.3); infos are kept.
func (c *compiler) lint(machine *statekit.MachineConfig[Step]) {
	for _, d := range lint.Lint(machine).Diagnostics {
		f := Finding{Rule: d.Rule, Severity: d.Severity.String(), State: string(d.State), Event: string(d.Event), Message: d.Message}
		if d.State != "" {
			f.Path = "chart.states." + string(d.State)
		}
		c.findings = append(c.findings, f)
	}
}

// ── stepping ────────────────────────────────────────────────────────

// Start enters the chart's initial state, running its entry actions,
// and returns the state it settled in and the effects it asked for.
func (d *Definition) Start(step Step) (string, Step) {
	interp := statekit.NewInterpreter(d.machine)
	defer func() { _ = interp.Close() }()
	interp.UpdateContext(func(s *Step) { *s = step })
	interp.Start()
	st := interp.State()
	return string(st.Value), st.Context
}

// Advance restores an instance in state and sends it step's event. It
// reports false when the event no longer applies there — no transition,
// or every guard said no — which the runner records as ignored, never
// as an error ("the world moved on" is normal, §2.5).
func (d *Definition) Advance(state string, step Step) (string, Step, bool, error) {
	interp := statekit.NewInterpreter(d.machine)
	defer func() { _ = interp.Close() }()
	if err := interp.Restore(statekit.Snapshot[Step]{
		MachineID: d.machine.ID, CurrentState: statekit.StateID(state), Context: step,
	}); err != nil {
		return "", Step{}, false, fmt.Errorf("workflow: restore %q: %w", state, err)
	}
	if !interp.SendResult(statekit.Event{Type: statekit.EventType(step.Trigger.Event)}) {
		return state, step, false, nil
	}
	st := interp.State()
	return string(st.Value), st.Context, true, nil
}

// Final reports whether state is one of the chart's final states.
func (d *Definition) Final(state string) bool {
	s := d.machine.GetState(statekit.StateID(state))
	return s != nil && s.IsFinal()
}

// States lists the chart's state ids, sorted.
func (d *Definition) States() []string {
	out := make([]string, 0, len(d.machine.States))
	for id := range d.machine.States {
		out = append(out, string(id))
	}
	slices.Sort(out)
	return out
}
