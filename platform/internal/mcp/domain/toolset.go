package domain

import (
	"fmt"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// Toolset is the set of tools a session registered. It is the *second*
// lock on anything that changes something (RFC 0005 §7.2): the token
// must carry the matching scope, and the client must also have opened
// the session asking for that toolset. A token is long-lived and an
// agent is not a person; one accidental tool call should not be able to
// rewrite a catalog — or move production onto a different release.
//
// The toolsets mirror the token scopes one for one, and they are
// deliberately *not* a ladder. `publish` is not a wider `write`: the
// scopes themselves are orthogonal (a publish token carries only
// PermReleasesPublish), so a write session cannot move a release and a
// publish session cannot rewrite the catalog. A client that means to do
// both says so twice.
type Toolset string

const (
	// ToolsetRead is the default: a session that can only read. Every
	// other toolset includes it, because a token that may change
	// something must be able to see what it changes.
	ToolsetRead Toolset = "read"
	// ToolsetWrite registers the read tools and the write tools. It is
	// opened explicitly (`?toolset=write`, `--allow-write` on the stdio
	// proxy) and only on a token that carries the write scope.
	ToolsetWrite Toolset = "write"
	// ToolsetPublish registers the read tools and the release tools —
	// publish, promote and roll back. It is opened explicitly
	// (`?toolset=publish`, `--allow-publish` on the stdio proxy) and only
	// on a token that carries the publish scope.
	ToolsetPublish Toolset = "publish"
)

// The endpoint's wire vocabulary. It lives here rather than in the
// transport adapter because both ends of RFC 0005 §7.1 need it: the
// server that serves /mcp and the CLI's stdio proxy that dials it. A
// constant two packages agree on cannot drift; two spellings can.
const (
	// EndpointPath is where glossa-server serves MCP.
	EndpointPath = "/mcp"
	// ToolsetParam is how a client asks for a toolset beyond read:
	// `/mcp?toolset=write`, `/mcp?toolset=publish`. A query parameter
	// rather than a header because most MCP clients are configured with
	// a URL and nothing else, and the stdio proxy simply appends it.
	ToolsetParam = "toolset"
)

// Toolsets lists every toolset, for the transport's servers, the
// metrics adapter's bounded labels and the tests. It is also the
// vocabulary mcp_tool_calls.toolset admits, so a value added here
// without widening that CHECK loses audit rows.
func Toolsets() []Toolset { return []Toolset{ToolsetRead, ToolsetWrite, ToolsetPublish} }

// ParseToolset reads the toolset a client asked for. Empty means read,
// so a client that says nothing gets the safe session.
func ParseToolset(s string) (Toolset, error) {
	switch Toolset(s) {
	case "":
		return ToolsetRead, nil
	case ToolsetRead:
		return ToolsetRead, nil
	case ToolsetWrite:
		return ToolsetWrite, nil
	case ToolsetPublish:
		return ToolsetPublish, nil
	}
	return "", fmt.Errorf("%w: %q (read, write or publish)", ErrInvalidToolset, s)
}

// String returns the toolset name.
func (t Toolset) String() string { return string(t) }

// Includes reports whether a session with this toolset may call a tool
// belonging to want. Every session includes the read tools; beyond
// those, a session includes exactly the toolset it opened.
func (t Toolset) Includes(want Toolset) bool {
	return want == ToolsetRead || t == want
}

// Scope is the token scope a toolset needs. `admin` is never a toolset:
// MCP exposes no member, token, connection or tenant management, and no
// delete tool of any kind (RFC 0005 §7.2).
func (t Toolset) Scope() domain.Scope {
	switch t {
	case ToolsetWrite:
		return domain.ScopeWrite
	case ToolsetPublish:
		return domain.ScopePublish
	}
	return domain.ScopeRead
}

// Outcome is how a tool call ended. The vocabulary is closed: it is a
// metric label and an audited column, so a new value is a deliberate
// change in both places.
type Outcome string

const (
	// OutcomeOK is a call that ran and answered.
	OutcomeOK Outcome = "ok"
	// OutcomeDenied is a call refused by authorization or by the
	// session's toolset.
	OutcomeDenied Outcome = "denied"
	// OutcomeInvalid is a call whose arguments were rejected.
	OutcomeInvalid Outcome = "invalid"
	// OutcomeError is a call that failed for any other reason.
	OutcomeError Outcome = "error"
)

// Outcomes lists every outcome, for the metrics adapter's zero-valued
// series and for tests.
func Outcomes() []Outcome { return []Outcome{OutcomeOK, OutcomeDenied, OutcomeInvalid, OutcomeError} }

// String returns the outcome name.
func (o Outcome) String() string { return string(o) }
