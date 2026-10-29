package audit_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	auditdomain "go.klarlabs.de/glossa/platform/internal/audit/domain"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/adapters/audit"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

type recorder struct{ calls []auditdomain.ToolCall }

func (r *recorder) RecordSignIn(context.Context, auditdomain.SignIn) error { return nil }

func (r *recorder) RecordToolCall(_ context.Context, c auditdomain.ToolCall) error {
	r.calls = append(r.calls, c)
	return nil
}

// The trail gets the ledger row's id, the token as the actor, and the
// arguments' shape — never the MCP session id.
func TestAToolCallBecomesAnAuditEntry(t *testing.T) {
	rec := &recorder{}
	tok := identity.NewTokenID()
	row := app.AuditEntry{
		ID: uuid.Must(uuid.NewV7()), Session: "session-that-must-not-leave-mcp", Actor: identity.TokenActor(tok),
		Token: tok, Toolset: domain.ToolsetWrite, Tool: "message_upsert", Outcome: domain.OutcomeOK,
		Arguments: domain.Shape(json.RawMessage(`{"locale":"de","source":"Hallo Welt"}`), []string{"locale"}),
		Affected:  []string{"a", "b"}, Duration: 15 * time.Millisecond, At: time.Now(),
	}
	if err := audit.New(rec).Record(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("%d calls recorded", len(rec.calls))
	}
	c := rec.calls[0]
	if c.Call != row.ID || c.Actor != "token:"+tok.String() || c.Affected != 2 || c.DurationMs != 15 ||
		c.Toolset != "write" || c.Outcome != "ok" || c.Arguments["source"] != "string(len=10)" {
		t.Errorf("tool call %+v", c)
	}
	draft := c.Draft()
	if err := draft.Validate(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(draft.Summary), "session-that") || strings.Contains(string(draft.Summary), "Hallo") {
		t.Errorf("the entry carries the session or the text: %s", draft.Summary)
	}
}
