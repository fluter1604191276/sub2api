# Channel monitor and smart probe controls

Date: 2026-09-08

This release adds group-scoped sticky escape policy presets, a 15-second high-frequency streaming recovery-probe mode, channel-monitor daily estimated-cost accounting with an application-timezone budget cap, and preview-before-apply bulk upstream model synchronization for accounts.

The daily cap applies only to active channel-monitor probes and does not stop passive real-traffic aggregation or recovery-probe billing safeguards. Bulk model synchronization is read-only during preview and uses optimistic version checks before selected mappings are applied.
