#!/usr/bin/env node

import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

const DEFAULT_BASE_URL = 'https://xn--vduyey89e.com';
const DEFAULT_OUTPUT =
  'ops/public-deploy/reports/kbq-openai-anthropic-pricing.md';
const DEFAULT_JSON_OUTPUT =
  'ops/public-deploy/reports/kbq-openai-anthropic-pricing.json';

function parseArgs(argv) {
  const args = {
    baseUrl: process.env.KBQ_API_BASE_URL || DEFAULT_BASE_URL,
    output: DEFAULT_OUTPUT,
    jsonOutput: DEFAULT_JSON_OUTPUT,
    pricingOnly: false,
    tokenOnly: false,
    timeoutMs: 20_000,
  };

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--pricing-only') {
      args.pricingOnly = true;
    } else if (arg === '--token-only') {
      args.tokenOnly = true;
    } else if (arg === '--base-url') {
      args.baseUrl = argv[++i];
    } else if (arg === '--output') {
      args.output = argv[++i];
    } else if (arg === '--json-output') {
      args.jsonOutput = argv[++i];
    } else if (arg === '--timeout-ms') {
      args.timeoutMs = Number(argv[++i]);
    } else if (arg === '--help' || arg === '-h') {
      printHelp();
      process.exit(0);
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  args.baseUrl = args.baseUrl.replace(/\/+$/, '');
  return args;
}

function printHelp() {
  console.log(`Usage:
  KBQ_API_KEY=sk-... node ops/public-deploy/scripts/generate-kbq-pricing-table.mjs

Options:
  --pricing-only            Generate from /api/pricing only. Skips /v1/models.
  --token-only              Exclude per-call models and keep quota_type=0 only.
  --base-url <url>          Upstream API base URL. Default: ${DEFAULT_BASE_URL}
  --output <path>           Markdown output path.
  --json-output <path>      JSON output path.
  --timeout-ms <number>     Request timeout in milliseconds. Default: 20000
`);
}

async function fetchJson(url, { timeoutMs, headers = {} }) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(url, {
      headers,
      signal: controller.signal,
    });
    const text = await response.text();
    if (!response.ok) {
      throw new Error(
        `${response.status} ${response.statusText} from ${url}: ${text.slice(
          0,
          300,
        )}`,
      );
    }
    return JSON.parse(text);
  } finally {
    clearTimeout(timeout);
  }
}

function vendorNameById(pricing) {
  return Object.fromEntries((pricing.vendors || []).map((v) => [v.id, v.name]));
}

function endpointLabel(types) {
  const labels = {
    anthropic: 'Anthropic /v1/messages',
    gemini: 'Gemini generateContent',
    openai: 'OpenAI /v1/chat/completions',
  };
  return (types || []).map((type) => labels[type] || type).join(', ');
}

function groupRatio(pricing) {
  return Number(pricing.group_ratio?.default ?? pricing.group_ratio?.auto ?? 1);
}

function numberOrNull(value) {
  return value === null || value === undefined || value === '' ? null : Number(value);
}

function usd(value, digits = 4) {
  if (value === null || value === undefined || Number.isNaN(value)) {
    return '-';
  }
  return `$${Number(value).toFixed(digits)}`;
}

function billingSummary(item, pricing) {
  const ratio = groupRatio(pricing);
  if (item.quota_type === 1) {
    return {
      type: '按次',
      inputUsdPer1M: null,
      outputUsdPer1M: null,
      cacheReadUsdPer1M: null,
      cacheCreateUsdPer1M: null,
      perCallUsd: numberOrNull(item.model_price) * ratio,
    };
  }

  const modelRatio = numberOrNull(item.model_ratio) || 0;
  const completionRatio = numberOrNull(item.completion_ratio) || 1;
  const inputUsdPer1M = modelRatio * ratio * 2;

  return {
    type: '按Token',
    inputUsdPer1M,
    outputUsdPer1M: inputUsdPer1M * completionRatio,
    cacheReadUsdPer1M:
      item.cache_ratio === null || item.cache_ratio === undefined
        ? null
        : inputUsdPer1M * Number(item.cache_ratio),
    cacheCreateUsdPer1M:
      item.create_cache_ratio === null || item.create_cache_ratio === undefined
        ? null
        : inputUsdPer1M * Number(item.create_cache_ratio),
    perCallUsd: null,
  };
}

function toRow(item, pricing, vendors, availableModels) {
  const billing = billingSummary(item, pricing);
  const modelName = item.model_name;
  return {
    model: modelName,
    vendor: vendors[item.vendor_id] || `vendor:${item.vendor_id}`,
    quotaType: item.quota_type,
    billingType: billing.type,
    modelRatio: numberOrNull(item.model_ratio),
    completionRatio: numberOrNull(item.completion_ratio),
    cacheRatio: numberOrNull(item.cache_ratio),
    createCacheRatio: numberOrNull(item.create_cache_ratio),
    inputUsdPer1M: billing.inputUsdPer1M,
    outputUsdPer1M: billing.outputUsdPer1M,
    cacheReadUsdPer1M: billing.cacheReadUsdPer1M,
    cacheCreateUsdPer1M: billing.cacheCreateUsdPer1M,
    perCallUsd: billing.perCallUsd,
    endpoints: item.supported_endpoint_types || [],
    endpointLabel: endpointLabel(item.supported_endpoint_types),
    inModelsApi:
      availableModels === null ? null : availableModels.has(modelName),
    tags: item.tags || [],
    ownerBy: item.owner_by || '',
    description: item.description || '',
  };
}

function byVendorThenModel(a, b) {
  return (
    a.vendor.localeCompare(b.vendor) ||
    a.model.localeCompare(b.model, 'zh-Hans-CN')
  );
}

function markdownTable(rows, { showPerCall }) {
  const header = [
    '模型',
    '厂商',
    '计费',
    '输入/1M',
    '输出/1M',
    '缓存读/1M',
    '缓存写/1M',
    ...(showPerCall ? ['按次'] : []),
    'v1确认',
  ];
  const lines = [
    `| ${header.join(' | ')} |`,
    `| ${header.map(() => '---').join(' | ')} |`,
  ];

  for (const row of rows) {
    const verified =
      row.inModelsApi === null ? '未校验' : row.inModelsApi ? '是' : '否';
    const cells = [
        row.model,
        row.vendor,
        row.billingType,
        usd(row.inputUsdPer1M),
        usd(row.outputUsdPer1M),
        usd(row.cacheReadUsdPer1M),
        usd(row.cacheCreateUsdPer1M),
        ...(showPerCall ? [usd(row.perCallUsd)] : []),
        verified,
      ];
    lines.push(
      cells
        .map((cell) => String(cell).replace(/\|/g, '\\|'))
        .join(' | ')
        .replace(/^/, '| ')
        .replace(/$/, ' |'),
    );
  }
  return lines.join('\n');
}

function buildMarkdown({ pricing, modelsApi, openaiRows, anthropicRows, tokenOnly }) {
  const now = new Date().toISOString();
  const modelCount = modelsApi?.data?.length ?? null;
  const modelCheckText =
    modelCount === null
      ? '未执行。生成时使用了 `--pricing-only`，请传入 `KBQ_API_KEY` 后重新生成以确认可调用模型。'
      : `已执行，/v1/models 返回 ${modelCount} 个模型。`;

  const titleSuffix = tokenOnly ? '（仅按Token计费）' : '';
  const filterNote = tokenOnly
    ? '- 本报告已过滤掉 `quota_type=1` 的按次计费模型，只保留 `quota_type=0` 的按 Token 计费模型。\n'
    : '';
  const perCallNote = tokenOnly
    ? ''
    : '- “按次”的价格使用 `model_price × 分组倍率`。\n';

  return `# KBQ 上游 OpenAI / Anthropic 模型价格表${titleSuffix}

生成时间：${now}

数据来源：

- 模型可调用确认：\`GET /v1/models\`，${modelCheckText}
- 价格与倍率：\`GET /api/pricing\`
- pricing_version：\`${pricing.pricing_version}\`
- default 分组倍率：\`${groupRatio(pricing)}\`

说明：

- “按Token”的价格按 NewAPI 常见 quota 换算：输入价 = \`model_ratio × 分组倍率 × 2\` 美元 / 1M tokens，输出价 = 输入价 × \`completion_ratio\`。
${perCallNote}${filterNote}- 这里是上游成本口径，不等于本站最终给用户售卖价格。
- \`v1确认=是\` 表示该模型名精确出现在当前 key 的 \`/v1/models\` 返回中。

## 汇总

| 项目 | 数量 |
| --- | ---: |
| pricing 总模型 | ${(pricing.data || []).length} |
| OpenAI /v1/chat/completions | ${openaiRows.length} |
| Anthropic /v1/messages | ${anthropicRows.length} |

## OpenAI /v1/chat/completions

${markdownTable(openaiRows, { showPerCall: !tokenOnly })}

## Anthropic /v1/messages

${markdownTable(anthropicRows, { showPerCall: !tokenOnly })}
`;
}

async function writeJson(path, data) {
  await mkdir(dirname(resolve(path)), { recursive: true });
  await writeFile(path, JSON.stringify(data, null, 2));
}

async function writeMarkdown(path, data) {
  await mkdir(dirname(resolve(path)), { recursive: true });
  await writeFile(path, data);
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const pricing = await fetchJson(`${args.baseUrl}/api/pricing`, {
    timeoutMs: args.timeoutMs,
  });
  if (!pricing.success || !Array.isArray(pricing.data)) {
    throw new Error('Unexpected /api/pricing response shape');
  }

  let modelsApi = null;
  let availableModels = null;
  if (!args.pricingOnly) {
    const key = process.env.KBQ_API_KEY;
    if (!key) {
      throw new Error(
        'KBQ_API_KEY is required for /v1/models. Use --pricing-only to skip the model availability check.',
      );
    }
    modelsApi = await fetchJson(`${args.baseUrl}/v1/models`, {
      timeoutMs: args.timeoutMs,
      headers: {
        Authorization: `Bearer ${key}`,
      },
    });
    if (!Array.isArray(modelsApi.data)) {
      throw new Error('Unexpected /v1/models response shape');
    }
    availableModels = new Set(modelsApi.data.map((model) => model.id));
  }

  const vendors = vendorNameById(pricing);
  const allRows = pricing.data
    .filter((item) => !args.tokenOnly || item.quota_type === 0)
    .map((item) => toRow(item, pricing, vendors, availableModels));
  const openaiRows = allRows
    .filter((row) => row.endpoints.includes('openai'))
    .sort(byVendorThenModel);
  const anthropicRows = allRows
    .filter((row) => row.endpoints.includes('anthropic'))
    .sort(byVendorThenModel);

  const payload = {
    generatedAt: new Date().toISOString(),
    baseUrl: args.baseUrl,
    pricingVersion: pricing.pricing_version,
    groupRatio: pricing.group_ratio || {},
    modelAvailabilityChecked: modelsApi !== null,
    modelsApiCount: modelsApi?.data?.length ?? null,
    pricingModelCount: pricing.data.length,
    counts: {
      openaiChatCompletions: openaiRows.length,
      anthropicMessages: anthropicRows.length,
    },
    supportedEndpoint: pricing.supported_endpoint || {},
    openaiChatCompletions: openaiRows,
    anthropicMessages: anthropicRows,
  };

  await writeJson(args.jsonOutput, payload);
  await writeMarkdown(args.output, buildMarkdown({
    pricing,
    modelsApi,
    openaiRows,
    anthropicRows,
    tokenOnly: args.tokenOnly,
  }));

  console.log(`Wrote ${args.output}`);
  console.log(`Wrote ${args.jsonOutput}`);
  console.log(`OpenAI /v1/chat/completions: ${openaiRows.length}`);
  console.log(`Anthropic /v1/messages: ${anthropicRows.length}`);
}

main().catch((error) => {
  console.error(error.message);
  process.exit(1);
});
