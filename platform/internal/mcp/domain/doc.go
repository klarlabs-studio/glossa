// Package domain is the MCP context's model: what a session is, which
// toolset it opened, and how a tool call is written down.
//
// MCP is a second façade on the platform's application ports, not a
// second platform (RFC 0005 §7.1). So this package deliberately holds
// almost no rules: authorization is Identity's (`Scope` →
// `GrantForScopes` → the permissions), and every tool is a thin call
// into a context's application service. What is genuinely MCP's own is
// here: the two session toolsets and the second lock they put on a
// write, the outcome vocabulary the audit ledger and the metrics share,
// and the argument shape that keeps message text out of both.
package domain
