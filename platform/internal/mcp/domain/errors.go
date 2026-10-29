package domain

import "errors"

var (
	// ErrUnauthenticated means the request carried no usable tenant API
	// token.
	ErrUnauthenticated = errors.New("mcp: unauthenticated")
	// ErrCredentialNotAccepted means the bearer authenticates elsewhere
	// but is not a credential MCP accepts: a CI token (glossa_ci_…) or
	// an in-context grant (glossa_ctx_…). Each is minted for one job or
	// one origin, and lending either to a long-lived agent session would
	// widen it (RFC 0005 §7.2).
	ErrCredentialNotAccepted = errors.New("mcp: credential not accepted")
	// ErrWriteNotGranted means a write session was asked for on a token
	// that does not carry the write scope.
	ErrWriteNotGranted = errors.New("mcp: the token does not carry the write scope")
	// ErrPublishNotGranted means a publish session was asked for on a
	// token that does not carry the publish scope. It is its own error
	// rather than a widened ErrWriteNotGranted because the scopes are
	// orthogonal: `write` does not imply `publish` and never has
	// (identity/domain: a publish token carries PermReleasesPublish and
	// nothing else beyond read).
	ErrPublishNotGranted = errors.New("mcp: the token does not carry the publish scope")
	// ErrToolNotInSession means the tool exists but belongs to a toolset
	// this session did not open — the second of the two locks on a write
	// (RFC 0005 §7.2).
	ErrToolNotInSession = errors.New("mcp: the tool is not in this session's toolset")
	// ErrToolNotFound means no tool of that name is registered.
	ErrToolNotFound = errors.New("mcp: no such tool")
	// ErrNotFound means the thing a tool's arguments name is not there:
	// it does not exist, or it belongs to another tenant. The two are
	// deliberately one answer — a session is bound to its token's tenant
	// and nothing tells it apart from a typo — because a
	// forbidden-with-detail would confirm that the id exists somewhere
	// (RFC 0005 §7.2).
	ErrNotFound = errors.New("mcp: not found")
	// ErrInvalidToolset means the client asked for a toolset that does
	// not exist.
	ErrInvalidToolset = errors.New("mcp: invalid toolset")
)
