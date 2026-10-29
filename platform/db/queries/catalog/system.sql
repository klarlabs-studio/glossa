-- System scope catalog.proposals (db.SystemTx as glossa_system):
-- migration 0016 opens these columns to it, read-only, and nothing
-- else. The query finds the work across tenants; the sweep itself runs
-- in each tenant's scope (RFC 0004 §4.1).

-- name: ListTenantsWithExpiredProposals :many
-- The tenants holding proposed messages whose every proposing branch
-- closed or merged before the cutoff — exactly what SweepProposals
-- obsoletes. The daily catalog.proposals job sweeps each of them.
SELECT DISTINCT m.tenant_id FROM catalog_messages m
WHERE m.state = 'proposed'
  AND EXISTS (SELECT FROM catalog_proposals p WHERE p.message_id = m.id AND p.kind = 'new_key')
  AND NOT EXISTS (SELECT FROM catalog_proposals p JOIN catalog_branches b ON b.id = p.branch_id
                  WHERE p.message_id = m.id AND p.kind = 'new_key'
                    AND (b.closed_at IS NULL OR b.closed_at > sqlc.arg(cutoff)))
ORDER BY m.tenant_id
LIMIT sqlc.arg(max_rows)::int;
