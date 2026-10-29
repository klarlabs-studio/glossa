-- 0037 — the MCP audit ledger admits the `publish` toolset.
--
-- RFC 0005 §7.3 puts the release tools behind the `publish` scope, and
-- §7.2 makes `publish` a scope of its own: a publish token carries
-- `releases.publish` and nothing else beyond read, so it is neither a
-- subset nor a superset of `write`. The session toolsets mirror the
-- scopes one for one, which means there are now three of them and not
-- two — a write session cannot move a release, and a publish session
-- cannot rewrite the catalog.
--
-- 0028 wrote the vocabulary of the day into a CHECK: ('read', 'write').
-- A publish session's rows would not have been refused loudly; they
-- would have been *lost*. The ledger write runs in its own transaction
-- and a failure is logged and swallowed, deliberately, so that a broken
-- ledger cannot swallow a good answer — which is exactly why the column
-- must never be the thing that breaks. A narrow CHECK here does not
-- fail a tool call, it silently ends the account of one.
--
-- `admin` stays out, and that is the point of keeping a CHECK at all
-- rather than widening the column to any text. MCP exposes no member,
-- token, connection or tenant management and no delete tool of any
-- kind (RFC 0005 §7.2); a row claiming an `admin` toolset would mean
-- something had gone wrong upstream, and the ledger should refuse to
-- record a session that cannot exist.
--
-- internal/mcp/adapters/postgres asserts, against a real database, that
-- every value of domain.Toolsets() is one this constraint admits and
-- that 'admin' is not — so the Go vocabulary and the SQL one cannot
-- drift apart again without a test going red.

ALTER TABLE mcp_tool_calls DROP CONSTRAINT mcp_tool_calls_toolset_check;

ALTER TABLE mcp_tool_calls
    ADD CONSTRAINT mcp_tool_calls_toolset_check
    CHECK (toolset IN ('read', 'write', 'publish'));
