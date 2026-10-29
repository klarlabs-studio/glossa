package domain

import (
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// The actions of acts that never reach the outbox (RFC 0006 §6.1, "not
// only events"). They are spelled like event types, so an action column
// reads as one vocabulary.
const (
	ActionSignedIn     = "identity.person.signed_in"
	ActionSignInFailed = "identity.person.sign_in_failed"
	ActionToolCalled   = "mcp.tool.called"
)

// Aggregates of direct entries.
const (
	AggregatePerson   = "person"
	AggregateToolCall = "tool_call"
)

// SignIn is one sign-in attempt by a known person, as recorded in each
// tenant they belong to. It holds the method and the outcome: never the
// email, the password, a code, a session or a link.
type SignIn struct {
	// Attempt identifies the attempt; the same id is recorded in each of
	// the person's tenants, once.
	Attempt uuid.UUID
	Person  uuid.UUID
	// Method is "password", "passkey" or "magic_link".
	Method string
	// Failure is "" for a sign-in that succeeded, otherwise why it did
	// not ("invalid_credentials", "totp_invalid", "locked",
	// "email_unverified").
	Failure   string
	At        time.Time
	RequestID string
}

// Draft returns the entry the attempt becomes. The actor of a success is
// the person; a failure was not authenticated, so it is attributed to no
// one ("unknown") and targets the person whose account was tried.
func (s SignIn) Draft() Draft {
	action, actor := ActionSignedIn, "person:"+s.Person.String()
	summary := map[string]any{"method": s.Method}
	if s.Failure != "" {
		action, actor = ActionSignInFailed, "unknown"
		summary["reason"] = s.Failure
	}
	return Draft{
		EventID: s.Attempt, Source: SourceDirect, Action: action, Actor: actor, OccurredAt: Instant(s.At),
		AggregateType: AggregatePerson, AggregateID: s.Person.String(),
		Summary: mustJSON(summary), RequestID: s.RequestID,
	}
}

// ToolCall is one MCP tool call, as the MCP ledger (mcp_tool_calls)
// records it: the entry points at the ledger row by id and repeats only
// what is content-free — the tool, the toolset, the outcome, the shape
// of the arguments and how many rows the call affected. Never the MCP
// session id or the token's secret; the actor names the token by id.
type ToolCall struct {
	// Call is the ledger row's id.
	Call    uuid.UUID
	Actor   string
	Toolset string
	Tool    string
	Outcome string
	// Arguments is the arguments' shape, as mcp/domain.Shape renders it.
	Arguments  map[string]string
	Affected   int
	DurationMs int64
	At         time.Time
	RequestID  string
}

// Draft returns the entry the call becomes.
func (c ToolCall) Draft() Draft {
	args := make(map[string]any, len(c.Arguments))
	names := make([]string, 0, len(c.Arguments))
	for k := range c.Arguments {
		names = append(names, k)
	}
	slices.Sort(names)
	for i, k := range names {
		if i == MaxSummaryKeys {
			args[TruncatedKey] = fmt.Sprintf("%d more", len(names)-MaxSummaryKeys)
			break
		}
		v := c.Arguments[k]
		if !verbatimString(k) {
			k = fmt.Sprintf("key(len=%d)", utf8.RuneCountInString(k))
		}
		if !verbatimString(v) {
			v = fmt.Sprintf("string(len=%d)", utf8.RuneCountInString(v))
		}
		args[k] = v
	}
	summary := map[string]any{
		"tool": c.Tool, "toolset": c.Toolset, "outcome": c.Outcome,
		"arguments": args, "affected": c.Affected, "duration_ms": c.DurationMs,
	}
	if !verbatimString(c.Tool) { // an unknown tool's name is whatever the client sent
		summary["tool"] = fmt.Sprintf("string(len=%d)", utf8.RuneCountInString(c.Tool))
	}
	return Draft{
		EventID: c.Call, Source: SourceDirect, Action: ActionToolCalled, Actor: c.Actor, OccurredAt: Instant(c.At),
		AggregateType: AggregateToolCall, AggregateID: c.Call.String(),
		Summary: mustJSON(summary), RequestID: c.RequestID,
	}
}
