-- Smart scheduling is an opt-in group policy. Existing and newly created
-- groups remain on the legacy scheduler until an administrator enables it.

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS smart_scheduler_enabled BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN groups.smart_scheduler_enabled IS
    'Whether smart scheduling may reorder eligible account candidates for this group; defaults to false';
