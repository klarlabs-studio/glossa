-- 0028 — the MCP audit ledger (RFC 0005 §7.2, §10, §11).
--
-- `glossa-server` serves MCP at /mcp for agents. An agent is not a
-- person: it acts on a long-lived tenant API token, unattended, and
-- often in a loop. So every tool call it makes is written here, and the
-- row is the only account of what an agent did through MCP.
--
-- What is recorded, and what deliberately is not:
--
--   actor / token_id  the credential that acted, "token:<uuid>" as
--                     Identity's Actor already spells it. MCP mints no
--                     credential of its own, so this is the same id the
--                     tenant can already see, revoke and expire.
--   session_id        the MCP session the call arrived on, so a run of
--                     calls reads as one conversation.
--   toolset           'read' or 'write': which session was opened. A
--                     write tool needs the token's scope *and* a write
--                     session, and the row says which it ran in.
--   tool / outcome    what was called and how it ended.
--   arguments         the *shape* of the arguments, never their content:
--                     {"key": "string(len=27)", "locale": "de"}. A tool
--                     argument can carry source text or a translation,
--                     and message text never goes to a log or a ledger
--                     (§11). Each tool names the few arguments that are
--                     selectors — a locale, a state, a namespace — and
--                     those are recorded verbatim, because they are the
--                     only thing that makes a row readable later;
--                     everything else becomes its JSON type and, for a
--                     string, its rune length. That is enough to answer
--                     "what did this agent touch, and how much of it",
--                     which is what the ledger is for.
--   affected          the ids the call created or changed, so a write is
--                     traceable to the rows it produced.
--
-- The ledger is tenant-owned and append-only for the application role:
-- glossa_app may INSERT and SELECT and nothing else, so no code path —
-- and no agent — can rewrite or erase its own trail. Retention is a
-- later decision; nothing purges it yet.

CREATE TABLE mcp_tool_calls (
    id          uuid        PRIMARY KEY,
    tenant_id   uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    session_id  text        NOT NULL CHECK (char_length(session_id) BETWEEN 1 AND 128),
    actor       text        NOT NULL CHECK (char_length(actor) BETWEEN 1 AND 128),
    token_id    uuid        NOT NULL,
    toolset     text        NOT NULL CHECK (toolset IN ('read', 'write')),
    tool        text        NOT NULL CHECK (char_length(tool) BETWEEN 1 AND 128),
    outcome     text        NOT NULL CHECK (outcome IN ('ok', 'denied', 'invalid', 'error')),
    arguments   jsonb       NOT NULL DEFAULT '{}'::jsonb
                            CHECK (jsonb_typeof(arguments) = 'object'),
    affected    jsonb       NOT NULL DEFAULT '[]'::jsonb
                            CHECK (jsonb_typeof(affected) = 'array'),
    duration_ms integer     NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    called_at   timestamptz NOT NULL
);

-- "What have this tenant's agents done lately?" and "what did this token
-- do?" are the two questions the ledger is read for.
CREATE INDEX mcp_tool_calls_tenant ON mcp_tool_calls (tenant_id, called_at DESC);
CREATE INDEX mcp_tool_calls_token ON mcp_tool_calls (tenant_id, token_id, called_at DESC);

ALTER TABLE mcp_tool_calls ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_tool_calls FORCE ROW LEVEL SECURITY;
CREATE POLICY mcp_tool_calls_tenant_isolation ON mcp_tool_calls
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
-- Append-only: no UPDATE, no DELETE.
GRANT SELECT, INSERT ON mcp_tool_calls TO glossa_app;
