package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// The fakes below stand in for the write side of the contexts, and they
// are row-level security again: a project of another tenant is not
// refused, it is not there. Each records what reached it, so a test can
// assert what the tool asked for and not only what it answered.

// ── Catalog ─────────────────────────────────────────────────────────

type fakeMessages struct {
	owner map[uuid.UUID]tenancy.ID
	// wrote is every upsert that got through, in order.
	wrote []tools.MessageUpsert
	rev   int
}

func (f *fakeMessages) UpsertMessage(
	ctx context.Context, project uuid.UUID, in tools.MessageUpsert,
) (tools.MessageWritten, error) {
	if owner, ok := f.owner[project]; !ok || !scoped(ctx, owner) {
		return tools.MessageWritten{}, tools.ErrNotFound
	}
	f.wrote = append(f.wrote, in)
	f.rev++
	ns := ""
	if in.Namespace != nil {
		ns = *in.Namespace
	}
	return tools.MessageWritten{
		MessageSummary: tools.MessageSummary{
			ID: uuid.New().String(), Key: in.Key, Namespace: ns, State: "active",
			Source: in.Text, Syntax: "mf2", Revision: f.rev,
		},
		Status: "created",
	}, nil
}

// ── Localization ────────────────────────────────────────────────────

type fakeProposals struct {
	owner map[uuid.UUID]tenancy.ID
	wrote []tools.TranslationProposal
	// state is what the adapter would have stored. The fake answers
	// needs_review because that is the only thing the real adapter ever
	// asks Localization for; a test that wants proof of *that* runs the
	// real service, in internal/mcp/adapters/sources.
	state string
}

func (f *fakeProposals) ProposeTranslation(
	ctx context.Context, project uuid.UUID, in tools.TranslationProposal,
) (tools.ProposedTranslation, error) {
	if owner, ok := f.owner[project]; !ok || !scoped(ctx, owner) {
		return tools.ProposedTranslation{}, tools.ErrNotFound
	}
	f.wrote = append(f.wrote, in)
	state := f.state
	if state == "" {
		state = "needs_review"
	}
	return tools.ProposedTranslation{
		Translation: tools.Translation{
			MessageID: uuid.New().String(), Key: in.Key, Locale: in.Locale, Text: in.Text,
			Syntax: "mf2", State: state, Origin: "ai", By: "token:1",
			SourceRevision: 3, CurrentSourceRevision: 3, Revision: 1,
		},
		Status: "created",
	}, nil
}

type fakeLocales struct {
	owner map[uuid.UUID]tenancy.ID
	added []string
}

func (f *fakeLocales) AddLocale(ctx context.Context, project uuid.UUID, code string) (tools.AddedLocale, error) {
	if owner, ok := f.owner[project]; !ok || !scoped(ctx, owner) {
		return tools.AddedLocale{}, tools.ErrNotFound
	}
	f.added = append(f.added, code)
	return tools.AddedLocale{Locale: code, Direction: "ltr", Created: true}, nil
}

// ── Quality ─────────────────────────────────────────────────────────

type fakeChecks struct {
	owner map[uuid.UUID]tenancy.ID
	asked []tools.CheckRequest
}

func (f *fakeChecks) Run(
	ctx context.Context, project uuid.UUID, in tools.CheckRequest,
) (tools.CheckReport, error) {
	if owner, ok := f.owner[project]; !ok || !scoped(ctx, owner) {
		return tools.CheckReport{}, tools.ErrNotFound
	}
	f.asked = append(f.asked, in)
	return tools.CheckReport{
		Conclusion: "failure", PolicyVersion: 3, Messages: 3, Errors: 1, Warnings: 0,
		Layers: []string{"structure", "parity", "completeness"},
		Findings: []tools.Finding{{
			Fingerprint: "fp-1", Layer: "parity", Code: "argument_missing", Severity: "error",
			Key: "checkout.pay", Locale: "de", Explanation: "the de text drops {$amount}",
		}},
	}, nil
}

// ── Intelligence ────────────────────────────────────────────────────

// fakeTranslator is the AI provider port, faked. No test in this
// package may reach a provider or the network: `translate` starts a
// server-side job, and what a test asserts is the request that reached
// Intelligence (RFC 0005 §7.4).
type fakeTranslator struct {
	owner map[uuid.UUID]tenancy.ID
	asked []tools.TranslateRequest
}

func (f *fakeTranslator) Translate(
	ctx context.Context, project uuid.UUID, in tools.TranslateRequest,
) (tools.TranslateJob, error) {
	if owner, ok := f.owner[project]; !ok || !scoped(ctx, owner) {
		return tools.TranslateJob{}, tools.ErrNotFound
	}
	f.asked = append(f.asked, in)
	return tools.TranslateJob{
		Fill: uuid.New().String(), Locales: in.Locales, Select: "missing",
		JobsCreated: 4, JobsExisting: 1, Skipped: map[string]int{"sensitive_namespace": 2},
	}, nil
}

// ── the world, with its write side ──────────────────────────────────

// writeTools are the four write tools of RFC 0005 §7.3, in the order
// Write registers them.
var writeTools = []string{
	tools.MessageUpsertName, tools.TranslationProposeName, tools.LocaleAddName, tools.TranslateName,
}

// writers is everything the write side of a seeded world wrote.
type writers struct {
	messages   *fakeMessages
	proposals  *fakeProposals
	locales    *fakeLocales
	checks     *fakeChecks
	translator *fakeTranslator
}

// seedWrites adds the write ports to a seeded world, owned by its
// tenant, and the second tenant's project beside them.
func seedWrites(w *world) *writers {
	owner := map[uuid.UUID]tenancy.ID{w.project: w.tenant, w.otherProject: w.other}
	ws := &writers{
		messages:   &fakeMessages{owner: owner},
		proposals:  &fakeProposals{owner: owner},
		locales:    &fakeLocales{owner: owner},
		checks:     &fakeChecks{owner: owner},
		translator: &fakeTranslator{owner: owner},
	}
	w.sources.Messages = ws.messages
	w.sources.Proposals = ws.proposals
	w.sources.Locales = ws.locales
	w.sources.Checks = ws.checks
	w.sources.Translator = ws.translator
	return ws
}

// writeArgs are valid arguments for each write tool, so a gate test can
// prove a refusal came from the gate and not from a bad argument.
func writeArgs(project uuid.UUID) map[string]map[string]any {
	return map[string]map[string]any{
		tools.MessageUpsertName: {
			"project": project.String(), "key": "checkout.pay", "text": "Pay {$amount}",
		},
		tools.TranslationProposeName: {
			"project": project.String(), "key": "checkout.pay", "locale": "de", "text": "Zahlen {$amount}",
		},
		tools.LocaleAddName: {"project": project.String(), "locale": "pt-BR"},
		tools.TranslateName: {"project": project.String(), "locales": []string{"de"}},
	}
}

// ── registration ────────────────────────────────────────────────────

func TestWriteRegistersTheFourToolsInTheWriteToolset(t *testing.T) {
	w := seed()

	got := tools.Write(w.sources)
	names := make([]string, len(got))
	for i, tool := range got {
		names[i] = tool.Name
		if tool.Toolset != domain.ToolsetWrite {
			t.Errorf("%s is in the %s toolset, want write", tool.Name, tool.Toolset)
		}
		if tool.ReadOnly {
			t.Errorf("%s is marked read-only", tool.Name)
		}
		if tool.Permission == "" {
			t.Errorf("%s checks no permission", tool.Name)
		}
		if tool.Description == "" || tool.Title == "" {
			t.Errorf("%s has no description or title", tool.Name)
		}
	}
	if !slices.Equal(names, writeTools) {
		t.Fatalf("tools = %v, want %v", names, writeTools)
	}
}

func TestWriteSkipsToolsWithoutTheirPort(t *testing.T) {
	if got := tools.Write(tools.Sources{}); len(got) != 0 {
		t.Fatalf("tools = %d, want none", len(got))
	}
}

// Each write tool checks the permission its own context checks, so a
// tool and the endpoint beside it can never disagree about what a token
// may do (RFC 0005 §7.2).
func TestWriteToolsCheckTheirContextsPermission(t *testing.T) {
	w := seed()

	want := map[string]identity.Permission{
		tools.MessageUpsertName:      identity.PermCatalogWrite,
		tools.TranslationProposeName: identity.PermTranslationsWrite,
		tools.LocaleAddName:          identity.PermCatalogWrite,
		tools.TranslateName:          identity.PermIntelligenceTranslate,
	}
	for _, tool := range tools.Write(w.sources) {
		if tool.Permission != want[tool.Name] {
			t.Errorf("%s checks %q, want %q", tool.Name, tool.Permission, want[tool.Name])
		}
	}
}

// No write tool offers a way to destroy something: M4 exposes no delete
// of any kind (RFC 0005 §7.2).
func TestNoWriteToolDestroysAnything(t *testing.T) {
	w := seed()

	for _, tool := range tools.All(w.sources) {
		for _, word := range []string{"delete", "remove", "destroy", "purge", "rename"} {
			if strings.Contains(tool.Name, word) {
				t.Errorf("%s exists", tool.Name)
			}
		}
	}
}

// ── the two-lock write gate ─────────────────────────────────────────

// The first lock. A read-only token cannot open a write session at all,
// so it never reaches a write tool — and the capability probe tells it
// so rather than letting it find out by being refused.
func TestAReadOnlyTokenCannotOpenAWriteSession(t *testing.T) {
	w := seed()

	svc, _ := session(t, w, nil, "read")
	if err := svc.AllowToolset(callerWith(t, w.tenant, "read"), domain.ToolsetWrite); !errors.Is(err, domain.ErrWriteNotGranted) {
		t.Fatalf("err = %v, want ErrWriteNotGranted", err)
	}
	if err := svc.AllowToolset(callerWith(t, w.tenant, "read", "write"), domain.ToolsetWrite); err != nil {
		t.Fatalf("a write token was refused a write session: %v", err)
	}
}

// The exit criterion (RFC 0005 §12.6 case 6): a read-only token is
// refused on translation_propose. It is refused on every write tool,
// with arguments that are otherwise perfectly valid, and nothing
// reaches any context.
func TestAReadOnlyTokenIsRefusedOnEveryWriteTool(t *testing.T) {
	w := seed()
	ws := w.writes
	audit := &recordingAudit{}
	svc, sess := session(t, w, audit, "read")
	if sess.Toolset != domain.ToolsetRead {
		t.Fatalf("toolset = %s, want read", sess.Toolset)
	}
	args := writeArgs(w.project)
	for _, name := range writeTools {
		if _, err := call(t, svc, sess, name, args[name]); !errors.Is(err, domain.ErrToolNotInSession) {
			t.Errorf("%s: err = %v, want ErrToolNotInSession", name, err)
		}
	}
	if len(ws.messages.wrote) != 0 || len(ws.proposals.wrote) != 0 ||
		len(ws.locales.added) != 0 || len(ws.translator.asked) != 0 {
		t.Fatal("a refused call still reached a context")
	}
	// A read session is not even offered them.
	for _, tool := range svc.Tools(domain.ToolsetRead) {
		if slices.Contains(writeTools, tool.Name) {
			t.Errorf("a read session is offered %s", tool.Name)
		}
	}
	for _, e := range audit.entries {
		if e.Outcome != domain.OutcomeDenied {
			t.Errorf("%s was audited as %s, want denied", e.Tool, e.Outcome)
		}
	}
	if len(audit.entries) != len(writeTools) {
		t.Fatalf("audited %d refusals, want %d", len(audit.entries), len(writeTools))
	}
}

// The second lock, and the one that is easy to forget: a token that
// *does* carry write, in a session that did not ask for the write
// toolset, still cannot write. A token is long-lived and an agent is
// not a person (RFC 0005 §7.2).
func TestAWriteTokenInAReadSessionStillCannotWrite(t *testing.T) {
	w := seed()
	ws := w.writes
	svc, _ := session(t, w, nil, "read", "write")
	caller := callerWith(t, w.tenant, "read", "write")
	read := svc.Open("streamable-http", caller, domain.ToolsetRead)
	args := writeArgs(w.project)
	for _, name := range writeTools {
		if _, err := call(t, svc, read, name, args[name]); !errors.Is(err, domain.ErrToolNotInSession) {
			t.Errorf("%s: err = %v, want ErrToolNotInSession", name, err)
		}
	}
	if len(ws.proposals.wrote) != 0 {
		t.Fatal("a proposal got through a read session")
	}
}

// Both locks open: a write token in a write session writes.
func TestBothLocksOpenLetTheWriteThrough(t *testing.T) {
	w := seed()
	ws := w.writes
	svc, sess := session(t, w, nil, "read", "write")
	args := writeArgs(w.project)
	for _, name := range writeTools {
		if _, err := call(t, svc, sess, name, args[name]); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(ws.messages.wrote) != 1 || len(ws.proposals.wrote) != 1 ||
		len(ws.locales.added) != 1 || len(ws.translator.asked) != 1 {
		t.Fatal("a write did not reach its context")
	}
}

// ── translation_propose always lands in review ──────────────────────

// The invariant, from the tool's side: there is no way to ask for a
// review state. Not an argument that is refused — an argument that does
// not exist, in the schema and in the port (RFC 0005 §7.4).
func TestTranslationProposeOffersNoWayToApprove(t *testing.T) {
	w := seed()

	tool, ok := toolNamed(tools.Write(w.sources), tools.TranslationProposeName)
	if !ok {
		t.Fatal("translation_propose is not registered")
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for name := range schema.Properties {
		for _, forbidden := range []string{"state", "review", "approve", "origin", "provenance"} {
			if strings.Contains(name, forbidden) {
				t.Errorf("translation_propose declares a %q argument", name)
			}
		}
	}
	// An argument it does not declare is refused rather than ignored, so
	// a client that sends `state` is told, not quietly obeyed.
	svc, sess := session(t, w, nil, "read", "write")
	_, err := call(t, svc, sess, tools.TranslationProposeName, map[string]any{
		"project": w.project.String(), "key": "checkout.pay", "locale": "de",
		"text": "Zahlen", "state": "approved",
	})
	var invalid *app.InvalidArgumentError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want an invalid-argument error", err)
	}
}

// And from the answer's side: a proposal is reported as waiting for
// review, and never as approved.
func TestAProposalLandsInReview(t *testing.T) {
	w := seed()
	ws := w.writes
	svc, sess := session(t, w, nil, "read", "write")
	res, err := call(t, svc, sess, tools.TranslationProposeName, map[string]any{
		"project": w.project.String(), "key": "checkout.pay", "locale": "de", "text": "Zahlen {$amount}",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := res.Data.(tools.ProposedTranslation)
	if !ok {
		t.Fatalf("data = %T, want a ProposedTranslation", res.Data)
	}
	if got.State != "needs_review" {
		t.Fatalf("state = %q, want needs_review", got.State)
	}
	if !strings.Contains(res.Explanation, "review") {
		t.Fatalf("explanation = %q, want it to say the text waits for review", res.Explanation)
	}
	if len(ws.proposals.wrote) != 1 || ws.proposals.wrote[0].Text != "Zahlen {$amount}" {
		t.Fatalf("proposal = %+v", ws.proposals.wrote)
	}
}

// ── check_run is a read tool ────────────────────────────────────────

// RFC 0005 §7.3 gives check_run the `read` scope, and §12.6 case 6 has
// a read-only token run one. It changes nothing and stores nothing.
func TestAReadOnlyTokenRunsACheck(t *testing.T) {
	w := seed()
	ws := w.writes
	svc, sess := session(t, w, nil, "read")
	res, err := call(t, svc, sess, tools.CheckRunName, map[string]any{
		"project": w.project.String(), "layers": []string{"parity"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rep, ok := res.Data.(tools.CheckReport)
	if !ok {
		t.Fatalf("data = %T, want a CheckReport", res.Data)
	}
	if rep.Conclusion != "failure" || len(rep.Findings) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if len(res.Affected) != 0 {
		t.Fatalf("a check run reported %v as affected; it changes nothing", res.Affected)
	}
	if len(ws.checks.asked) != 1 || !slices.Equal(ws.checks.asked[0].Layers, []string{"parity"}) {
		t.Fatalf("request = %+v", ws.checks.asked)
	}
	tool, _ := toolNamed(tools.Read(w.sources), tools.CheckRunName)
	if tool.Toolset != domain.ToolsetRead || !tool.ReadOnly {
		t.Fatalf("check_run is %s and read-only=%v, want read and true", tool.Toolset, tool.ReadOnly)
	}
}

// ── tenancy ─────────────────────────────────────────────────────────

// A second tenant's token sees and touches nothing of the first
// tenant's project (RFC 0005 §12.6 case 6). The id is well formed and
// the arguments are valid: what stops the write is that the project is
// not there, which is the same answer a typo gets.
func TestWritesNeverCrossTenants(t *testing.T) {
	w := seed()
	ws := w.writes
	svc, sess := session(t, w, nil, "read", "write")
	args := writeArgs(w.otherProject)
	for _, name := range writeTools {
		if _, err := call(t, svc, sess, name, args[name]); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if _, err := call(t, svc, sess, tools.CheckRunName, map[string]any{
		"project": w.otherProject.String(),
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("check_run: err = %v, want ErrNotFound", err)
	}
	if len(ws.messages.wrote) != 0 || len(ws.proposals.wrote) != 0 ||
		len(ws.locales.added) != 0 || len(ws.translator.asked) != 0 || len(ws.checks.asked) != 0 {
		t.Fatal("a cross-tenant call reached a context")
	}
	// The other tenant's own session reaches its own project, so the
	// refusal above was tenancy and not a broken fake.
	other := callerWith(t, w.other, "read", "write")
	otherSess := svc.Open("streamable-http", other, domain.ToolsetWrite)
	if _, err := call(t, svc, otherSess, tools.LocaleAddName, args[tools.LocaleAddName]); err != nil {
		t.Fatalf("the owning tenant was refused its own project: %v", err)
	}
}

// ── the audit ledger ────────────────────────────────────────────────

// Every write is audited like every read: the arguments' shape, the ids
// it touched, and never the text (RFC 0005 §7.2).
func TestEveryWriteIsAudited(t *testing.T) {
	w := seed()

	text := "Zahlen {$amount}"
	audit := &recordingAudit{}
	svc, sess := session(t, w, audit, "read", "write")
	args := writeArgs(w.project)
	for _, name := range writeTools {
		if _, err := call(t, svc, sess, name, args[name]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(audit.entries) != len(writeTools) {
		t.Fatalf("audited %d calls, want %d", len(audit.entries), len(writeTools))
	}
	for i, e := range audit.entries {
		if e.Tool != writeTools[i] {
			t.Fatalf("entry %d is %s, want %s", i, e.Tool, writeTools[i])
		}
		if e.Outcome != domain.OutcomeOK {
			t.Errorf("%s = %s, want ok", e.Tool, e.Outcome)
		}
		if e.Toolset != domain.ToolsetWrite {
			t.Errorf("%s was audited under the %s toolset", e.Tool, e.Toolset)
		}
		if len(e.Affected) == 0 {
			t.Errorf("%s recorded nothing as affected", e.Tool)
		}
		if e.Arguments["project"] != w.project.String() {
			t.Errorf("%s hid its project: %v", e.Tool, e.Arguments)
		}
	}
	// The proposal's text is a length, never the text itself.
	shape := audit.entries[1].Arguments
	if shape["text"] != fmt.Sprintf("string(len=%d)", len([]rune(text))) {
		t.Fatalf("translation_propose.text = %q, want a length", shape["text"])
	}
	if strings.Contains(fmt.Sprint(shape), "Zahlen") {
		t.Fatalf("the proposal's text reached the ledger: %v", shape)
	}
}

// ── arguments ───────────────────────────────────────────────────────

func TestWriteArgumentsAreValidated(t *testing.T) {
	w := seed()
	ws := w.writes
	svc, sess := session(t, w, nil, "read", "write")
	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"upsert needs a key", tools.MessageUpsertName, map[string]any{
			"project": w.project.String(), "text": "Pay",
		}},
		{"upsert needs text", tools.MessageUpsertName, map[string]any{
			"project": w.project.String(), "key": "checkout.pay",
		}},
		{"upsert bounds its text", tools.MessageUpsertName, map[string]any{
			"project": w.project.String(), "key": "checkout.pay",
			"text": strings.Repeat("x", tools.MaxTextBytes+1),
		}},
		{"a base revision starts at 1", tools.MessageUpsertName, map[string]any{
			"project": w.project.String(), "key": "checkout.pay", "text": "Pay", "base_revision": 0,
		}},
		{"propose needs a locale", tools.TranslationProposeName, map[string]any{
			"project": w.project.String(), "key": "checkout.pay", "text": "Zahlen",
		}},
		{"propose bounds its text", tools.TranslationProposeName, map[string]any{
			"project": w.project.String(), "key": "checkout.pay", "locale": "de",
			"text": strings.Repeat("x", tools.MaxTextBytes+1),
		}},
		{"locale_add needs a locale", tools.LocaleAddName, map[string]any{
			"project": w.project.String(),
		}},
		{"translate needs a locale", tools.TranslateName, map[string]any{
			"project": w.project.String(), "locales": []string{},
		}},
		{"translate bounds its locales", tools.TranslateName, map[string]any{
			"project": w.project.String(), "locales": many("de-", tools.MaxTranslateLocales+1),
		}},
		{"translate bounds its keys", tools.TranslateName, map[string]any{
			"project": w.project.String(), "locales": []string{"de"},
			"keys": many("k.", tools.MaxTranslateKeys+1),
		}},
		{"a project id is a project id", tools.LocaleAddName, map[string]any{
			"project": "not-a-uuid", "locale": "de",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := call(t, svc, sess, tc.tool, tc.args)
			var invalid *app.InvalidArgumentError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want an invalid-argument error", err)
			}
		})
	}
	if len(ws.messages.wrote) != 0 || len(ws.proposals.wrote) != 0 ||
		len(ws.locales.added) != 0 || len(ws.translator.asked) != 0 {
		t.Fatal("an invalid call reached a context")
	}
}

// many builds n distinct strings with a prefix.
func many(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

// translate never carries a provider, a model or a key in either
// direction: the job runs server-side under the tenant's own settings
// (RFC 0005 §7.4).
func TestTranslateCarriesNoProviderKey(t *testing.T) {
	w := seed()

	tool, ok := toolNamed(tools.Write(w.sources), tools.TranslateName)
	if !ok {
		t.Fatal("translate is not registered")
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for name := range schema.Properties {
		for _, forbidden := range []string{"provider", "model", "api_key", "secret", "prompt", "credential"} {
			if strings.Contains(name, forbidden) {
				t.Errorf("translate declares a %q argument", name)
			}
		}
	}
	svc, sess := session(t, w, nil, "read", "write")
	res, err := call(t, svc, sess, tools.TranslateName, map[string]any{
		"project": w.project.String(), "locales": []string{"de", "fr"},
	})
	if err != nil {
		t.Fatal(err)
	}
	job, ok := res.Data.(tools.TranslateJob)
	if !ok {
		t.Fatalf("data = %T, want a TranslateJob", res.Data)
	}
	if job.Fill == "" || job.JobsCreated != 4 {
		t.Fatalf("job = %+v", job)
	}
	// Intelligence skips sensitive namespaces itself and says so; the
	// tool reports the skip rather than deciding it.
	if job.Skipped["sensitive_namespace"] != 2 {
		t.Fatalf("skipped = %v", job.Skipped)
	}
}
