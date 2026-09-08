-- Extend recovery probes with a bounded high-frequency streaming benchmark preset.
ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_recovery_probe_mode_check;
ALTER TABLE groups ADD CONSTRAINT groups_recovery_probe_mode_check
    CHECK (recovery_probe_mode IN ('manual', 'smart', 'high_frequency'));

ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_recovery_probe_interval_check;
ALTER TABLE groups ADD CONSTRAINT groups_recovery_probe_interval_check
    CHECK (
        recovery_probe_interval_seconds BETWEEN 60 AND 86400
        OR (recovery_probe_mode = 'high_frequency' AND recovery_probe_interval_seconds BETWEEN 15 AND 86400)
    );
