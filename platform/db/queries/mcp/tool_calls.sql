-- name: InsertToolCall :exec
INSERT INTO mcp_tool_calls (
    id, tenant_id, session_id, actor, token_id, toolset, tool, outcome,
    arguments, affected, duration_ms, called_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
);

-- name: ToolCallsByTenant :many
SELECT id, session_id, actor, token_id, toolset, tool, outcome, arguments, affected, duration_ms, called_at
FROM mcp_tool_calls
WHERE tenant_id = $1
ORDER BY called_at DESC, id DESC
LIMIT $2;
