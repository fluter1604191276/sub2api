# Upstream Rates Admin Dashboard

This directory contains the small server-side ledger used by the
admin-only upstream cost dashboard.

- Database on VPS: `/var/lib/fluterapi-upstream-rates/upstream_rates.sqlite`
- Rendered page: `/www/fluterapi-home/admin/upstream-rates/index.html`
- Public URL: `https://fluterapi.top/admin/upstream-rates/`

The page is static HTML generated from SQLite. Caddy protects only the
`/admin/upstream-rates/` path with Basic Auth. Updating the ledger does not
require rebuilding or restarting sub2api.

Commands on the VPS:

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/seed_upstream_rates.py --reset
sudo python3 /var/lib/fluterapi-upstream-rates/refresh_kbq_token_models.py
sudo python3 /var/lib/fluterapi-upstream-rates/refresh_public_pricing_adapters.py
sudo python3 /var/lib/fluterapi-upstream-rates/audit_kbq_true_costs.py --local-postgres --hours 24
sudo python3 /var/lib/fluterapi-upstream-rates/emit_true_loss_alerts.py
sudo python3 /var/lib/fluterapi-upstream-rates/render_upstream_dashboard.py
```

Admin health and metrics service:

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/ledger_ai_server.py --host 127.0.0.1 --port 8751
```

The historical filename is retained for deployment compatibility. The service
now exposes only `/health` and `/metrics`; `/ai` has been removed. It reads
system status, allowlisted SQLite timestamps, Docker status, systemd timer
metadata, and backup file metadata. It does not read an AI API key or call the
sub2api model gateway. Caddy keeps the routes under the same Basic Auth path as
the dashboard.

The dashboard now focuses on five read-only modules: overview, infrastructure,
KBQ public pricing, KBQ true-cost risk, and operation metadata. Collection
configuration, balances, account multipliers, image-account operations, and run
history belong to S2A Manager and are not duplicated here.

Safe hourly refresh entrypoint:

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/refresh_upstream_ledger.py --local-postgres --hours 24
```

This imports upstream-hub observations when a local hub or sanitized snapshot is
available, refreshes KBQ public token pricing, refreshes public pricing adapters such as Junche
`/api/pricing`, snapshots current production account/group multipliers into the
independent ledger, recomputes recent KBQ true upstream costs from read-only
production usage logs, emits a dry-run true-loss alert summary, marks old
upstream groups that no longer appear in refreshed upstream pages/APIs, and
renders the static admin page. It does not run paid image-generation smoke
tests and does not edit sub2api accounts, groups, channels, pricing, or notes.

upstream-hub is now the preferred collection source for logged-in upstream
balances and group multipliers. The old Chrome/Tampermonkey/Safari collectors
are retained for diagnostics and rollback, but they are no longer the default
freshness source for the hourly ledger.

Preferred Mac → VPS sync entrypoint:

```bash
python3 ops/public-deploy/upstream-rates/sync_upstream_hub_snapshot_to_vps.py
```

This reads upstream-hub locally, validates that the exported JSON is sanitized,
copies only that snapshot to the VPS, and runs the VPS safe refresh with
`--upstream-hub-snapshot-json`. It does not expose upstream-hub Postgres, does
not copy login secrets, and does not edit production sub2api records.

Local LaunchAgent `com.fluter.upstream-hub-ledger-sync` is the default
unattended Mac-side trigger for this bridge. It runs
`run_upstream_hub_ledger_sync.sh` hourly, which wraps the command above with a
simple lock so overlapping syncs are skipped. The VPS timer is still responsible
for the server-side safe refresh and static dashboard render; it does not reach
into the Mac upstream-hub database directly. The old Codex automation
`fluter-upstream-ledger-hourly` is kept paused as a manual fallback for
human-readable summaries and incident review.

Install or refresh the LaunchAgent on the Mac:

```bash
chmod +x ops/public-deploy/upstream-rates/run_upstream_hub_ledger_sync.sh
mkdir -p ~/Library/Application\ Support/Fluter/upstream-ledger-sync
cp ops/public-deploy/upstream-rates/sync_upstream_hub_snapshot_to_vps.py \
  ops/public-deploy/upstream-rates/refresh_from_upstream_hub.py \
  ~/Library/Application\ Support/Fluter/upstream-ledger-sync/
cp ops/public-deploy/upstream-rates/launchd/fluter_upstream_hub_ledger_sync_launcher.sh \
  ~/Library/Application\ Support/Fluter/upstream-ledger-sync/fluter_upstream_hub_ledger_sync_launcher.sh
chmod +x ~/Library/Application\ Support/Fluter/upstream-ledger-sync/fluter_upstream_hub_ledger_sync_launcher.sh
cp ops/public-deploy/upstream-rates/launchd/com.fluter.upstream-hub-ledger-sync.plist \
  ~/Library/LaunchAgents/com.fluter.upstream-hub-ledger-sync.plist
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/com.fluter.upstream-hub-ledger-sync.plist
launchctl kickstart -k "gui/$(id -u)/com.fluter.upstream-hub-ledger-sync"
```

Logs:

```text
~/Library/Logs/fluter-upstream-hub-ledger-sync.out.log
~/Library/Logs/fluter-upstream-hub-ledger-sync.err.log
```

Local upstream-hub import can read Postgres through Docker compose or a local
TCP psql connection. On this Mac the Docker daemon is Colima, so the script
auto-detects `~/.colima/default/docker.sock` and injects `DOCKER_HOST` when the
normal `/var/run/docker.sock` context is stale. The compose service is
`postgres`; `upstreamhub-postgres` is only the container name. A healthy local
trial import should return quickly, for example:

```bash
python3 ops/public-deploy/upstream-rates/refresh_from_upstream_hub.py \
  --db /tmp/fluter-upstream-hub-import-test.sqlite \
  --hub-compose-dir /Users/fluter_claw/Desktop/study_project/upstream-hub \
  --hub-connection docker \
  --update-ledger-page-rates
```

The local TCP path reads `POSTGRES_PASSWORD` from upstream-hub's `.env` into
`PGPASSWORD` process environment only and never prints it. If TCP accepts the
connection but hangs, prefer the Docker path; do not copy Postgres credentials
into logs, chat, or the ledger.

When the ledger refresh runs on a different machine than upstream-hub, do not
try to expose upstream-hub Postgres over the network. Export a sanitized local
snapshot on the Mac, sync only that JSON to the ledger host, and import it
there:

```bash
python3 ops/public-deploy/upstream-rates/refresh_from_upstream_hub.py \
  --hub-compose-dir /Users/fluter_claw/Desktop/study_project/upstream-hub \
  --hub-connection docker \
  --export-json /tmp/fluter-upstream-hub-snapshot.json \
  --export-only

sudo python3 /var/lib/fluterapi-upstream-rates/refresh_upstream_ledger.py \
  --local-postgres \
  --hours 24 \
  --upstream-hub-snapshot-json /var/lib/fluterapi-upstream-rates/upstream-hub-snapshot.json
```

The snapshot schema stores only provider/channel names, site URLs, balances,
group/model ratios, timestamps, and rate-change observations. It must not store
login secrets, session material, full keys, or raw HTML. If the snapshot file is
missing, the hourly refresh skips only the upstream-hub import and continues
KBQ pricing, public pricing, read-only production snapshots, KBQ true-cost
audit, and rendering.

KBQ true-loss alert:

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/emit_true_loss_alerts.py
sudo python3 /var/lib/fluterapi-upstream-rates/emit_true_loss_alerts.py --endpoint http://127.0.0.1:8752/alerts
```

This reads only the latest `audit_kbq_true_costs.py` result from SQLite. It
never recomputes prices with a second formula, never reads production
credentials, and never edits production. Without `--endpoint` it is a dry-run
report. The default hourly refresh does not wire an alert endpoint; if a loopback
endpoint is explicitly provided, the script sends only when the latest audit has
`REAL_LOSS` buckets, then records the audit run id in SQLite so the same run
does not alert repeatedly. `DISPLAY_DRIFT` remains a display/accounting mismatch
and does not trigger Feishu/OpenClaw alerting. The endpoint must be loopback
(`127.0.0.1`, `localhost`, or `::1`) and must not include credentials or query
parameters.

To wire it into the hourly refresh, pass the local OpenClaw endpoint explicitly:

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/refresh_upstream_ledger.py --local-postgres --hours 24 --true-loss-alert-endpoint http://127.0.0.1:8752/alerts
```

If `--skip-kbq-audit` is used, the alert step is skipped too, so stale audit
data cannot be sent by accident.

API balance refresh:

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/refresh_balance_api_adapters.py --local-postgres
```

Deprecated after upstream-hub adoption. This reads active production account
`base_url` and `api_key` values into memory only and was useful before it became
clear that NewAPI dashboards require backend access tokens rather than OpenAI
compatible call keys. It is skipped by the default hourly refresh. Run it only
for a targeted diagnostic with `refresh_upstream_ledger.py
--include-balance-api-adapters`; do not treat 401/unsupported results as a
reason to store upstream backend access tokens.

Hourly refresh hard rule: it may read upstream pages/API balance endpoints,
write only the independent ledger SQLite database, and render static HTML. It
must never write production sub2api accounts, groups, channels, pricing, or user data. The
`sync_account_multipliers_from_ledger.py --create-drafts` path stays
manual-only and must not be put into cron or unattended automation. The old
direct-edit `--apply` path is intentionally disabled.

Removed upstream group cleanup:

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/cleanup_removed_upstream_groups.py --db /var/lib/fluterapi-upstream-rates/upstream_rates.sqlite
sudo python3 /var/lib/fluterapi-upstream-rates/cleanup_removed_upstream_groups.py --db /var/lib/fluterapi-upstream-rates/upstream_rates.sqlite --apply
```

The cleanup is conservative. It only marks a ledger row as
`上游分组已消失/待重映射` when upstream-hub, or a fresh current browser/key-page
fallback, saw enough rate/group lines for that provider, but the ledger's old
`upstream_group` was absent. A single observed group is treated as an
incomplete inventory and cannot remove old groups. Public `/api/pricing` rows
remain price references only and cannot prove a logged-in account group
disappeared. If the adapter saw `rate_lines=0`, a blank page, a failed public
pricing refresh, or only a balance line, the row is not cleaned because the
capture itself is incomplete. Already-marked rows stay visible in reports, but
`--apply` does not rewrite their notes again; metadata records both the current
candidate count and the count actually changed in this run. This writes only the
independent SQLite ledger and does not edit production sub2api records.

Important scope note: upstream-hub is the logged-in collection layer, while this
ledger is the cost/profit analysis layer. KBQ remains special because true
costs need `/api/pricing` plus production usage logs for cross-checking. Kingdom
and Kingdom Image are treated as one Kingdom provider:
`image.tokenskingdom.com` is only an image API fast subdomain, not a separate
dashboard. Browser snapshots are now fallback diagnostics; high-confidence
image costs still need usage-log APIs or a real request/流水 cross-check.

Local browser read-only diagnostic role:

- upstream-hub is the preferred logged-in collector for balances and group
  multipliers.
- Chrome/Tampermonkey is now only the diagnostic/rollback "eyes inside logged-in
  pages". It can still read currently visible, already logged-in upstream pages
  and send a small sanitized snapshot to `127.0.0.1`, but it is not the default
  freshness source.
- The local collector remains available for investigations. It validates
  token/source/body size, sanitizes again, and writes
  `local-snapshots/chrome/latest.json`; old/partial/preserved snapshots must
  stay in diagnostics and must not train ledger rows.
- Local Codex automation is still useful. It exports upstream-hub snapshots,
  syncs them to the VPS, summarizes freshness and missing providers, and avoids
  paid tests or production edits.
- The VPS remains the source of truth for the admin dashboard: it merges public
  pricing, upstream-hub snapshots, production account/group multiplier
  snapshots, KBQ true-cost audits, then renders static HTML.

Tampermonkey no longer replaces the normal collection path; upstream-hub does.
The default routine is upstream-hub sanitized snapshot first, VPS safe refresh
second. Browser snapshots are for targeted diagnosis or rollback only.

For Fluter's own sites, prefer direct server-side DB/API snapshots because we
own the server and can avoid DOM scraping. Add Tampermonkey coverage only for
read-only UI signals that are hard to obtain from the database, and whitelist
fields such as render time, visible status, balances, and public summaries.
Never collect full keys, tokens, cookies, raw HTML, or editable form values.
Sub2api-owned data must come from DB/internal API snapshots such as
`refresh_site_account_snapshot.py` and `audit_kbq_true_costs.py`; the browser
collector is only for third-party upstream dashboards where no reliable API is
available.

Local browser read-only diagnostic refresh:

```bash
python3 ops/public-deploy/upstream-rates/refresh_browser_readonly_adapters.py --remote-ssh-host us-api-vps
```

This legacy path refreshes already-open Safari tabs, extracts small sanitized
balance/rate summaries, and imports them into the VPS ledger for diagnostics.
It stores no cookies, passwords, full keys, Bearer tokens, or raw HTML.
Diagnostic browser rows must not override upstream-hub current observations or
produce production drift conclusions by themselves.

Chrome/Tampermonkey read-only diagnostic collector:

```bash
python3 ops/public-deploy/upstream-rates/chrome_readonly_collector.py
python3 ops/public-deploy/upstream-rates/chrome_readonly_collector.py --queue-command KBQ --command-reason "refresh before ledger sync"
python3 ops/public-deploy/upstream-rates/chrome_readonly_collector.py --sync-latest --remote-ssh-host us-api-vps
```

This is a legacy Chrome companion to the Safari diagnostic adapter. The
Tampermonkey script lives at
`ops/public-deploy/browser-userscripts/fluter-upstream-readonly-collector.user.js`.
It reads already-open logged-in upstream pages, sends a small sanitized summary
to a local collector bound to `127.0.0.1`, and writes
`ops/public-deploy/upstream-rates/local-snapshots/chrome/latest.json`.
The collector also serves the latest userscript from a normal local page:
`http://127.0.0.1:8799/install`. Prefer this page when installing/updating
Tampermonkey, because `.worktrees` is hidden in Finder. The raw script is at
`http://127.0.0.1:8799/userscript`, and the script also declares `@updateURL`
and `@downloadURL` pointing at
`http://127.0.0.1:8799/userscript/fluter-upstream-readonly-collector.user.js`.
After updating, reload an upstream page and confirm the bottom-right
`Fluter 上游采集` panel appears.
When a fresher logged-in page is needed, queue a one-shot command with
`--queue-command <provider-or-site>`. The userscript polls the local collector
every 45 seconds and only the matching open tab executes the command. The
default command action reloads that current page once, sends a forced sanitized
snapshot, and acknowledges completion. It does not refresh every upstream page,
does not run without the user's Chrome profile being logged in, and expires
after 5 minutes by default.
The collector checks `X-Collector-Token`, source domain, and body size, then
reuses the same `refresh_browser_readonly_adapters.py --import-json` contract to
sync sanitized account-name/rate observations to the VPS ledger. The token is stored at `~/.config/fluter-collector/token`
and should stay mode `600`; do not put it in the repo, chat, screenshots, or the
ledger database.
0.1.16 adds `mdkj.lol` / 乔燃 as a whitelisted logged-in dashboard provider.
0.1.12 adds `sub2.congmingai.com` as a whitelisted logged-in dashboard provider.
0.1.11 keeps those long-running browser safety guards and tightens extraction:
the userscript removes its own floating panel before reading page text, prefers
key-table semantics (`account name -> masked key -> upstream group -> x-rate`)
over whole-row guessing, supports `sk-xxxx********yyyy` masked keys, trims
compact NewAPI status prefixes such as `已启用 / 无限额度`, and stores concise
balance labels like `当前余额 ¥xx.xx`. 0.1.11 also rejects long dashboard text
that only contains balance words but no nearby amount, so noisy console copy
cannot overwrite a real wallet balance. The local collector mirrors the same
rules, filters panel/status noise, treats KBQ browser rows as balance/reference
only because KBQ cost truth comes from `/api/pricing`, and preserves the
previous high-signal snapshot when an incoming page read has no real
account/rate/balance signal.
Unlabeled wallet money such as the `聪明AI` dashboard's bare `$41.38` is accepted
only on logged-in dashboard/key-home pages. Pricing, usage, billing, recharge,
and spend-history contexts are excluded so model prices or usage charges cannot
be mistaken for wallet balance.

To diagnose whether the latest Chrome snapshot agrees with the rate markers in
upstream account names, run:

```bash
python3 ops/public-deploy/upstream-rates/check_browser_snapshot_account_markers.py
```

This local diagnostic understands Fluter's naming conventions: Kingdom-style
`page_rate * recharge_factor = cost_multiplier`, normal text-rate markers such
as `仅文字0.13`, and image-only markers such as `5.1分`. It reports real
`DRIFT` rows separately from informational image-price rows and never edits the
ledger or production sub2api accounts.

The collector is bound to `127.0.0.1`, compares the token with constant-time
comparison, and accepts browser source only from trusted `Origin`/`Referer`
domains. It does not trust caller-declared source headers. Command queue files
under `local-snapshots/chrome/commands/` contain only provider/site/action/status
metadata and sanitized reason/detail text.

Read-only production account multiplier snapshot:

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/refresh_site_account_snapshot.py --local-postgres
```

This reads `accounts.rate_multiplier` and each account's assigned group
`groups.rate_multiplier` into `site_account_snapshots`, then updates the ledger
display columns that show "账号成本倍率（内部）" and "用户分组倍率/售价". It writes
only the independent ledger SQLite database. It never edits production accounts.
Matching is intentionally conservative: exact account name wins; if the account
name changed only because the trailing multiplier changed, it may fall back to a
unique `(base host, account name stem)` match. The stem strips calculated suffixes
such as `0.2*0.9=0.18` and text-only/image-only tail labels, but it keeps pool
identity words such as `对接倍率`, `优质plus`, `福利plus`, and `pro破限`.
`ccmax` and `max` are normalized as the same family for matching. Ambiguous
matches are skipped rather than guessed.

Dry-run account multiplier sync from the ledger:

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/sync_account_multipliers_from_ledger.py --local-postgres
```

The sync script is intentionally dry-run by default. It compares
`upstream_rate_records.page_rate * recharge_factor` with production
`accounts.rate_multiplier`, skips image/special/inactive/unconfirmed records,
skips conservative cases where the production account multiplier is already
higher than the ledger-calculated cost, and prints planned disabled draft
accounts only for cost records that would otherwise be undercounted. This keeps
production as the source of truth for non-KBQ/manual providers and avoids
lowering a deliberately conservative internal cost record. Re-run with
`--create-drafts` only after reviewing the plan; draft mode creates a
PostgreSQL backup and inserts new accounts copied from the old accounts. It does
not edit old accounts. If an operator intentionally wants to draft lower
conservative multipliers, use `--include-conservative` explicitly.

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/sync_account_multipliers_from_ledger.py --local-postgres --create-drafts
```

Draft account rules:

- `name`: prefixed with `（修改）`.
- `status`: `active`, so the admin UI can show it normally.
- `schedulable`: `false`, so the scheduler cannot choose it.
- `account_groups`: no rows are inserted, so no user group can route to it.
- `rate_multiplier`: set to the ledger-calculated actual cost multiplier.
- `notes`: records the original account, old multiplier, new multiplier, ledger
  source, recharge factor, generated time, and "waiting for manual review".
- Runtime state such as rate-limit/cooldown/session windows is cleared; static
  config such as platform, credentials, model mapping, proxy, concurrency,
  priority, expiration, and load factor is copied.
- Duplicate protection keys off the original source account id recorded in the
  draft note (`原账号 id=...`), not just the draft name. If a draft is manually
  renamed but keeps its note, later runs still skip that source account.

The sync script uses the same matching rule as the snapshot script: exact name
first, then unique `(base host, account name stem)` fallback. This prevents
false "生产库找不到同名账号" reports when only the numeric multiplier suffix in an
account name changed, while still refusing ambiguous matches.

For non-KBQ sites, treat the current production account settings as the source
of truth unless a fresh upstream usage log or price API proves otherwise. Older
manual ledger rows can be useful history, but they should not override renamed
production accounts or deliberately conservative `accounts.rate_multiplier`
values.

Read-only account priority bucket planning:

```bash
sudo python3 /var/lib/fluterapi-upstream-rates/plan_account_priority_buckets.py --local-postgres
```

This learns Fluter's priority convention and prints only recommendations:

- `0-9`: protected manual range.
- `10-19`: codex ultra-low cost.
- `20-29`: codex low cost.
- `30-39`: codex value.
- `40-49`: codex pro low-cost power pool.
- `50-79`: codex pro fast/limit/fallback ranges.
- `80-99`: image and DeepSeek/special pools.
- `100+`: Claude ranges by group semantics.

The script does not support direct priority writes. Its deprecated `--apply`
flag exits with an error. `--write-notes` is also retired and exits before any
production read/write path. The dashboard may show the dry-run preview from
`--preview-db`, but it must not display a command that writes `accounts.notes`.
If account order should change, review the preview and adjust the admin UI
manually.

Do not expose draft creation as a casual one-click dashboard action yet. It
writes production account rows, even though the rows are disabled. A safe UI
would still need CSRF protection, second confirmation, preview hash, audit log,
backup path display, duplicate-draft detection, and rollback instructions. Until
then, create drafts only from an explicit operator command after a reviewed
dry-run.

Safety notes:

- Do not store full API keys, passwords, cookies, or Bearer tokens in the DB.
- Public `/api/pricing` snapshots do not overwrite curated
  `upstream_rate_records.page_rate` by default. Direct overwrite requires
  `--update-ledger-page-rates` and should be used only after confirming that the
  public group ratio is exactly the final Fluter cost multiplier.
- Treat `page_rate` as the upstream dashboard's visible multiplier for ordinary
  providers. For KBQ token models, `page_rate` is the pre-recharge cost
  multiplier derived from `/api/pricing`, not a stale key-page/group label.
- Treat `actual_cost_multiplier = page_rate * recharge_factor`. For KBQ token
  models, the refresh script reconciles curated account rows from
  `kbq_token_model_records.cost_multiplier`; if the curated row and model table
  disagree, the model table wins.
- Treat `accounts.rate_multiplier` as **账号成本倍率（内部）**. It is our own
  accounting record for an upstream account, so it should be as close as
  possible to `actual_cost_multiplier`. A slightly higher value is conservative;
  a lower value means our internal cost display may undercount cost. It does not
  directly change how much a user is charged.
- Treat `groups.rate_multiplier` / ledger `site_group_multiplier` as
  **用户分组倍率/售价**. This is the rate sold to users. Profit and loss are
  judged by comparing user billing or this user group rate against true upstream
  cost, not by comparing the internal account-cost record alone.
- Treat `upstream_discount_profiles` as the preferred recharge-discount source.
  It is the manual discount layer between upstream observed facts and Fluter's
  cost calculation. `upstream_rate_records.recharge_factor` remains only a
  compatibility snapshot/fallback for older rows. If a site-level profile exists,
  dashboard rendering and refresh orchestration should use it first.
- The cost engine is layered as:
  `upstream page rate or model-derived page cost × recharge_factor = true cost multiplier`.
  `page_rate` comes from upstream-hub / KBQ pricing / public pricing / curated
  rows; `recharge_factor` comes from the site discount profile; production
  `accounts.rate_multiplier` is then compared against that true cost as an
  internal cost record.
- 充值优惠统一这样算：如果 `A 元到账 B 刀/额度`，则
  `recharge_factor = A / B`，中文“几折”=`recharge_factor × 10`。例如
  Kingdom 当前 `148.88 RMB = 2000 USD`，所以 `recharge_factor=0.07444`，
  相当于 `0.7444 折`；页面 25x 的真实成本就是 `25 × 0.07444 = 1.861x`。
- 钧澈当前按“充100到账108”折算，`recharge_factor = 100 / 108 = 0.925926`，
  相当于约 `9.26 折`。例如 TEAM 0.025x 的真实现金成本是
  `0.025 × 0.925926 = 0.023148x`。
- KBQ still needs model-price conversion through `/api/pricing`; its visible
  `default 1x` or old account-name suffix is not the final Fluter cost ratio.
- KBQ 当前按 9 折充值折算，脚本默认 `recharge_factor=0.9`。也就是
  价格页/模型计算出来的页面成本倍率再乘 0.9，才是当前真实现金成本倍率。
  如果优惠结束或充值比例变化，先改 `refresh_kbq_token_models.py` 和
  `audit_kbq_true_costs.py` 的默认系数，再刷新台账，不要直接改生产账号。
- KBQ Codex plus/pro examples must be checked against live `/api/pricing`.
  2026-06-13 的 plus 口径是 `0.12 × 0.9 = 0.108`，pro 口径是
  `0.2 × 0.9 = 0.18`；不要再使用旧价 `0.08 × 0.9 = 0.072`。
- KBQ Claude loss checks should use `audit_kbq_true_costs.py`, not the raw
  `A成本` display alone. `REAL_LOSS` means true upstream cost exceeds
  `usage_logs.actual_cost`; `DISPLAY_DRIFT` means the admin display/account
  stats formula differs from the recomputed true cost.
- Channel pricing remains a display/baseline override tool. Do not force Claude
  1h cache-write prices into it while sub2api has only one `cache_write_price`
  field for both 5m and 1h cache writes.
- Logged-in upstream pages should be refreshed before reading balances/rates.
- Balance pages differ by provider: token/key pages may omit balances, while
  home/dashboard pages can show them.
- Image-generation costs should be confirmed from real upstream usage logs
  after 1K/2K/4K smoke calls; text multipliers alone are not enough.
- The `codex 生图+文字` pool is a mixed pool. Some accounts in it are text-only
  routing sources, so image failure on those accounts is not automatically a
  production bug.
- Scheduled/read-only ledger refreshes should update balances, price pages, and
  rendered HTML only. Do not run paid image smoke tests unless the user
  explicitly authorizes that test window.
- Ledger-to-account sync must start with dry-run. Never auto-apply image account
  costs from the multiplier ledger because many image accounts are billed per
  generated image rather than by text-token multiplier.

Local verification:

```bash
python3 -m py_compile ops/public-deploy/upstream-rates/*.py
rm -rf /tmp/fluter-upstream-rates-test
mkdir -p /tmp/fluter-upstream-rates-test
python3 ops/public-deploy/upstream-rates/seed_upstream_rates.py --db /tmp/fluter-upstream-rates-test/upstream_rates.sqlite --reset
python3 ops/public-deploy/upstream-rates/refresh_from_upstream_hub.py --db /tmp/fluter-upstream-rates-test/upstream_rates.sqlite --hub-compose-dir /Users/fluter_claw/Desktop/study_project/upstream-hub --hub-connection docker --update-ledger-page-rates
python3 ops/public-deploy/upstream-rates/refresh_kbq_token_models.py --db /tmp/fluter-upstream-rates-test/upstream_rates.sqlite
python3 ops/public-deploy/upstream-rates/refresh_public_pricing_adapters.py --db /tmp/fluter-upstream-rates-test/upstream_rates.sqlite
python3 ops/public-deploy/upstream-rates/refresh_site_account_snapshot.py --db /tmp/fluter-upstream-rates-test/upstream_rates.sqlite --ssh-host us-api-vps
python3 ops/public-deploy/upstream-rates/audit_kbq_true_costs.py --db /tmp/fluter-upstream-rates-test/upstream_rates.sqlite --ssh-host us-api-vps --hours 24
python3 ops/public-deploy/upstream-rates/render_upstream_dashboard.py --db /tmp/fluter-upstream-rates-test/upstream_rates.sqlite --output /tmp/fluter-upstream-rates-test/index.html
python3 ops/public-deploy/upstream-rates/refresh_upstream_ledger.py --db /tmp/fluter-upstream-rates-test/upstream_rates.sqlite --output /tmp/fluter-upstream-rates-test/index.html --skip-kbq-audit
python3 ops/public-deploy/upstream-rates/sync_account_multipliers_from_ledger.py --db /tmp/fluter-upstream-rates-test/upstream_rates.sqlite --ssh-host us-api-vps
python3 ops/public-deploy/upstream-rates/ledger_ai_server.py --db /tmp/fluter-upstream-rates-test/upstream_rates.sqlite --host 127.0.0.1 --port 8751
```
