#!/usr/bin/env node

import { mkdir, writeFile } from 'node:fs/promises';
import { dirname } from 'node:path';
import { spawn } from 'node:child_process';

const DEFAULT_OUTPUT = 'ops/public-deploy/reports/upstream-cost-audit.md';
const DEFAULT_JSON_OUTPUT = 'ops/public-deploy/reports/upstream-cost-audit.json';
const DEFAULT_SSH_HOST = 'fluterapi-prod';

function parseArgs(argv) {
  const args = {
    sshHost: process.env.SUB2API_SSH_HOST || DEFAULT_SSH_HOST,
    output: DEFAULT_OUTPUT,
    jsonOutput: DEFAULT_JSON_OUTPUT,
    timeoutMs: 8000,
    includeInactive: false,
    probe: true,
  };

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--ssh-host') {
      args.sshHost = argv[++i];
    } else if (arg === '--output') {
      args.output = argv[++i];
    } else if (arg === '--json-output') {
      args.jsonOutput = argv[++i];
    } else if (arg === '--timeout-ms') {
      args.timeoutMs = Number(argv[++i]);
    } else if (arg === '--include-inactive') {
      args.includeInactive = true;
    } else if (arg === '--no-probe') {
      args.probe = false;
    } else if (arg === '--help' || arg === '-h') {
      printHelp();
      process.exit(0);
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  return args;
}

function printHelp() {
  console.log(`Usage:
  node ops/public-deploy/scripts/audit-upstream-costs.mjs

Read-only upstream cost audit for Fluter public sub2api.

Options:
  --ssh-host <host>       SSH host alias for production. Default: ${DEFAULT_SSH_HOST}
  --output <path>         Markdown report path. Default: ${DEFAULT_OUTPUT}
  --json-output <path>    JSON report path. Default: ${DEFAULT_JSON_OUTPUT}
  --timeout-ms <number>   Public pricing probe timeout. Default: 8000
  --include-inactive      Include inactive/deleted-off accounts in the report.
  --no-probe              Skip public pricing probes and only list manual checks.
`);
}

function runProcess(command, args, { input } = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    child.stdout.setEncoding('utf8');
    child.stderr.setEncoding('utf8');
    child.stdout.on('data', (chunk) => {
      stdout += chunk;
    });
    child.stderr.on('data', (chunk) => {
      stderr += chunk;
    });
    child.on('error', reject);
    child.on('close', (code) => {
      if (code !== 0) {
        reject(new Error(`${command} exited ${code}: ${stderr.trim()}`));
        return;
      }
      resolve(stdout);
    });
    if (input) {
      child.stdin.write(input);
    }
    child.stdin.end();
  });
}

async function loadAccounts(sshHost, { includeInactive }) {
  const statusFilter = includeInactive
    ? ''
    : "and a.status = 'active' and a.deleted_at is null";
  const sql = `
copy (
  select coalesce(jsonb_agg(row_to_json(t) order by id), '[]'::jsonb)
  from (
    select
      a.id,
      a.name,
      a.platform,
      a.type,
      a.status,
      a.schedulable,
      a.rate_multiplier::text as rate_multiplier,
      coalesce(a.credentials->>'base_url', '') as base_url,
      coalesce(a.credentials->'model_mapping', '{}'::jsonb) as model_mapping,
      coalesce(
        jsonb_agg(
          jsonb_build_object(
            'id', g.id,
            'name', g.name,
            'rate_multiplier', g.rate_multiplier::text,
            'status', g.status
          )
          order by g.id
        ) filter (where g.id is not null),
        '[]'::jsonb
      ) as groups
    from accounts a
    left join account_groups ag on ag.account_id = a.id
    left join groups g on g.id = ag.group_id and g.deleted_at is null
    where coalesce(a.credentials->>'base_url', '') <> ''
      ${statusFilter}
    group by a.id
  ) t
) to stdout;
`;

  const stdout = await runProcess(
    'ssh',
    [
      sshHost,
      'docker exec -i sub2api-postgres psql -U sub2api -d sub2api -At',
    ],
    { input: sql },
  );

  return JSON.parse(stdout.trim() || '[]').map((account) => ({
    ...account,
    id: Number(account.id),
    schedulable: Boolean(account.schedulable),
    rate_multiplier: Number(account.rate_multiplier),
    model_mapping: account.model_mapping || {},
    groups: account.groups || [],
  }));
}

function normalizeBaseUrl(url) {
  const value = url.includes('://') ? url : `https://${url}`;
  const parsed = new URL(value);
  return parsed.toString().replace(/\/+$/, '');
}

function originOf(url) {
  return new URL(normalizeBaseUrl(url)).origin;
}

function hostOf(url) {
  return new URL(normalizeBaseUrl(url)).host;
}

function isTrustedCostHost(host) {
  const normalized = host.toLowerCase();
  return normalized === 'xn--vduyey89e.com' || normalized === 'kbq.de5.net';
}

function unique(values) {
  return [...new Set(values)];
}

function candidatePricingUrls(baseUrl) {
  const normalized = normalizeBaseUrl(baseUrl);
  const parsed = new URL(normalized);
  const origin = parsed.origin;
  const bases = [origin];
  if (parsed.pathname && parsed.pathname !== '/' && !/\/v\d+\/?$/.test(parsed.pathname)) {
    bases.push(normalized);
  }

  return unique(
    bases.flatMap((base) => [
      `${base}/api/pricing`,
      `${base}/api/model/pricing`,
      `${base}/api/prices`,
    ]),
  );
}

async function fetchJson(url, timeoutMs) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(url, {
      headers: {
        Accept: 'application/json',
        'User-Agent': 'fluter-sub2api-upstream-cost-audit/1.0',
      },
      signal: controller.signal,
    });
    const text = await response.text();
    if (!response.ok) {
      return {
        ok: false,
        status: response.status,
        statusText: response.statusText,
        preview: text.slice(0, 160).replace(/\s+/g, ' '),
      };
    }
    try {
      return { ok: true, json: JSON.parse(text) };
    } catch {
      return {
        ok: false,
        status: response.status,
        statusText: 'Non-JSON response',
        preview: text.slice(0, 160).replace(/\s+/g, ' '),
      };
    }
  } catch (error) {
    return {
      ok: false,
      status: 'ERR',
      statusText: error.name === 'AbortError' ? 'timeout' : error.message,
      preview: '',
    };
  } finally {
    clearTimeout(timeout);
  }
}

function looksLikeNewApiPricing(data) {
  return (
    data &&
    Array.isArray(data.data) &&
    data.data.some(
      (item) =>
        item &&
        typeof item === 'object' &&
        typeof item.model_name === 'string' &&
        ('model_ratio' in item || 'model_price' in item),
    )
  );
}

function groupRatio(pricing) {
  return Number(pricing.group_ratio?.default ?? pricing.group_ratio?.auto ?? 1);
}

function numberOrNull(value) {
  if (value === null || value === undefined || value === '') {
    return null;
  }
  const n = Number(value);
  return Number.isFinite(n) ? n : null;
}

function upstreamPrices(item, pricing) {
  if (!item) {
    return {};
  }
  const ratio = groupRatio(pricing);
  if (item.quota_type === 1) {
    const perCall = numberOrNull(item.model_price);
    return {
      billingType: 'per_call',
      perCallUsd: perCall === null ? null : perCall * ratio,
    };
  }

  const modelRatio = numberOrNull(item.model_ratio) || 0;
  const completionRatio = numberOrNull(item.completion_ratio) || 1;
  const input = modelRatio * ratio * 2;
  const cacheReadRatio = numberOrNull(item.cache_ratio);
  const cacheWriteRatio = numberOrNull(item.create_cache_ratio);
  return {
    billingType: 'token',
    inputUsdPer1M: input,
    outputUsdPer1M: input * completionRatio,
    cacheReadUsdPer1M: cacheReadRatio === null ? null : input * cacheReadRatio,
    cacheWriteUsdPer1M: cacheWriteRatio === null ? null : input * cacheWriteRatio,
    rawModelRatio: modelRatio,
  };
}

function officialPricesFor(model) {
  const m = String(model).toLowerCase();

  if (m.includes('gpt-5.5')) {
    return { inputUsdPer1M: 5, outputUsdPer1M: 30, cacheReadUsdPer1M: 0.5 };
  }
  if (m.includes('gpt-5.4-mini')) {
    return { inputUsdPer1M: 0.75, outputUsdPer1M: 4.5, cacheReadUsdPer1M: 0.075 };
  }
  if (m.includes('gpt-5.4')) {
    return { inputUsdPer1M: 2.5, outputUsdPer1M: 15, cacheReadUsdPer1M: 0.25 };
  }

  if (m.includes('claude')) {
    if (m.includes('haiku-4-5')) {
      return {
        inputUsdPer1M: 1,
        outputUsdPer1M: 5,
        cacheReadUsdPer1M: 0.1,
        cacheWriteUsdPer1M: 1.25,
      };
    }
    if (m.includes('sonnet-4')) {
      return {
        inputUsdPer1M: 3,
        outputUsdPer1M: 15,
        cacheReadUsdPer1M: 0.3,
        cacheWriteUsdPer1M: 3.75,
      };
    }
    if (m.includes('opus-4-1') || m.includes('opus-4-20250514')) {
      return {
        inputUsdPer1M: 15,
        outputUsdPer1M: 75,
        cacheReadUsdPer1M: 1.5,
        cacheWriteUsdPer1M: 18.75,
      };
    }
    if (m.includes('opus-4')) {
      return {
        inputUsdPer1M: 5,
        outputUsdPer1M: 25,
        cacheReadUsdPer1M: 0.5,
        cacheWriteUsdPer1M: 6.25,
      };
    }
  }

  // DeepSeek changes often; keep this as an alerting baseline only.
  if (m.includes('deepseek-v4-flash')) {
    return { inputUsdPer1M: 0.14, outputUsdPer1M: 0.28, cacheReadUsdPer1M: 0.0028 };
  }
  if (m.includes('deepseek-v4-pro')) {
    return { inputUsdPer1M: 0.435, outputUsdPer1M: 0.87, cacheReadUsdPer1M: 0.003625 };
  }

  return null;
}

function maxCostRatio(live, official) {
  if (!official || live.billingType !== 'token') {
    return null;
  }
  const ratios = [
    ratio(live.inputUsdPer1M, official.inputUsdPer1M),
    ratio(live.outputUsdPer1M, official.outputUsdPer1M),
    ratio(live.cacheReadUsdPer1M, official.cacheReadUsdPer1M),
    ratio(live.cacheWriteUsdPer1M, official.cacheWriteUsdPer1M),
  ].filter((value) => value !== null);
  return ratios.length ? Math.max(...ratios) : null;
}

function ratio(live, official) {
  if (live === null || live === undefined || !official) {
    return null;
  }
  return live / official;
}

function statusFor(account, item, live, costRatio) {
  if (!item) {
    return 'MISSING';
  }
  if (live.billingType === 'per_call') {
    return 'PER_CALL';
  }
  if (costRatio === null) {
    return 'NO_BASELINE';
  }
  const recorded = Number(account.rate_multiplier || 0);
  if (costRatio >= recorded + 0.05 || (recorded > 0 && costRatio >= recorded * 1.2)) {
    return 'RISK';
  }
  if (costRatio >= recorded + 0.01) {
    return 'WATCH';
  }
  return 'OK';
}

async function probeHost(baseUrl, timeoutMs) {
  const attempts = [];
  for (const url of candidatePricingUrls(baseUrl)) {
    const result = await fetchJson(url, timeoutMs);
    attempts.push({
      url,
      ok: result.ok,
      status: result.status ?? 200,
      statusText: result.statusText ?? 'OK',
      preview: result.preview || '',
    });
    if (result.ok && looksLikeNewApiPricing(result.json)) {
      return {
        kind: 'newapi_pricing',
        sourceUrl: url,
        pricingVersion: result.json.pricing_version || '',
        modelCount: result.json.data.length,
        data: result.json,
        attempts,
      };
    }
  }
  return {
    kind: 'manual_required',
    attempts,
  };
}

async function probeHosts(accounts, timeoutMs, shouldProbe) {
  const byHost = new Map();
  for (const account of accounts) {
    const host = hostOf(account.base_url);
    if (!byHost.has(host)) {
      byHost.set(host, {
        host,
        baseUrls: [],
        accounts: [],
      });
    }
    const entry = byHost.get(host);
    entry.baseUrls.push(account.base_url);
    entry.accounts.push(account);
  }

  const hostReports = [];
  for (const entry of [...byHost.values()].sort((a, b) => a.host.localeCompare(b.host))) {
    const baseUrl = entry.baseUrls[0];
    const probe = shouldProbe
      ? await probeHost(baseUrl, timeoutMs)
      : { kind: 'manual_required', attempts: [] };
    hostReports.push({
      ...entry,
      baseUrls: unique(entry.baseUrls),
      trustedCost: isTrustedCostHost(entry.host),
      probe,
    });
  }
  return hostReports;
}

function buildAccountRows(hostReports) {
  const rows = [];
  for (const hostReport of hostReports) {
    if (hostReport.probe.kind !== 'newapi_pricing' || !hostReport.trustedCost) {
      continue;
    }
    const models = new Map(
      hostReport.probe.data.data
        .filter((item) => item && item.model_name)
        .map((item) => [item.model_name, item]),
    );
    for (const account of hostReport.accounts) {
      for (const [publicModel, upstreamModel] of Object.entries(account.model_mapping || {})) {
        const item = models.get(upstreamModel);
        const live = upstreamPrices(item, hostReport.probe.data);
        const official = officialPricesFor(publicModel);
        const costRatio = maxCostRatio(live, official);
        rows.push({
          status: statusFor(account, item, live, costRatio),
          host: hostReport.host,
          accountId: account.id,
          accountName: account.name,
          platform: account.platform,
          schedulable: account.schedulable,
          recordedMultiplier: account.rate_multiplier,
          groups: account.groups,
          publicModel,
          upstreamModel,
          billingType: live.billingType || 'missing',
          inputUsdPer1M: live.inputUsdPer1M ?? null,
          outputUsdPer1M: live.outputUsdPer1M ?? null,
          cacheReadUsdPer1M: live.cacheReadUsdPer1M ?? null,
          cacheWriteUsdPer1M: live.cacheWriteUsdPer1M ?? null,
          perCallUsd: live.perCallUsd ?? null,
          rawModelRatio: live.rawModelRatio ?? null,
          costRatio,
        });
      }
    }
  }
  return rows.sort((a, b) => {
    const priority = { RISK: 0, WATCH: 1, MISSING: 2, PER_CALL: 3, NO_BASELINE: 4, OK: 5 };
    return (
      (priority[a.status] ?? 9) - (priority[b.status] ?? 9) ||
      a.accountId - b.accountId ||
      a.publicModel.localeCompare(b.publicModel)
    );
  });
}

function fmtNumber(value, digits = 4) {
  if (value === null || value === undefined || Number.isNaN(value)) {
    return '-';
  }
  const fixed = Number(value).toFixed(digits);
  return fixed.replace(/\.?0+$/, '');
}

function fmtGroups(groups) {
  if (!groups?.length) {
    return 'NO_GROUP';
  }
  return groups.map((group) => `${group.name}@${fmtNumber(Number(group.rate_multiplier), 4)}`).join(', ');
}

function escapeCell(value) {
  return String(value ?? '-').replace(/\|/g, '\\|').replace(/\n/g, ' ');
}

function table(headers, rows) {
  return [
    `| ${headers.map(escapeCell).join(' | ')} |`,
    `| ${headers.map(() => '---').join(' | ')} |`,
    ...rows.map((row) => `| ${row.map(escapeCell).join(' | ')} |`),
  ].join('\n');
}

function buildMarkdown({ accounts, hostReports, rows, args }) {
  const now = new Date().toISOString();
  const autoHosts = hostReports.filter((host) => host.probe.kind === 'newapi_pricing');
  const trustedAutoHosts = autoHosts.filter((host) => host.trustedCost);
  const publicOnlyHosts = autoHosts.filter((host) => !host.trustedCost);
  const manualHosts = hostReports.filter(
    (host) => host.probe.kind !== 'newapi_pricing' || !host.trustedCost,
  );
  const riskRows = rows.filter((row) => row.status === 'RISK');
  const watchRows = rows.filter((row) => row.status === 'WATCH');
  const missingRows = rows.filter((row) => row.status === 'MISSING');

  const lines = [];
  lines.push('# Fluter 上游成本与倍率巡检');
  lines.push('');
  lines.push(`生成时间：\`${now}\``);
  lines.push('');
  lines.push('模式：只读。脚本不会读取、打印或上传任何 API key。');
  lines.push('');
  lines.push('## 汇总');
  lines.push('');
  lines.push(
    table(
      ['项目', '数量'],
      [
        ['生产账号', accounts.length],
        ['上游域名', hostReports.length],
        ['自动识别公开价格接口', autoHosts.length],
        ['可直接用于成本判定', trustedAutoHosts.length],
        ['仅公开价格参考', publicOnlyHosts.length],
        ['需要网页登录/人工流水确认', manualHosts.length],
        ['RISK', riskRows.length],
        ['WATCH', watchRows.length],
        ['MISSING', missingRows.length],
      ],
    ),
  );
  lines.push('');
  lines.push('## 自动价格接口');
  lines.push('');
  if (autoHosts.length) {
    lines.push(
      table(
        ['域名', '接口', '版本', '模型数', '账号'],
        autoHosts.map((host) => [
          host.host,
          host.probe.sourceUrl,
          host.probe.pricingVersion || '-',
          host.probe.modelCount,
          `${host.trustedCost ? '可信成本口径' : '仅公开价/需后台确认'}：${host.accounts
            .map((account) => `#${account.id} ${account.name}`)
            .join('; ')}`,
        ]),
      ),
    );
  } else {
    lines.push('没有识别到可自动读取的公开价格接口。');
  }
  lines.push('');

  lines.push('## 自动比对结果');
  lines.push('');
  if (rows.length) {
    lines.push(
      table(
        [
          '状态',
          '账号',
          '记录倍率',
          '成本倍率',
          '分组',
          '模型',
          '上游模型',
          '输入/1M',
          '输出/1M',
          '缓存读/1M',
          '按次',
          'raw ratio',
        ],
        rows.map((row) => [
          row.status,
          `#${row.accountId} ${row.accountName}`,
          fmtNumber(row.recordedMultiplier, 4),
          fmtNumber(row.costRatio, 4),
          fmtGroups(row.groups),
          row.publicModel,
          row.upstreamModel,
          fmtNumber(row.inputUsdPer1M, 6),
          fmtNumber(row.outputUsdPer1M, 6),
          fmtNumber(row.cacheReadUsdPer1M, 6),
          fmtNumber(row.perCallUsd, 6),
          fmtNumber(row.rawModelRatio, 6),
        ]),
      ),
    );
  } else {
    lines.push('没有可自动比对的模型映射。');
  }
  lines.push('');

  lines.push('## 需要人工确认的上游');
  lines.push('');
  lines.push(
    '这些站点没有可直接用于“你这把 API key 成本倍率”的可信自动口径。即使扫到了公开 `/api/pricing`，也可能只是站点默认价，不等于你的 API key 专属分组倍率。下一步应登录对应后台查看“API key 分组倍率/模型价格/调用流水”，或通过小额真实调用反推成本。',
  );
  lines.push('');
  if (manualHosts.length) {
    lines.push(
      table(
        ['域名', 'base_url', '账号', '探测结果'],
        manualHosts.map((host) => [
          host.host,
          host.baseUrls.join(', '),
          host.accounts
            .map(
              (account) =>
                `#${account.id} ${account.name}(${fmtNumber(account.rate_multiplier, 4)})${
                  account.schedulable ? '' : '[off]'
                }`,
            )
            .join('; '),
          host.probe.kind === 'newapi_pricing'
            ? `公开价格接口已发现：${host.probe.sourceUrl}；未验证为 key 专属价`
            : host.probe.attempts
                .slice(0, 3)
                .map((attempt) => `${attempt.status} ${attempt.statusText}`)
                .join('; ') || '未探测',
        ]),
      ),
    );
  } else {
    lines.push('暂无。');
  }
  lines.push('');

  lines.push('## 解释');
  lines.push('');
  lines.push('- `记录倍率` 来自本站 `accounts.rate_multiplier`，是我们给这个上游账号写下的成本记录。');
  lines.push('- `成本倍率` 是脚本按 `上游实际价 / 官方基准价` 换算出来的值。');
  lines.push('- `raw ratio` 是上游 NewAPI 原始字段，不直接等于我们的成本倍率。');
  lines.push('- 非 KBQ 的公开 `/api/pricing` 默认只当作参考，不直接判亏本；很多站点会给不同 API key 分配不同分组倍率。');
  lines.push('- `RISK` 表示当前自动换算成本明显高于本站记录倍率。');
  lines.push('- `PER_CALL` 表示上游按次计费，不能直接套 token 倍率，通常需要单独决定是否接入。');
  lines.push('- 本报告不修改账号、分组或渠道；发现风险后再决定是否备份并改价。');
  lines.push('');
  lines.push('## 运行参数');
  lines.push('');
  lines.push(`- sshHost: \`${args.sshHost}\``);
  lines.push(`- timeoutMs: \`${args.timeoutMs}\``);
  lines.push(`- includeInactive: \`${args.includeInactive}\``);
  lines.push(`- probe: \`${args.probe}\``);
  lines.push('');

  return lines.join('\n');
}

function sanitizeAccount(account) {
  return {
    id: account.id,
    name: account.name,
    platform: account.platform,
    type: account.type,
    status: account.status,
    schedulable: account.schedulable,
    rate_multiplier: account.rate_multiplier,
    base_url: account.base_url,
    model_mapping: account.model_mapping,
    groups: account.groups,
  };
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const accounts = await loadAccounts(args.sshHost, {
    includeInactive: args.includeInactive,
  });
  const hostReports = await probeHosts(accounts, args.timeoutMs, args.probe);
  const rows = buildAccountRows(hostReports);
  const report = {
    generatedAt: new Date().toISOString(),
    mode: 'read-only',
    args,
    accounts: accounts.map(sanitizeAccount),
    hostReports: hostReports.map((host) => ({
      ...host,
      accounts: host.accounts.map((account) => ({
        id: account.id,
        name: account.name,
        platform: account.platform,
        status: account.status,
        schedulable: account.schedulable,
        rate_multiplier: account.rate_multiplier,
        groups: account.groups,
      })),
      probe:
        host.probe.kind === 'newapi_pricing'
          ? {
              kind: host.probe.kind,
              trustedCost: host.trustedCost,
              sourceUrl: host.probe.sourceUrl,
              pricingVersion: host.probe.pricingVersion,
              modelCount: host.probe.modelCount,
              attempts: host.probe.attempts,
            }
          : host.probe,
    })),
    rows,
  };
  const markdown = buildMarkdown({ accounts, hostReports, rows, args });

  await mkdir(dirname(args.output), { recursive: true });
  await mkdir(dirname(args.jsonOutput), { recursive: true });
  await writeFile(args.output, markdown);
  await writeFile(args.jsonOutput, JSON.stringify(report, null, 2));

  console.log(`Wrote ${args.output}`);
  console.log(`Wrote ${args.jsonOutput}`);
}

main().catch((error) => {
  console.error(error.message);
  process.exit(1);
});
