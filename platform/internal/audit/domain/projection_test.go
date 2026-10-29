package domain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

const (
	person     = "person:0190a1b2-0000-7000-8000-0000000000aa"
	tokenActor = "token:0190a1b2-0000-7000-8000-0000000000ab"
	project    = "0190a1b2-0000-7000-8000-000000000001"
	// canary is a word that only ever appears in message text. It must
	// not survive into any entry (RFC 0006 §12.5).
	canary = "Quokkafluent"
)

func delivery(typ string, actor outbox.Actor, payload string) outbox.Delivery {
	return outbox.Delivery{
		EventID: uuid.Must(uuid.NewV7()), TenantID: tenancy.NewID(), Type: typ,
		AggregateType: "translation", AggregateID: uuid.NewString(), Actor: actor,
		Payload: json.RawMessage(payload), OccurredAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
	}
}

func TestATranslationEventKeepsIdsAndSelectorsAndDropsText(t *testing.T) {
	d := delivery("localization.translation.revised", outbox.Actor(tokenActor), `{
		"translation_id": "0190a1b2-0000-7000-8000-0000000000bb", "project_id": "`+project+`",
		"message_id": "0190a1b2-0000-7000-8000-0000000000cc", "locale": "de-CH", "revision": 4,
		"source_revision": 2, "state": "needs_review", "origin": "machine", "by": "`+tokenActor+`",
		"text": "Der `+canary+` ist da", "note": "`+canary+`"}`)
	draft, err := domain.FromEvent(d)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Action != "localization.translation.revised" || draft.Actor != tokenActor || draft.Source != domain.SourceOutbox {
		t.Errorf("draft %+v", draft)
	}
	if !draft.Project.Valid || draft.Project.UUID.String() != project || draft.Locale != "de-CH" {
		t.Errorf("project %v, locale %q", draft.Project, draft.Locale)
	}
	if draft.EventID != d.EventID || draft.TraceID != d.TraceID {
		t.Error("event id or trace id lost")
	}
	var summary map[string]any
	if err := json.Unmarshal(draft.Summary, &summary); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"translation_id": "0190a1b2-0000-7000-8000-0000000000bb", "locale": "de-CH", "state": "needs_review",
		"origin": "machine", "revision": float64(4), "text": "string(len=23)", "note": "string(len=12)",
	}
	for k, v := range want {
		if summary[k] != v {
			t.Errorf("summary[%s] = %v, want %v", k, summary[k], v)
		}
	}
	if strings.Contains(string(draft.Summary), canary) {
		t.Errorf("text leaked into the summary: %s", draft.Summary)
	}
}

// Whatever an event's payload carries in fields its projection does not
// select, it reaches the entry only as its shape — for every projection.
func TestNoProjectionLetsTextThrough(t *testing.T) {
	payload := `{"text":"` + canary + `","name":"` + canary + `","email":"` + canary + `@example.com",` +
		`"message":{"key":"k","text":"` + canary + `"},"summary":{"title":"` + canary + `","created":3},` +
		`"fallback":{"de":["` + canary + ` text that is long enough not to pass as an id at all, ever"]},` +
		`"description":"` + canary + `","items":["` + canary + `"]}`
	for _, typ := range domain.ProjectedTypes() {
		draft, err := domain.FromEvent(delivery(typ, outbox.Actor(person), payload))
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if strings.Contains(string(draft.Summary), canary) {
			t.Errorf("%s: text leaked into the summary: %s", typ, draft.Summary)
		}
		if _, err := domain.Append(uuid.Must(uuid.NewV7()), domain.Head{}, draft); err != nil {
			t.Errorf("%s: the projected draft is not appendable: %v", typ, err)
		}
	}
}

func TestAnEmailIsNeverKept(t *testing.T) {
	draft, err := domain.FromEvent(delivery("identity.member.added", outbox.Actor(person), `{
		"member_id": "0190a1b2-0000-7000-8000-0000000000dd", "email": "ada@example.com", "status": "invited",
		"roles": ["translator"], "locales": ["de", "fr"], "added_by": "`+person+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	s := string(draft.Summary)
	if strings.Contains(s, "ada@") {
		t.Errorf("the email was kept: %s", s)
	}
	if !strings.Contains(s, `"roles":["translator"]`) || !strings.Contains(s, `"locales":["de","fr"]`) {
		t.Errorf("selectors were not kept: %s", s)
	}
}

func TestTheActorOfAnEventFromBefore0042(t *testing.T) {
	tests := []struct {
		name, payload string
		envelope      outbox.Actor
		want          string
		fromPayload   bool
	}{
		{"the envelope wins", `{"by":"` + person + `"}`, outbox.Actor(tokenActor), tokenActor, false},
		{"unknown, but the payload's by names someone", `{"by":"` + person + `"}`, outbox.ActorUnknown, person, true},
		{"unknown, and by is not an actor", `{"by":"alice"}`, outbox.ActorUnknown, "unknown", false},
		{"unknown, and no by", `{}`, outbox.ActorUnknown, "unknown", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := delivery("catalog.project.updated", tc.envelope, tc.payload)
			draft, err := domain.FromEvent(d)
			if err != nil {
				t.Fatal(err)
			}
			if draft.Actor != tc.want || domain.ActorFromPayload(d) != tc.fromPayload {
				t.Errorf("actor %q (from payload %t), want %q (%t)", draft.Actor, domain.ActorFromPayload(d), tc.want, tc.fromPayload)
			}
		})
	}
	// An integration job's by is its requester, not who ended it: it is
	// never taken as the actor.
	d := delivery("integration.import.completed", outbox.ActorUnknown, `{"job_id":"x","by":"`+person+`"}`)
	if draft, _ := domain.FromEvent(d); draft.Actor != "unknown" {
		t.Errorf("an import's requester was taken as its actor: %q", draft.Actor)
	}
}

func TestAnUnmappedTypeIsRefusedLiveAndShapedInHistory(t *testing.T) {
	d := delivery("catalog.widget.frobbed", outbox.Actor(person), `{"project_id":"`+project+`","text":"`+canary+`"}`)
	if _, err := domain.FromEvent(d); !errors.Is(err, domain.ErrUnmapped) {
		t.Errorf("an unmapped type projected: %v", err)
	}
	draft := domain.FromRetiredEvent(d)
	if draft.Project.Valid || draft.Actor != person || strings.Contains(string(draft.Summary), canary) ||
		strings.Contains(string(draft.Summary), project) {
		t.Errorf("a retired event kept more than its shape: %+v %s", draft, draft.Summary)
	}
}

func TestSummarizeShapes(t *testing.T) {
	got := string(domain.Summarize(json.RawMessage(`{
		"id": "0190a1b2-0000-7000-8000-0000000000dd", "count": 3, "ratio": 0.5, "ok": true, "none": null,
		"long_id": "`+strings.Repeat("x", 65)+`", "ctl": "a\nb", "list": [1, 2], "obj": {"a": 1},
		"nested": {"id": "n1", "text": "words here"}, "fallback": {"de": ["de-CH"], "fr": ["fr-CA", "en"]}}`),
		[]string{"id", "long_id", "ctl", "list", "nested.id", "fallback.*"}))
	for _, want := range []string{
		`"id":"0190a1b2-0000-7000-8000-0000000000dd"`, `"count":3`, `"ratio":"number"`, `"ok":true`, `"none":null`,
		`"long_id":"string(len=65)"`, `"ctl":"string(len=3)"`, `"list":"array(2)"`, `"obj":"object(1)"`,
		`"nested":{"id":"n1","text":"string(len=10)"}`, `"fallback":{"de":["de-CH"],"fr":["fr-CA","en"]}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %s:\n%s", want, got)
		}
	}
	if got := string(domain.Summarize(json.RawMessage(`"a string"`), nil)); got != `{"_":"string(len=8)"}` {
		t.Errorf("a scalar payload: %s", got)
	}
	if got := string(domain.Summarize(nil, nil)); got != `{}` {
		t.Errorf("no payload: %s", got)
	}
	many := map[string]int{}
	for i := range 40 {
		many[strings.Repeat("k", i+1)] = i
	}
	raw, _ := json.Marshal(many)
	if got := string(domain.Summarize(raw, nil)); !strings.Contains(got, `"…":"8 more"`) {
		t.Errorf("a wide payload is not bounded: %s", got)
	}
}

func TestDirectDrafts(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	p := uuid.MustParse("0190a1b2-0000-7000-8000-0000000000aa")
	ok := domain.SignIn{Attempt: uuid.Must(uuid.NewV7()), Person: p, Method: "passkey", At: at}.Draft()
	if ok.Action != domain.ActionSignedIn || ok.Actor != person || ok.AggregateID != p.String() ||
		string(ok.Summary) != `{"method":"passkey"}` {
		t.Errorf("success %+v %s", ok, ok.Summary)
	}
	failed := domain.SignIn{Attempt: uuid.Must(uuid.NewV7()), Person: p, Method: "password", Failure: "locked", At: at}.Draft()
	if failed.Action != domain.ActionSignInFailed || failed.Actor != "unknown" ||
		string(failed.Summary) != `{"method":"password","reason":"locked"}` {
		t.Errorf("failure %+v %s", failed, failed.Summary)
	}

	call := domain.ToolCall{
		Call: uuid.Must(uuid.NewV7()), Actor: tokenActor, Toolset: "write", Tool: "message_upsert", Outcome: "ok",
		Arguments: map[string]string{
			"key": "string(len=12)", "locale": "de", strings.Repeat("w", 80): "number",
			"source": canary + " " + strings.Repeat("words ", 20),
		},
		Affected: 2, DurationMs: 15, At: at,
	}.Draft()
	s := string(call.Summary)
	if call.Action != domain.ActionToolCalled || call.Actor != tokenActor || call.AggregateType != domain.AggregateToolCall {
		t.Errorf("tool call %+v", call)
	}
	for _, want := range []string{`"tool":"message_upsert"`, `"locale":"de"`, `"key(len=80)":"number"`, `"affected":2`} {
		if !strings.Contains(s, want) {
			t.Errorf("tool call summary lacks %s: %s", want, s)
		}
	}
	if strings.Contains(s, canary) {
		t.Errorf("an argument's text leaked: %s", s)
	}
	for _, d := range []domain.Draft{ok, failed, call} {
		if err := d.Validate(); err != nil {
			t.Errorf("%s: %v", d.Action, err)
		}
	}
}

// Workflow's events carry two kinds of text a person wrote — a
// definition's name and the reason on an assignment or an approval
// decision — beside the identifiers an entry needs. The text is recorded
// as its length and the identifiers as written.
func TestWorkflowEventsKeepTheirPartiesAndDropTheirText(t *testing.T) {
	for typ, payload := range map[string]string{
		"workflow.approval.denied": `{"approval_id":"a1","project_id":"p1","subject_kind":"translation",
			"subject_id":"m1","locale":"de","eligible":"group:g1","state":"denied","principal":"person:u1",
			"verdict":"denied","reason":"Wrong formality for the legal team","approvers":[],"by":"person:u1"}`,
		"workflow.assignment.declined": `{"assignment_id":"s1","project_id":"p1","assignee":"member:u2",
			"permission":"translations.write","state":"declined","reason":"Too busy this week","by":"person:u2","units":[]}`,
		"workflow.definition_saved": `{"definition_id":"d1","project_id":"p1","name":"Legal sign-off","subject":"translation","version":2}`,
		"workflow.instance.rebased": `{"instance_id":"i1","project_id":"p1","definition_id":"d1","subject_kind":"translation",
			"locale":"de","from_version":1,"to_version":2,"state":"legal_review"}`,
	} {
		p, ok := domain.Projections[typ]
		if !ok {
			t.Fatalf("%s has no projection", typ)
		}
		got := string(domain.Summarize(json.RawMessage(payload), p.Selectors))
		for _, text := range []string{"Wrong formality", "Too busy", "Legal sign-off"} {
			if strings.Contains(got, text) {
				t.Errorf("%s kept %q verbatim: %s", typ, text, got)
			}
		}
		for _, id := range []string{"group:g1", "member:u2", `"version":2`, `"to_version":2`, `"state":"legal_review"`} {
			if strings.Contains(payload, id) && !strings.Contains(got, id) {
				t.Errorf("%s dropped the identifier %s: %s", typ, id, got)
			}
		}
	}
}
