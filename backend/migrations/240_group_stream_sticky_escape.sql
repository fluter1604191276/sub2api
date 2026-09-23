-- Allow each group to opt out of the committed-stream sticky escape recovery.
-- The default stays enabled so existing groups retain the candidate behavior;
-- the admin API can explicitly set FALSE for a group.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS stream_sticky_escape_enabled BOOLEAN NOT NULL DEFAULT TRUE;
