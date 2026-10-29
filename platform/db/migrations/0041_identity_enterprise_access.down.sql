-- 0041 down — drop groups, vendors and the members' and tokens' project
-- scope, vendor and visibility.

DROP TABLE IF EXISTS identity_group_members;
DROP TABLE IF EXISTS identity_groups;

ALTER TABLE identity_api_tokens DROP COLUMN IF EXISTS projects;

DROP INDEX IF EXISTS identity_members_vendor;
ALTER TABLE identity_members
    DROP CONSTRAINT IF EXISTS identity_members_vendor_fkey,
    DROP CONSTRAINT IF EXISTS identity_members_tenant_id_key,
    DROP CONSTRAINT IF EXISTS identity_members_owner_unscoped,
    DROP CONSTRAINT IF EXISTS identity_members_assigned_translator,
    DROP CONSTRAINT IF EXISTS identity_members_vendor_assigned,
    DROP COLUMN IF EXISTS visibility,
    DROP COLUMN IF EXISTS vendor_id,
    DROP COLUMN IF EXISTS projects;

DROP TABLE IF EXISTS identity_vendors;
