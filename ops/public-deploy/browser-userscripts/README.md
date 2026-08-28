# Fluter Chrome Read-Only Collector

这套东西是给上游倍率台账用的 Chrome 前端：Chrome 负责读你已经登录的上游后台页面，本机 collector 负责二次脱敏和落盘，然后继续复用现有 `refresh_browser_readonly_adapters.py --import-json` 同步到 VPS 台账。

它只读页面文字，不保存 Cookie、密码、完整 API key、Bearer token，也不改上游后台。

## 文件

- Tampermonkey 脚本：`ops/public-deploy/browser-userscripts/fluter-upstream-readonly-collector.user.js`
- 本机 collector：`ops/public-deploy/upstream-rates/chrome_readonly_collector.py`
- 本机快照目录：`ops/public-deploy/upstream-rates/local-snapshots/chrome/`

## 第一次安装

1. 用 Chrome 安装 Tampermonkey。
2. 新建一个单独 Chrome Profile，例如 `Fluter 上游监控`，只在这里登录上游后台。
3. 启动本机 collector 后，在 Chrome 打开 `http://127.0.0.1:8799/install`，点“安装 / 更新 userscript”。这样不用去 Finder 里找隐藏的 `.worktrees` 目录。
4. 生成本机 collector token：

```bash
python3 ops/public-deploy/upstream-rates/chrome_readonly_collector.py --init-token
```

5. 自己打开 `~/.config/fluter-collector/token`，把 token 粘到 Tampermonkey 菜单里的 `设置 Fluter collector token`。不要把 token 发到聊天里，也不要写进仓库。

## 日常使用

先启动本机 collector：

```bash
python3 ops/public-deploy/upstream-rates/chrome_readonly_collector.py
```

然后在 Chrome 里打开已登录的上游页面。脚本只在这些白名单域名运行：

- `api.saki.lat`、`saki.lat`
- `pool.gptstore.club`、`gptstore.club`
- `api.tokenskingdom.com`、`image.tokenskingdom.com`、`tokenskingdom.com`
- `api.mouubox.com`
- `sub2api.mouubox.com`
- `sub2.congmingai.com`
- `xn--vduyey89e.com`
- `vip.lcodex.cn`、`lcodex.cn`

脚本会自动发送当前页的脱敏摘要，并尽量识别页面里的上游账号名、分组和倍率。也可以从右下角“Fluter 上游采集”悬浮窗或 Tampermonkey 菜单点 `发送当前页只读快照` 手动发送。

0.1.12 起，脚本额外覆盖 `sub2.congmingai.com`。0.1.11 起，脚本会先移除自己的悬浮窗再读取页面文字，并优先按 key 表格语义解析账号行：`账号名 -> masked key -> 上游分组 -> x 倍率`。它支持 `sk-xxx...yyy`、`sk-xxxxxxxx...redacted` 和 `sk-xxxx********yyyy` 这几类脱敏 key；遇到钧澈这类紧凑表格行时，会从“已启用/无限额度”等状态词前切出账号名，并把余额整理成 `当前余额 ¥xx.xx` 这种短标签。0.1.11 还会拒绝“有余额字样但附近没有金额”的长控制台文案，避免把整段页面文字当余额。本机 collector 也会过滤悬浮窗/状态文字；如果某次自动发送只抓到面板或空页，`latest.json` 会保留上一份有账号/倍率信号的快照，避免越跑越脏。

Codex 也可以让某个已经打开的上游页“刷新一次后发送快照”。这不是远程控制 Chrome，也不会刷新所有站点；它只是往本机 collector 写一个一次性命令，油猴脚本每 45 秒轮询一次，只在当前打开页面匹配对应上游时执行。

例子：

```bash
python3 ops/public-deploy/upstream-rates/chrome_readonly_collector.py \
  --queue-command KBQ \
  --command-reason "refresh before ledger comparison"
```

也可以用站点域名：

```bash
python3 ops/public-deploy/upstream-rates/chrome_readonly_collector.py \
  --queue-command pool.gptstore.club \
  --command-reason "refresh Magic before ledger sync"
```

默认命令 5 分钟过期，同一上游 2 分钟内不会连续强制刷新；冷却期内会改为只发送当前页快照。命令状态保存在：

```text
ops/public-deploy/upstream-rates/local-snapshots/chrome/commands/commands.json
```

查看本机最新快照：

```bash
python3 -m json.tool ops/public-deploy/upstream-rates/local-snapshots/chrome/latest.json
```

同步到 VPS 台账：

```bash
python3 ops/public-deploy/upstream-rates/chrome_readonly_collector.py --sync-latest --remote-ssh-host fluterapi-prod
```

这一步会调用现有脚本：

```bash
python3 ops/public-deploy/upstream-rates/refresh_browser_readonly_adapters.py \
  --import-json ops/public-deploy/upstream-rates/local-snapshots/chrome/latest.json \
  --remote-ssh-host fluterapi-prod
```

## 安全边界

- collector 只绑定 `127.0.0.1`，不对公网开放。
- 接收 `POST /ingest` 写快照、`GET /commands` 给油猴领取一次性命令、`POST /command-ack` 确认命令结果。
- 本机 operator 可用 `--queue-command` 或 `POST /commands` 创建一次性命令。
- 请求必须带 `X-Collector-Token`。
- `Origin` / `Referer` 或脚本自带的 `X-Collector-Source` 必须命中白名单域名。
- body 默认最大 `256KB`。
- collector 会二次脱敏，只保留 provider/site/browser/status/detail/time/url/title/balance/accounts/rates/excerpt 这些字段。
- 请求线程只落本地 JSON，不直接 SSH；同步需要单独执行。
- 命令队列只保存 provider/site/action/status/time/reason/detail，不保存 Cookie、密码、API key 或页面正文。

## 验收

```bash
python3 -m py_compile ops/public-deploy/upstream-rates/chrome_readonly_collector.py
python3 -m py_compile ops/public-deploy/upstream-rates/refresh_browser_readonly_adapters.py
python3 -m unittest ops.public-deploy.upstream-rates.test_chrome_readonly_collector
```

用临时 token 和临时快照目录可以做本机 HTTP 测试，确认错误 token、错误来源、合法 payload 都按预期处理。
