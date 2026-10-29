-- 0010 — Catalog: listing a project's namespaces with their message
-- counts (GET …/projects/{project}/namespaces) groups the project's
-- messages by namespace and state, a page of names at a time. With
-- (project_id, namespace, state) the grouping reads the index alone
-- (an index-only scan once the visibility map is set), in name order,
-- and starts after the page's last name without scanning the rest.
--
-- Plain CREATE INDEX: golang-migrate runs each file in a transaction,
-- and the table is small enough to lock briefly at this stage.

CREATE INDEX catalog_messages_namespaces ON catalog_messages (project_id, namespace, state);
