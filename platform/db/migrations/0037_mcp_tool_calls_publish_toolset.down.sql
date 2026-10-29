-- Restore the narrow vocabulary for new rows, and keep every row
-- already written.
--
-- NOT VALID is deliberate. If any publish session ever ran, the ledger
-- holds rows the old constraint cannot describe, and there are only
-- three ways down: fail the rollback, delete those rows, or keep them
-- and refuse new ones. The first leaves a database stuck; the second
-- destroys an audit trail, which nothing in this system is allowed to
-- do — the table grants glossa_app no DELETE at all for that reason.
-- So: history stands, and `publish` stops being accepted from here on.
--
-- On a database that never opened a publish session this is
-- indistinguishable from the original constraint.

ALTER TABLE mcp_tool_calls DROP CONSTRAINT mcp_tool_calls_toolset_check;

ALTER TABLE mcp_tool_calls
    ADD CONSTRAINT mcp_tool_calls_toolset_check
    CHECK (toolset IN ('read', 'write')) NOT VALID;
