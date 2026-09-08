# 二开登记：渠道监控批量探测间隔

```text
Capability ID: channel-monitor-bulk-interval
Business purpose: 允许管理员在渠道监控列表中多选监控，一次性调整探针检测间隔（例如 15 秒），并立即让后台调度器使用新间隔。
Backend/frontend files: backend/internal/service/channel_monitor_service.go; backend/internal/repository/channel_monitor_repo.go; backend/internal/handler/admin/channel_monitor_handler.go; backend/internal/server/routes/admin.go; frontend/src/api/admin/channelMonitor.ts; frontend/src/components/admin/monitor/MonitorBulkIntervalDialog.vue; frontend/src/components/admin/monitor/MonitorFiltersBar.vue; frontend/src/views/admin/ChannelMonitorView.vue; frontend/src/i18n/locales/zh/admin/channels.ts; frontend/src/i18n/locales/en/admin/channels.ts
Routes or jobs: POST /api/v1/admin/channel-monitors/batch-interval; existing per-monitor ChannelMonitorRunner schedules are rebuilt after the update; no new background job
Database migration/data dependency: existing channel_monitors.interval_seconds and jitter_seconds columns; transactional update clamps jitter_seconds so interval_seconds - jitter_seconds >= 15
Environment-variable/config dependency: existing channel monitor runtime settings and scheduler; no new environment variable
Billing impact: none
Scheduling impact: monitoring cadence only; does not select request accounts or alter smart recovery routing
Client protocol impact: none
Upstream version/base commit: feature branch based on fd25b46c5
Tests/fixtures: backend service bulk interval unit tests; frontend bulk interval API Vitest; backend targeted service/handler/repository/routes tests; frontend typecheck and ESLint
Image smoke evidence: pending candidate build; this commit is not released to production
First release manifest: pending candidate build
Rollback note: revert the commit or restore the previous application image; no database migration rollback is required. Existing interval/jitter values can be restored from the pre-release database backup if the endpoint has been used.
Owner/status: fluter / implemented locally, pending release verification
```

Implementation note: IDs are de-duplicated and bounded to 100 per request. Setting
15 seconds automatically reduces an existing jitter above zero to zero; enabled
monitors are immediately rescheduled, while disabled monitors remain unscheduled.
