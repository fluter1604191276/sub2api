const RESTRICTED_COUNTRIES = new Set(["CN"]);
const ACK_COOKIE = "fluter_region_ack=1";

const MACHINE_PATHS = {
  "api.fluterapi.top": [
    "/health",
    "/responses",
    "/v1",
  ],
  "img-api.fluterapi.top": [
    "/health",
    "/responses",
    "/v1/responses",
    "/v1/images/generations",
    "/v1/images/edits",
  ],
};

export function isRestrictedCountry(country) {
  return RESTRICTED_COUNTRIES.has((country || "").toUpperCase());
}

export function hasRegionAck(cookieHeader) {
  return String(cookieHeader || "")
    .split(";")
    .map((part) => part.trim())
    .includes(ACK_COOKIE);
}

export function isAdminLedgerPath(pathname) {
  return pathname === "/admin/upstream-rates" || pathname.startsWith("/admin/upstream-rates/");
}

function pathMatches(prefix, pathname) {
  return pathname === prefix || pathname.startsWith(`${prefix}/`);
}

export function isMachinePath(hostname, pathname) {
  const normalizedHost = hostname.toLowerCase();
  const prefixes = MACHINE_PATHS[normalizedHost] || [];
  return prefixes.some((prefix) => pathMatches(prefix, pathname));
}

export function classifyComplianceRequest({ hostname, pathname, country, cookie }) {
  const normalizedHost = hostname.toLowerCase();
  if (!isRestrictedCountry(country)) return "pass";

  if (normalizedHost === "fluterapi.top") {
    if (isAdminLedgerPath(pathname)) return "pass";
    return "main-blocked";
  }

  if (normalizedHost === "api.fluterapi.top" || normalizedHost === "img-api.fluterapi.top") {
    if (isMachinePath(normalizedHost, pathname)) return "pass";
    if (hasRegionAck(cookie)) return "pass";
    return "risk-notice";
  }

  return "pass";
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function clientSummary(request) {
  const ip = request.headers.get("CF-Connecting-IP") || "Unavailable";
  const country = request.cf?.country || request.headers.get("CF-IPCountry") || "Unknown";
  return `${escapeHtml(ip)} (${escapeHtml(country)})`;
}

function htmlResponse(html, status) {
  return new Response(html, {
    status,
    headers: {
      "content-type": "text/html; charset=utf-8",
      "cache-control": "no-store",
      "x-robots-tag": "noindex, nofollow",
    },
  });
}

function renderMainBlocked(request) {
  return `<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="robots" content="noindex,nofollow" />
    <title>当前地区暂不支持访问 | Region Not Supported</title>
    <style>
      :root {
        color-scheme: dark;
        --bg: #0b0f17;
        --panel: #1a1f29;
        --panel-strong: #202633;
        --line: #313b4e;
        --text: #f8fbff;
        --muted: #a9b7cc;
        --gold: #f5c862;
        --gold-deep: #9d731e;
        --ink-shadow: 0 2px 0 rgba(42, 90, 190, 0.7);
      }

      * { box-sizing: border-box; }

      body {
        margin: 0;
        min-height: 100vh;
        font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
        color: var(--text);
        background: radial-gradient(circle at 18% 18%, rgba(53, 68, 97, 0.28), transparent 30%), linear-gradient(180deg, #0d111a 0%, var(--bg) 100%);
      }

      .page {
        display: grid;
        min-height: 100vh;
        place-items: center;
        padding: 32px;
      }

      .card {
        width: min(560px, 100%);
        padding: 38px;
        border: 1px solid var(--line);
        border-radius: 8px;
        background: linear-gradient(180deg, var(--panel-strong), var(--panel));
        box-shadow: 0 24px 70px rgba(0, 0, 0, 0.34);
      }

      .badge {
        display: inline-flex;
        align-items: center;
        min-height: 34px;
        padding: 0 16px;
        border: 1px solid var(--gold-deep);
        border-radius: 999px;
        color: #ffd978;
        background: rgba(72, 50, 15, 0.45);
        font-size: 13px;
        font-weight: 800;
      }

      .mark {
        display: grid;
        width: 56px;
        height: 56px;
        margin-top: 28px;
        place-items: center;
        border: 1px solid var(--gold-deep);
        border-radius: 50%;
        color: var(--gold);
        background: rgba(245, 200, 98, 0.14);
        font-size: 34px;
        font-weight: 900;
      }

      h1 {
        margin: 26px 0 0;
        font-size: clamp(34px, 6vw, 44px);
        line-height: 1.1;
        letter-spacing: 0;
        text-shadow: var(--ink-shadow);
      }

      p {
        margin: 14px 0 0;
        color: var(--muted);
        font-size: 16px;
        line-height: 1.75;
      }

      .notice {
        margin-top: 24px;
        padding: 18px 16px;
        border: 1px solid var(--line);
        border-radius: 7px;
        background: rgba(8, 13, 22, 0.46);
        color: var(--text);
        font-size: 15px;
        font-weight: 780;
        line-height: 1.75;
      }

      .meta {
        margin-top: 18px;
        color: #8493aa;
        font-size: 13px;
        line-height: 1.6;
      }

      @media (max-width: 560px) {
        .page { padding: 18px; }
        .card { padding: 28px 22px; }
      }
    </style>
  </head>
  <body>
    <main class="page">
      <section class="card" aria-labelledby="title">
        <div class="badge">403 Forbidden</div>
        <div class="mark" aria-hidden="true">!</div>
        <h1 id="title">当前地区暂不支持访问</h1>
        <p>由于当地政策和法规限制，中国大陆 IP 暂不能访问本站。</p>
        <p>Due to local policy and compliance requirements, this website is not available to IP addresses from mainland China.</p>
        <div class="notice">
          如果非中国大陆 IP 被误判为中国大陆 IP，请检查您的加速器、梯子或魔法服务是否代理到中国大陆节点。
          <br />
          If you believe this is a mistake, please check whether your proxy, relay, or acceleration service exits through a mainland China node.
        </div>
        <div class="meta">当前访问 IP / Current IP: <span>${clientSummary(request)}</span></div>
      </section>
    </main>
  </body>
</html>`;
}

function renderRiskNotice(request) {
  return `<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="robots" content="noindex,nofollow" />
    <title>访问前合规风险告知 | Region Risk Notice</title>
    <style>
      :root {
        color-scheme: dark;
        --bg: #0b0f17;
        --panel: #151b24;
        --line: #2b3548;
        --line-strong: #3d4a61;
        --text: #f8fbff;
        --muted: #c4d1e4;
        --soft: #8fa1ba;
        --gold: #ffd66d;
        --gold-bg: #33230d;
        --button: #e4bc5f;
        --button-hover: #f0cf7a;
        --cancel: #263142;
        --danger-shadow: 0 2px 0 rgba(34, 88, 190, 0.72);
      }

      * { box-sizing: border-box; }

      body {
        margin: 0;
        min-height: 100vh;
        font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
        color: var(--text);
        background: radial-gradient(circle at 12% 8%, rgba(63, 75, 103, 0.22), transparent 30%), linear-gradient(180deg, #0e131c 0%, var(--bg) 100%);
      }

      .shell {
        display: grid;
        min-height: 100vh;
        place-items: center;
        padding: 26px;
      }

      .card {
        width: min(1310px, 100%);
        padding: clamp(30px, 4vw, 46px);
        border: 1px solid var(--line);
        border-radius: 22px;
        background: rgba(21, 27, 36, 0.94);
        box-shadow: 0 26px 90px rgba(0, 0, 0, 0.36);
      }

      .pill {
        display: inline-flex;
        align-items: center;
        min-height: 44px;
        padding: 0 18px;
        border-radius: 999px;
        color: var(--gold);
        background: var(--gold-bg);
        font-size: 20px;
        font-weight: 760;
      }

      h1 {
        margin: 34px 0 0;
        font-size: clamp(36px, 4vw, 52px);
        line-height: 1.08;
        letter-spacing: 0;
        text-shadow: var(--danger-shadow);
      }

      .copy {
        display: grid;
        gap: 26px;
        margin-top: 30px;
        color: var(--text);
        font-size: clamp(20px, 2vw, 26px);
        font-weight: 640;
        line-height: 1.85;
      }

      .copy p { margin: 0; }

      .english {
        color: var(--muted);
        font-size: clamp(15px, 1.45vw, 18px);
        font-weight: 540;
        line-height: 1.75;
      }

      .ipline {
        margin-top: 20px;
        color: var(--soft);
        font-size: 14px;
      }

      .label {
        margin-top: 28px;
        color: var(--muted);
        font-size: 18px;
      }

      .phrase {
        margin-top: 16px;
        padding: 22px 24px;
        border: 1px solid var(--line-strong);
        border-radius: 15px;
        color: var(--gold);
        background: #0d1520;
        font-size: clamp(18px, 2vw, 24px);
        font-weight: 820;
        line-height: 1.55;
      }

      input {
        width: 100%;
        min-height: 70px;
        margin-top: 48px;
        padding: 0 22px;
        border: 1px solid var(--line-strong);
        border-radius: 15px;
        outline: none;
        color: var(--text);
        background: #090f18;
        font-size: 22px;
      }

      input:focus {
        border-color: rgba(255, 214, 109, 0.78);
        box-shadow: 0 0 0 4px rgba(255, 214, 109, 0.12);
      }

      .actions {
        display: flex;
        flex-wrap: wrap;
        gap: 18px;
        margin-top: 42px;
      }

      button {
        min-height: 66px;
        padding: 0 26px;
        border: 0;
        border-radius: 14px;
        font-size: 22px;
        font-weight: 860;
        cursor: pointer;
      }

      .continue { color: #0b0f17; background: var(--button); }
      .continue:disabled { cursor: not-allowed; opacity: 0.45; }
      .continue:not(:disabled):hover { background: var(--button-hover); }
      .cancel { color: var(--text); background: var(--cancel); }
      .hint { min-height: 24px; margin-top: 14px; color: #ffbd7a; font-size: 14px; }

      @media (max-width: 760px) {
        .shell { padding: 14px; }
        .card { border-radius: 16px; }
        .copy { gap: 18px; }
        input, button { font-size: 17px; }
        .actions { flex-direction: column; }
        button { width: 100%; }
      }
    </style>
  </head>
  <body>
    <main class="shell">
      <section class="card" aria-labelledby="title">
        <div class="pill">Region Risk Notice</div>
        <h1 id="title">访问前合规风险告知</h1>
        <div class="copy">
          <p>为遵守各地法律法规及合规监管要求，平台即日起不面向中国大陆及其他受限地区提供 AI 中转相关服务。系统检测到你当前可能来自受限地区网络环境。</p>
          <p>若你继续访问，即表示你确认自身具备所在地法律法规允许的访问、使用和接入条件，并承诺不会将本服务用于任何违反适用法律法规、平台规则或第三方权益的场景。</p>
          <p>如你不具备上述条件，请立即取消访问。由继续访问或使用产生的相关责任，将由访问者自行承担。</p>
          <p class="english">To comply with applicable laws, regulations, and platform policies, this service is not offered to users from mainland China or other restricted regions. By continuing, you confirm that your access and use are lawful in your location and that you will not use the service in violation of applicable rules or third-party rights.</p>
        </div>
        <div class="ipline">当前访问 IP / Current IP: <span>${clientSummary(request)}</span></div>
        <div class="label">请完整输入以下确认语以继续：</div>
        <div class="phrase" id="phrase">我已知悉如上合规风险，并确认本人具备合法访问与使用条件，继续访问产生的责任由本人自行承担。</div>
        <input id="confirm-input" autocomplete="off" spellcheck="false" placeholder="请在此输入上方确认语" />
        <div class="hint" id="hint"></div>
        <div class="actions">
          <button class="continue" id="continue-button" disabled>我已知悉风险并继续访问</button>
          <button class="cancel" id="cancel-button">取消访问</button>
        </div>
      </section>
    </main>
    <script>
      const phrase = document.getElementById("phrase").textContent.trim();
      const input = document.getElementById("confirm-input");
      const continueButton = document.getElementById("continue-button");
      const cancelButton = document.getElementById("cancel-button");
      const hint = document.getElementById("hint");

      input.addEventListener("input", () => {
        const matched = input.value.trim() === phrase;
        continueButton.disabled = !matched;
        hint.textContent = matched || input.value.length === 0 ? "" : "确认语尚未完全一致。";
      });

      continueButton.addEventListener("click", () => {
        if (continueButton.disabled) return;
        document.cookie = "fluter_region_ack=1; Max-Age=86400; Path=/; Domain=.fluterapi.top; SameSite=Lax; Secure";
        const next = new URL(location.href);
        next.searchParams.set("region_ack", "1");
        location.replace(next.toString());
      });

      cancelButton.addEventListener("click", () => {
        location.href = "about:blank";
      });
    </script>
  </body>
</html>`;
}

export default {
  async fetch(request) {
    const url = new URL(request.url);
    const action = classifyComplianceRequest({
      hostname: url.hostname,
      pathname: url.pathname,
      country: request.cf?.country || request.headers.get("CF-IPCountry"),
      cookie: request.headers.get("cookie"),
    });

    if (action === "main-blocked") return htmlResponse(renderMainBlocked(request), 403);
    if (action === "risk-notice") return htmlResponse(renderRiskNotice(request), 403);

    return fetch(request);
  },
};
