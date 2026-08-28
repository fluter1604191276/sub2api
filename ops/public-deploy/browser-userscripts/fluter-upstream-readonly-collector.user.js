// ==UserScript==
// @name         Fluter Upstream Read-Only Collector
// @namespace    https://fluterapi.top/
// @version      0.1.16
// @description  Read sanitized upstream balance/rate snippets from whitelisted pages and send them to the local Fluter collector.
// @updateURL    http://127.0.0.1:8799/userscript/fluter-upstream-readonly-collector.user.js
// @downloadURL  http://127.0.0.1:8799/userscript/fluter-upstream-readonly-collector.user.js
// @match        https://api.saki.lat/*
// @match        https://saki.lat/*
// @match        https://pool.gptstore.club/*
// @match        https://gptstore.club/*
// @match        https://api.tokenskingdom.com/*
// @match        https://image.tokenskingdom.com/*
// @match        https://tokenskingdom.com/*
// @match        https://api.mouubox.com/*
// @match        https://sub2api.mouubox.com/*
// @match        https://sub2.congmingai.com/*
// @match        https://mdkj.lol/*
// @match        https://xn--vduyey89e.com/*
// @match        https://vip.lcodex.cn/*
// @match        https://lcodex.cn/*
// @grant        GM_getValue
// @grant        GM_setValue
// @grant        GM_registerMenuCommand
// @grant        GM_xmlhttpRequest
// @connect      127.0.0.1
// @run-at       document-idle
// ==/UserScript==

(function () {
  "use strict";

  const SCRIPT_VERSION = "0.1.16";
  const DEFAULT_COLLECTOR_URL = "http://127.0.0.1:8799/ingest";
  const TOKEN_KEY = "fluterCollectorToken";
  const URL_KEY = "fluterCollectorUrl";
  const AUTO_SEND_KEY_PREFIX = "fluterLastSent:";
  const AUTO_SEND_ENABLED_KEY_PREFIX = "fluterAutoSendEnabled:";
  const AUTO_SEND_INTERVAL_MIN_KEY_PREFIX = "fluterAutoSendIntervalMinutes:";
  const AUTO_REFRESH_ENABLED_KEY_PREFIX = "fluterAutoRefreshEnabled:";
  const AUTO_REFRESH_INTERVAL_MIN_KEY_PREFIX = "fluterAutoRefreshIntervalMinutes:";
  const AUTO_REFRESH_LAST_KEY_PREFIX = "fluterAutoRefreshLast:";
  const PANEL_COLLAPSED_KEY_PREFIX = "fluterCollectorPanelCollapsed:";
  const REFRESH_ONCE_KEY_PREFIX = "fluterRefreshOnce:";
  const AFTER_RELOAD_SEND_KEY_PREFIX = "fluterAfterReloadSend:";
  const AFTER_RELOAD_COMMAND_KEY_PREFIX = "fluterAfterReloadCommand:";
  const COMMAND_REFRESH_LAST_KEY_PREFIX = "fluterCommandRefreshLast:";
  const DEFAULT_AUTO_SEND_INTERVAL_MINUTES = 5;
  const DEFAULT_AUTO_REFRESH_INTERVAL_MINUTES = 30;
  const MIN_AUTO_SEND_INTERVAL_MINUTES = 1;
  const MIN_AUTO_REFRESH_INTERVAL_MINUTES = 5;
  const AUTO_TICK_INTERVAL_MS = 30 * 1000;
  const COMMAND_POLL_INTERVAL_MS = 45 * 1000;
  const AFTER_RELOAD_SEND_MAX_AGE_MS = 2 * 60 * 1000;
  const COMMAND_REFRESH_COOLDOWN_MS = 2 * 60 * 1000;
  const MAX_TEXT_CHARS = 16000;
  const MAX_RATE_LINES = 16;
  const MAX_ACCOUNT_ROWS = 28;
  const READY_WAIT_TIMEOUT_MS = 12000;
  const READY_WAIT_INTERVAL_MS = 650;
  const READY_STABLE_REQUIRED_SAMPLES = 3;

  let lastSendResult = "";
  let lastCollectorCheck = "";
  let lastCollectorHealthCheckAt = 0;
  let panelElements = null;

  const PROVIDERS = [
    {
      provider: "Meow",
      site: "api.saki.lat",
      aliases: ["api.saki.lat", "saki.lat"],
    },
    {
      provider: "Magic",
      site: "pool.gptstore.club",
      aliases: ["pool.gptstore.club", "gptstore.club"],
    },
    {
      provider: "Kingdom",
      site: "api.tokenskingdom.com",
      aliases: ["api.tokenskingdom.com", "image.tokenskingdom.com", "tokenskingdom.com"],
    },
    {
      provider: "超超 Mouubox",
      site: "api.mouubox.com",
      aliases: ["api.mouubox.com"],
    },
    {
      provider: "超超 Mouubox 副站",
      site: "sub2api.mouubox.com",
      aliases: ["sub2api.mouubox.com"],
    },
    {
      provider: "聪明AI",
      site: "sub2.congmingai.com",
      aliases: ["sub2.congmingai.com"],
    },
    {
      provider: "乔燃",
      site: "mdkj.lol",
      aliases: ["mdkj.lol"],
    },
    {
      provider: "KBQ",
      site: "xn--vduyey89e.com",
      aliases: ["xn--vduyey89e.com"],
    },
    {
      provider: "钧澈",
      site: "vip.lcodex.cn",
      aliases: ["vip.lcodex.cn", "lcodex.cn"],
    },
  ];

  const provider = PROVIDERS.find((candidate) => candidate.aliases.some((alias) => hostMatches(location.hostname, alias)));
  if (!provider) {
    return;
  }

  GM_registerMenuCommand("设置 Fluter collector token", () => {
    const current = GM_getValue(TOKEN_KEY, "");
    const next = prompt("粘贴本机 collector token。不要发到聊天里。", current);
    if (next && next.trim()) {
      GM_setValue(TOKEN_KEY, next.trim());
      alert("已保存本机 collector token。");
    }
  });

  GM_registerMenuCommand("设置 Fluter collector 地址", () => {
    const current = GM_getValue(URL_KEY, DEFAULT_COLLECTOR_URL);
    const next = prompt("本机 collector 地址", current);
    if (next && next.trim()) {
      GM_setValue(URL_KEY, next.trim());
      alert("已保存 collector 地址。");
    }
  });

  GM_registerMenuCommand("发送当前页只读快照", () => {
    sendSnapshot({ force: true, waitForStablePage: true });
  });

  GM_registerMenuCommand("刷新当前页后发送快照", () => {
    refreshCurrentPageThenSend();
  });

  GM_registerMenuCommand("本页下次打开自动刷新一次再抓取", () => {
    GM_setValue(refreshOnceKey(), Date.now());
    alert("已开启：本页下次打开时会自动刷新一次，然后发送只读快照。");
  });

  GM_registerMenuCommand("取消本页下次自动刷新", () => {
    clearStoredValue(refreshOnceKey());
    clearStoredValue(afterReloadSendKey());
    alert("已取消本页的下次自动刷新设置。");
  });

  GM_registerMenuCommand("打开/刷新 Fluter 采集悬浮窗", () => {
    buildFloatingPanel();
    updatePanelStatus();
  });

  const startupAction = handleStartupRefreshActions();
  if (startupAction === "reloading") {
    return;
  }

  buildFloatingPanel();

  window.setTimeout(() => {
    const pendingCommandId = storedCommandId(afterReloadCommandKey());
    if (startupAction === "afterReloadSend") {
      clearStoredValue(afterReloadCommandKey());
    }
    sendSnapshot({
      force: startupAction === "afterReloadSend",
      waitForStablePage: startupAction === "afterReloadSend",
      onSuccess: pendingCommandId
        ? () => acknowledgeCommand(pendingCommandId, "done", "snapshot sent after command-triggered reload")
        : null,
      onFailure: pendingCommandId
        ? (detail) => acknowledgeCommand(pendingCommandId, "error", detail || "snapshot failed after command-triggered reload")
        : null,
    });
  }, startupAction === "afterReloadSend" ? 2200 : 1500);

  window.setTimeout(() => autoCollectionTick(), 9000);
  window.setInterval(() => autoCollectionTick(), AUTO_TICK_INTERVAL_MS);
  window.setTimeout(() => pollCommands(), 7000);
  window.setInterval(() => pollCommands(), COMMAND_POLL_INTERVAL_MS);
  window.setInterval(() => updatePanelStatus(), 10000);

  function refreshCurrentPageThenSend() {
    GM_setValue(afterReloadSendKey(), Date.now());
    console.info("[Fluter collector] reloading current page before sending snapshot.");
    location.reload();
  }

  function handleStartupRefreshActions() {
    const now = Date.now();
    const pendingAfterReloadAt = Number(GM_getValue(afterReloadSendKey(), 0) || 0);
    if (pendingAfterReloadAt) {
      clearStoredValue(afterReloadSendKey());
      if (now - pendingAfterReloadAt <= AFTER_RELOAD_SEND_MAX_AGE_MS) {
        console.info("[Fluter collector] sending one forced snapshot after page reload.");
        return "afterReloadSend";
      }
      clearStoredValue(afterReloadCommandKey());
      console.info("[Fluter collector] ignored stale after-reload marker.");
    }

    const pendingRefreshOnceAt = Number(GM_getValue(refreshOnceKey(), 0) || 0);
    if (pendingRefreshOnceAt) {
      clearStoredValue(refreshOnceKey());
      GM_setValue(afterReloadSendKey(), now);
      console.info("[Fluter collector] one-shot refresh marker found; reloading current page once.");
      location.reload();
      return "reloading";
    }

    return "normal";
  }

  function refreshOnceKey() {
    return REFRESH_ONCE_KEY_PREFIX + location.hostname + location.pathname;
  }

  function afterReloadSendKey() {
    return AFTER_RELOAD_SEND_KEY_PREFIX + location.hostname + location.pathname;
  }

  function afterReloadCommandKey() {
    return AFTER_RELOAD_COMMAND_KEY_PREFIX + location.hostname + location.pathname;
  }

  function commandRefreshLastKey() {
    return COMMAND_REFRESH_LAST_KEY_PREFIX + provider.site;
  }

  function autoSendEnabledKey() {
    return AUTO_SEND_ENABLED_KEY_PREFIX + provider.site;
  }

  function autoSendIntervalKey() {
    return AUTO_SEND_INTERVAL_MIN_KEY_PREFIX + provider.site;
  }

  function autoRefreshEnabledKey() {
    return AUTO_REFRESH_ENABLED_KEY_PREFIX + provider.site;
  }

  function autoRefreshIntervalKey() {
    return AUTO_REFRESH_INTERVAL_MIN_KEY_PREFIX + provider.site;
  }

  function autoRefreshLastKey() {
    return AUTO_REFRESH_LAST_KEY_PREFIX + provider.site;
  }

  function panelCollapsedKey() {
    return PANEL_COLLAPSED_KEY_PREFIX + provider.site;
  }

  function clearStoredValue(key) {
    GM_setValue(key, 0);
  }

  function storedCommandId(key) {
    const value = String(GM_getValue(key, "") || "").trim();
    return value === "0" ? "" : value;
  }

  function collectorUrl() {
    return String(GM_getValue(URL_KEY, DEFAULT_COLLECTOR_URL) || DEFAULT_COLLECTOR_URL).trim();
  }

  function collectorBaseUrl() {
    return collectorUrl().replace(/\/ingest\/?$/i, "").replace(/\/$/, "");
  }

  function numberSetting(key, defaultValue, minValue) {
    const value = Number(GM_getValue(key, defaultValue) || defaultValue);
    if (!Number.isFinite(value)) {
      return defaultValue;
    }
    return Math.max(minValue, value);
  }

  function setNumberSetting(key, value, minValue) {
    const parsed = Number(value);
    const safeValue = Number.isFinite(parsed) ? Math.max(minValue, parsed) : minValue;
    GM_setValue(key, safeValue);
    return safeValue;
  }

  function autoSendEnabled() {
    return GM_getValue(autoSendEnabledKey(), true) !== false;
  }

  function autoSendIntervalMinutes() {
    return numberSetting(autoSendIntervalKey(), DEFAULT_AUTO_SEND_INTERVAL_MINUTES, MIN_AUTO_SEND_INTERVAL_MINUTES);
  }

  function autoSendIntervalMs() {
    return autoSendIntervalMinutes() * 60 * 1000;
  }

  function autoRefreshEnabled() {
    return GM_getValue(autoRefreshEnabledKey(), false) === true;
  }

  function autoRefreshIntervalMinutes() {
    return numberSetting(autoRefreshIntervalKey(), DEFAULT_AUTO_REFRESH_INTERVAL_MINUTES, MIN_AUTO_REFRESH_INTERVAL_MINUTES);
  }

  function autoRefreshIntervalMs() {
    return autoRefreshIntervalMinutes() * 60 * 1000;
  }

  function lastSentAt() {
    return Number(GM_getValue(AUTO_SEND_KEY_PREFIX + location.hostname + location.pathname, 0) || 0);
  }

  function autoCollectionTick() {
    const token = String(GM_getValue(TOKEN_KEY, "") || "").trim();
    if (!token) {
      lastCollectorCheck = "未配置 token";
      updatePanelStatus();
      return;
    }
    const now = Date.now();
    if (autoRefreshEnabled()) {
      const lastRefresh = Number(GM_getValue(autoRefreshLastKey(), 0) || 0);
      if (!lastRefresh || now - lastRefresh >= autoRefreshIntervalMs()) {
        GM_setValue(autoRefreshLastKey(), now);
        GM_setValue(afterReloadSendKey(), now);
        lastSendResult = "自动刷新中...";
        updatePanelStatus();
        console.info("[Fluter collector] auto refresh interval reached; reloading before snapshot.");
        location.reload();
        return;
      }
    }
    if (autoSendEnabled()) {
      sendSnapshot({ force: false, waitForStablePage: true });
    }
    updatePanelStatus();
  }

  function checkCollectorHealth({ force } = {}) {
    const now = Date.now();
    if (!force && now - lastCollectorHealthCheckAt < 60 * 1000) {
      return;
    }
    lastCollectorHealthCheckAt = now;
    GM_xmlhttpRequest({
      method: "GET",
      url: collectorBaseUrl() + "/health",
      timeout: 5000,
      onload(response) {
        lastCollectorCheck = response.status === 200 ? "collector 可用" : "collector HTTP " + response.status;
        updatePanelStatus();
      },
      onerror() {
        lastCollectorCheck = "collector 未连接";
        updatePanelStatus();
      },
      ontimeout() {
        lastCollectorCheck = "collector 超时";
        updatePanelStatus();
      },
    });
  }

  function buildFloatingPanel() {
    if (panelElements && panelElements.root && document.body.contains(panelElements.root)) {
      return;
    }
    const old = document.getElementById("fluter-upstream-collector-panel");
    if (old) {
      old.remove();
    }

    const root = document.createElement("div");
    root.id = "fluter-upstream-collector-panel";
    root.style.cssText = [
      "position:fixed",
      "right:16px",
      "bottom:16px",
      "z-index:2147483647",
      "width:320px",
      "max-width:calc(100vw - 32px)",
      "font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif",
      "font-size:12px",
      "line-height:1.45",
      "color:#172033",
      "background:#ffffff",
      "border:1px solid rgba(23,32,51,.16)",
      "box-shadow:0 12px 34px rgba(18,24,40,.18)",
      "border-radius:10px",
      "overflow:hidden",
    ].join(";");

    root.innerHTML = `
      <div data-role="header" style="display:flex;align-items:center;justify-content:space-between;gap:8px;padding:10px 12px;background:#f6f8fb;border-bottom:1px solid rgba(23,32,51,.1);">
        <strong style="font-size:13px;">Fluter 上游采集</strong>
        <button data-role="toggle" type="button" style="border:0;background:#e7ebf2;border-radius:6px;padding:3px 7px;cursor:pointer;color:#172033;">收起</button>
      </div>
      <div data-role="body" style="padding:10px 12px;display:grid;gap:9px;">
        <div data-role="status" style="display:grid;gap:3px;color:#435066;"></div>
        <label style="display:flex;align-items:center;gap:8px;">
          <input data-role="auto-send" type="checkbox" />
          <span>自动发送快照</span>
        </label>
        <label style="display:grid;grid-template-columns:1fr 90px;align-items:center;gap:8px;">
          <span>发送间隔（分钟）</span>
          <input data-role="send-interval" type="number" min="${MIN_AUTO_SEND_INTERVAL_MINUTES}" step="1" style="width:100%;box-sizing:border-box;" />
        </label>
        <label style="display:flex;align-items:center;gap:8px;">
          <input data-role="auto-refresh" type="checkbox" />
          <span>定时刷新页面后抓取</span>
        </label>
        <label style="display:grid;grid-template-columns:1fr 90px;align-items:center;gap:8px;">
          <span>刷新间隔（分钟）</span>
          <input data-role="refresh-interval" type="number" min="${MIN_AUTO_REFRESH_INTERVAL_MINUTES}" step="1" style="width:100%;box-sizing:border-box;" />
        </label>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:8px;">
          <button data-role="send-now" type="button" style="border:1px solid rgba(23,32,51,.16);background:#172033;color:#fff;border-radius:7px;padding:7px 8px;cursor:pointer;">立即发送</button>
          <button data-role="refresh-send" type="button" style="border:1px solid rgba(23,32,51,.16);background:#fff;color:#172033;border-radius:7px;padding:7px 8px;cursor:pointer;">刷新后发送</button>
        </div>
        <button data-role="configure" type="button" style="border:1px solid rgba(23,32,51,.16);background:#f6f8fb;color:#172033;border-radius:7px;padding:7px 8px;cursor:pointer;">设置 token / 地址</button>
        <button data-role="health" type="button" style="border:1px solid rgba(23,32,51,.16);background:#fff;color:#172033;border-radius:7px;padding:7px 8px;cursor:pointer;">检测 collector</button>
      </div>
    `;

    document.body.appendChild(root);
    panelElements = {
      root,
      body: root.querySelector('[data-role="body"]'),
      toggle: root.querySelector('[data-role="toggle"]'),
      status: root.querySelector('[data-role="status"]'),
      autoSend: root.querySelector('[data-role="auto-send"]'),
      sendInterval: root.querySelector('[data-role="send-interval"]'),
      autoRefresh: root.querySelector('[data-role="auto-refresh"]'),
      refreshInterval: root.querySelector('[data-role="refresh-interval"]'),
      sendNow: root.querySelector('[data-role="send-now"]'),
      refreshSend: root.querySelector('[data-role="refresh-send"]'),
      configure: root.querySelector('[data-role="configure"]'),
      health: root.querySelector('[data-role="health"]'),
    };

    panelElements.toggle.addEventListener("click", () => {
      const collapsed = !isPanelCollapsed();
      GM_setValue(panelCollapsedKey(), collapsed);
      updatePanelStatus();
    });
    panelElements.autoSend.addEventListener("change", () => {
      GM_setValue(autoSendEnabledKey(), Boolean(panelElements.autoSend.checked));
      updatePanelStatus();
    });
    panelElements.sendInterval.addEventListener("change", () => {
      setNumberSetting(autoSendIntervalKey(), panelElements.sendInterval.value, MIN_AUTO_SEND_INTERVAL_MINUTES);
      updatePanelStatus();
    });
    panelElements.autoRefresh.addEventListener("change", () => {
      GM_setValue(autoRefreshEnabledKey(), Boolean(panelElements.autoRefresh.checked));
      updatePanelStatus();
    });
    panelElements.refreshInterval.addEventListener("change", () => {
      setNumberSetting(autoRefreshIntervalKey(), panelElements.refreshInterval.value, MIN_AUTO_REFRESH_INTERVAL_MINUTES);
      updatePanelStatus();
    });
    panelElements.sendNow.addEventListener("click", () => sendSnapshot({ force: true, waitForStablePage: true }));
    panelElements.refreshSend.addEventListener("click", () => refreshCurrentPageThenSend());
    panelElements.configure.addEventListener("click", () => configureCollectorFromPanel());
    panelElements.health.addEventListener("click", () => checkCollectorHealth({ force: true }));
    updatePanelStatus();
    checkCollectorHealth({ force: true });
  }

  function isPanelCollapsed() {
    return GM_getValue(panelCollapsedKey(), false) === true;
  }

  function configureCollectorFromPanel() {
    const currentToken = GM_getValue(TOKEN_KEY, "");
    const nextToken = prompt("粘贴本机 collector token。不要发到聊天里。", currentToken);
    if (nextToken && nextToken.trim()) {
      GM_setValue(TOKEN_KEY, nextToken.trim());
    }
    const currentUrl = GM_getValue(URL_KEY, DEFAULT_COLLECTOR_URL);
    const nextUrl = prompt("本机 collector 地址", currentUrl);
    if (nextUrl && nextUrl.trim()) {
      GM_setValue(URL_KEY, nextUrl.trim());
    }
    updatePanelStatus();
  }

  function formatRelativeTime(timestamp) {
    const value = Number(timestamp || 0);
    if (!value) {
      return "从未";
    }
    const seconds = Math.max(0, Math.round((Date.now() - value) / 1000));
    if (seconds < 60) {
      return seconds + " 秒前";
    }
    const minutes = Math.round(seconds / 60);
    if (minutes < 60) {
      return minutes + " 分钟前";
    }
    return Math.round(minutes / 60) + " 小时前";
  }

  function updatePanelStatus(extraStatus) {
    if (!panelElements || !panelElements.root) {
      return;
    }
    const collapsed = isPanelCollapsed();
    panelElements.body.style.display = collapsed ? "none" : "grid";
    panelElements.toggle.textContent = collapsed ? "展开" : "收起";
    panelElements.autoSend.checked = autoSendEnabled();
    panelElements.sendInterval.value = String(autoSendIntervalMinutes());
    panelElements.autoRefresh.checked = autoRefreshEnabled();
    panelElements.refreshInterval.value = String(autoRefreshIntervalMinutes());

    const tokenConfigured = String(GM_getValue(TOKEN_KEY, "") || "").trim() ? "已配置" : "未配置";
    const observed = buildObservation();
    const statusLines = [
      `<div><strong>${escapeHtml(provider.provider)}</strong> <span style="color:#748096;">${escapeHtml(provider.site)}</span></div>`,
      `<div>脚本：${SCRIPT_VERSION}；token：${tokenConfigured}；collector：${escapeHtml(lastCollectorCheck || "待检测")}</div>`,
      `<div>上次发送：${formatRelativeTime(lastSentAt())}</div>`,
      `<div>本页识别：余额 ${observed.detected_balance ? "有" : "无"}，账号行 ${observed.detected_accounts.length}，倍率行 ${observed.detected_rates.length}</div>`,
      `<div>最近结果：${escapeHtml(extraStatus || lastSendResult || "等待发送")}</div>`,
    ];
    panelElements.status.innerHTML = statusLines.join("");
  }

  function escapeHtml(value) {
    return String(value || "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function sendSnapshot({ force, waitForStablePage, onSuccess, onFailure } = {}) {
    const token = String(GM_getValue(TOKEN_KEY, "") || "").trim();
    if (!token) {
      console.info("[Fluter collector] token is not configured; use the Tampermonkey menu to set it.");
      if (onFailure) {
        onFailure("collector token is not configured");
      }
      return;
    }

    const now = Date.now();
    const lastSentKey = AUTO_SEND_KEY_PREFIX + location.hostname + location.pathname;
    const lastSent = Number(GM_getValue(lastSentKey, 0) || 0);
    if (!force && now - lastSent < autoSendIntervalMs()) {
      if (onSuccess) {
        onSuccess();
      }
      return;
    }

    const send = (observation) => {
      const payload = [observation || buildObservation()];
      postSnapshot(payload, token, now, lastSentKey, onSuccess, onFailure);
    };
    if (waitForStablePage) {
      waitForStableObservation(send);
      return;
    }
    send();
  }

  function waitForStableObservation(onReady) {
    const startedAt = Date.now();
    let lastSignature = "";
    let stableSamples = 0;
    let bestObservation = null;
    let bestQuality = null;
    lastSendResult = "等待页面渲染稳定...";
    updatePanelStatus();

    const sample = () => {
      const observation = buildObservation();
      const quality = observationQuality(observation);
      if (!bestQuality || quality.score >= bestQuality.score) {
        bestObservation = observation;
        bestQuality = quality;
      }

      if (quality.signature && quality.signature === lastSignature) {
        stableSamples += 1;
      } else {
        lastSignature = quality.signature;
        stableSamples = 1;
      }

      const elapsedMs = Date.now() - startedAt;
      const readyState = document.readyState || "unknown";
      const isStable =
        readyState !== "loading" &&
        quality.hasUsefulContent &&
        stableSamples >= READY_STABLE_REQUIRED_SAMPLES;
      const timedOut = elapsedMs >= READY_WAIT_TIMEOUT_MS;

      if (isStable || timedOut) {
        const finalObservation = isStable ? observation : bestObservation || observation;
        const finalQuality = isStable ? quality : bestQuality || quality;
        finalObservation.detail = buildSnapshotDetail(finalObservation, {
          waitState: isStable ? "stable" : "timeout",
          stableSamples,
          elapsedMs,
          readyState,
          bestAccountLines: finalQuality.accountCount,
          bestRateLines: finalQuality.rateCount,
        });
        lastSendResult = isStable
          ? `页面稳定，发送账号行 ${finalQuality.accountCount}`
          : `等待超时，发送最佳快照：账号行 ${finalQuality.accountCount}`;
        updatePanelStatus();
        onReady(finalObservation);
        return;
      }

      lastSendResult = `等待页面稳定：账号行 ${quality.accountCount}，样本 ${stableSamples}/${READY_STABLE_REQUIRED_SAMPLES}`;
      updatePanelStatus();
      window.setTimeout(sample, READY_WAIT_INTERVAL_MS);
    };

    sample();
  }

  function observationQuality(observation) {
    const accounts = Array.isArray(observation.detected_accounts) ? observation.detected_accounts : [];
    const rates = Array.isArray(observation.detected_rates) ? observation.detected_rates : [];
    const balance = String(observation.detected_balance || "").trim();
    const text = String(observation.text || "").trim();
    const accountSignature = accounts
      .map((row) => [row.account_name, row.upstream_group, row.page_rate].join(":"))
      .join("|");
    const rateSignature = rates.map((row) => String(row.source_line || row)).join("|");
    return {
      accountCount: accounts.length,
      rateCount: rates.length,
      hasUsefulContent: Boolean(accounts.length || rates.length || balance || text),
      score: accounts.length * 8 + rates.length * 4 + (balance ? 2 : 0) + Math.min(text.length, 2000) / 2000,
      signature: [accounts.length, rates.length, balance, accountSignature, rateSignature].join("||"),
    };
  }

  function buildSnapshotDetail(observation, readiness) {
    const accounts = Array.isArray(observation.detected_accounts) ? observation.detected_accounts : [];
    const rates = Array.isArray(observation.detected_rates) ? observation.detected_rates : [];
    const parts = [
      "Chrome Tampermonkey read-only snapshot",
      `balance=${observation.detected_balance ? "yes" : "no"}`,
      `account_lines=${accounts.length}`,
      `rate_lines=${rates.length}`,
      `script=${SCRIPT_VERSION}`,
    ];
    if (readiness) {
      parts.push(`wait_state=${readiness.waitState}`);
      parts.push(`ready_state=${readiness.readyState || "unknown"}`);
      parts.push(`stable_samples=${readiness.stableSamples || 0}`);
      parts.push(`wait_ms=${readiness.elapsedMs || 0}`);
      parts.push(`best_account_lines=${readiness.bestAccountLines || accounts.length}`);
      parts.push(`best_rate_lines=${readiness.bestRateLines || rates.length}`);
    }
    return parts.join("; ");
  }

  function postSnapshot(payload, token, sentAt, lastSentKey, onSuccess, onFailure) {
    GM_xmlhttpRequest({
      method: "POST",
      url: collectorUrl(),
      headers: {
        "Content-Type": "application/json",
        "X-Collector-Token": token,
        "X-Collector-Source": location.hostname,
      },
      data: JSON.stringify(payload),
      timeout: 10000,
      onload(response) {
        if (response.status >= 200 && response.status < 300) {
          GM_setValue(lastSentKey, sentAt);
          lastSendResult = "发送成功 " + new Date().toLocaleTimeString();
          lastCollectorCheck = "collector 可用";
          console.info("[Fluter collector] read-only snapshot sent:", response.status);
          updatePanelStatus();
          if (onSuccess) {
            onSuccess();
          }
        } else {
          lastSendResult = "发送失败 HTTP " + response.status;
          lastCollectorCheck = "collector 拒绝请求";
          console.warn("[Fluter collector] collector rejected snapshot:", response.status, response.responseText);
          updatePanelStatus();
          if (onFailure) {
            onFailure("collector rejected snapshot: " + response.status);
          }
        }
      },
      onerror(error) {
        lastSendResult = "发送失败：无法连接 collector";
        lastCollectorCheck = "collector 未连接";
        console.warn("[Fluter collector] request failed:", error);
        updatePanelStatus();
        if (onFailure) {
          onFailure("snapshot request failed");
        }
      },
      ontimeout() {
        lastSendResult = "发送失败：collector 超时";
        lastCollectorCheck = "collector 超时";
        console.warn("[Fluter collector] request timed out.");
        updatePanelStatus();
        if (onFailure) {
          onFailure("snapshot request timed out");
        }
      },
    });
  }

  function pollCommands() {
    const token = String(GM_getValue(TOKEN_KEY, "") || "").trim();
    if (!token) {
      return;
    }
    GM_xmlhttpRequest({
      method: "GET",
      url: collectorBaseUrl() + "/commands?site=" + encodeURIComponent(provider.site),
      headers: {
        "X-Collector-Token": token,
        "X-Collector-Source": location.hostname,
      },
      timeout: 10000,
      onload(response) {
        if (response.status !== 200) {
          console.warn("[Fluter collector] command poll rejected:", response.status, response.responseText);
          return;
        }
        let payload;
        try {
          payload = JSON.parse(response.responseText || "{}");
        } catch (error) {
          console.warn("[Fluter collector] command poll returned invalid JSON.");
          return;
        }
        const commands = Array.isArray(payload.commands) ? payload.commands : [];
        if (!commands.length) {
          return;
        }
        executeCommand(commands[0]);
      },
      onerror(error) {
        console.warn("[Fluter collector] command poll failed:", error);
      },
      ontimeout() {
        console.warn("[Fluter collector] command poll timed out.");
      },
    });
  }

  function executeCommand(command) {
    const commandId = String(command && command.id ? command.id : "").trim();
    const action = String(command && command.action ? command.action : "").trim();
    if (!commandId) {
      return;
    }
    if (action === "send_snapshot") {
      sendSnapshot({
        force: true,
        waitForStablePage: true,
        onSuccess: () => acknowledgeCommand(commandId, "done", "snapshot sent without reload"),
        onFailure: (detail) => acknowledgeCommand(commandId, "error", detail || "snapshot send failed"),
      });
      return;
    }
    if (action !== "refresh_then_send") {
      acknowledgeCommand(commandId, "error", "unsupported command action: " + action);
      return;
    }

    const now = Date.now();
    const lastRefresh = Number(GM_getValue(commandRefreshLastKey(), 0) || 0);
    if (lastRefresh && now - lastRefresh < COMMAND_REFRESH_COOLDOWN_MS) {
      console.info("[Fluter collector] command refresh is cooling down; sending forced snapshot instead.");
      sendSnapshot({
        force: true,
        waitForStablePage: true,
        onSuccess: () => acknowledgeCommand(commandId, "done", "cooldown active; sent snapshot without reload"),
        onFailure: (detail) => acknowledgeCommand(commandId, "error", detail || "cooldown snapshot failed"),
      });
      return;
    }
    GM_setValue(commandRefreshLastKey(), now);
    GM_setValue(afterReloadSendKey(), now);
    GM_setValue(afterReloadCommandKey(), commandId);
    console.info("[Fluter collector] command received; reloading current page before sending snapshot.", commandId);
    location.reload();
  }

  function acknowledgeCommand(commandId, status, detail) {
    const token = String(GM_getValue(TOKEN_KEY, "") || "").trim();
    if (!token || !commandId) {
      return;
    }
    GM_xmlhttpRequest({
      method: "POST",
      url: collectorBaseUrl() + "/command-ack",
      headers: {
        "Content-Type": "application/json",
        "X-Collector-Token": token,
        "X-Collector-Source": location.hostname,
      },
      data: JSON.stringify({
        id: commandId,
        status: status || "done",
        detail: String(detail || "").slice(0, 500),
      }),
      timeout: 10000,
      onload(response) {
        if (response.status >= 200 && response.status < 300) {
          console.info("[Fluter collector] command acknowledged:", commandId, status || "done");
        } else {
          console.warn("[Fluter collector] command ack rejected:", response.status, response.responseText);
        }
      },
      onerror(error) {
        console.warn("[Fluter collector] command ack failed:", error);
      },
      ontimeout() {
        console.warn("[Fluter collector] command ack timed out.");
      },
    });
  }

  function buildObservation() {
    const rawText = pageTextWithoutCollectorPanel();
    const sanitizedText = sanitizeClient(rawText).slice(0, MAX_TEXT_CHARS);
    const pricingLike = isPricingLike(location.href, sanitizedText);
    return {
      provider: provider.provider,
      site: provider.site,
      browser: "chrome",
      url: location.href,
      title: document.title || "",
      script_version: SCRIPT_VERSION,
      text: compactExcerpt(sanitizedText, 4000),
      detected_balance: detectBalance(sanitizedText, location.href),
      detected_accounts: pricingLike ? [] : detectAccountObjects(sanitizedText),
      detected_rates: detectRateObjects(sanitizedText),
      observed_at: new Date().toISOString(),
    };
  }

  function pageTextWithoutCollectorPanel() {
    if (!document.body) {
      return "";
    }
    const clone = document.body.cloneNode(true);
    const panel = clone.querySelector("#fluter-upstream-collector-panel");
    if (panel) {
      panel.remove();
    }
    return String(clone.innerText || clone.textContent || "");
  }

  function hostMatches(hostname, alias) {
    const host = String(hostname || "").toLowerCase();
    const normalizedAlias = String(alias || "").toLowerCase();
    return host === normalizedAlias || host.endsWith("." + normalizedAlias);
  }

  function sanitizeClient(value) {
    return String(value || "")
      .replace(/\u0000/g, "")
      .replace(/(sk-[A-Za-z0-9_-]{8})[A-Za-z0-9_-]{12,}/g, "$1...redacted")
      .replace(/(bearer\s+)[A-Za-z0-9._-]{16,}/gi, "$1...redacted")
      .replace(/(api[_ -]?key\s*[:=]\s*)[A-Za-z0-9._-]{12,}/gi, "$1...redacted")
      .replace(/[A-Za-z0-9_-]{48,}/g, "...redacted-long-token...");
  }

  function sourceLines(text) {
    return String(text || "")
      .split(/\r?\n/)
      .map((line) => line.trim().replace(/\s+/g, " "))
      .filter(Boolean);
  }

  function detectBalance(text, pageUrl) {
    if (isPricingLike(pageUrl, text)) {
      return "";
    }
    if (isTokenQuotaContext(text)) {
      return "";
    }
    const lines = sourceLines(text);
    const keywords = ["当前余额", "账户余额", "账号余额", "可用余额", "剩余余额", "余额", "剩余额度", "可用额度", "balance", "remaining", "quota"];
    const logLike = /\/(?:log|logs|usage|record|records|task|tasks|bill)\b/i.test(pageUrl || "");
    const balanceMatch = String(text || "").match(/(当前余额|账户余额|账号余额|可用余额|剩余余额|余额|剩余额度|可用额度|balance|remaining|quota)[^\n$¥￥0-9-]{0,40}([$¥￥]?\s*-?\d+(?:\.\d+)?)/i);
    if (balanceMatch && !logLike) {
      return `${balanceMatch[1]} ${balanceMatch[2].replace(/\s+/g, " ")}`.slice(0, 120);
    }
    for (let idx = 0; idx < lines.length; idx += 1) {
      const line = lines[idx];
      const lower = line.toLowerCase();
      if (!keywords.some((keyword) => line.includes(keyword) || lower.includes(keyword))) {
        continue;
      }
      const windowLines = lines.slice(idx, idx + 4);
      for (const candidate of windowLines) {
        if (candidate.length > 180 && !/[$¥￥]\s*-?\d+(?:\.\d+)?/.test(candidate)) {
          continue;
        }
        if (/\b\d+(?:\.\d+)?\s*x\b/i.test(candidate) && !/[$¥￥]\s*-?\d+(?:\.\d+)?/.test(candidate)) {
          continue;
        }
        const amount = amountFromLine(candidate);
        if (amount) {
          if (candidate.includes(amount) && candidate.length <= 120) {
            return candidate.slice(0, 120);
          }
          const label = keywords.find((keyword) => line.includes(keyword) || lower.includes(keyword)) || "余额";
          return `${label} ${amount}`.slice(0, 120);
        }
      }
    }
    return "";
  }

  function isTokenQuotaContext(value) {
    const text = sanitizeClient(value || "");
    return (
      text.includes("剩余额度/总额度") ||
      (text.includes("剩余额度") && text.includes("总额度")) ||
      (text.includes("剩余额度") && /密钥|分组|已启用|未启用|已禁用/.test(text))
    );
  }

  function isPricingLike(pageUrl, text) {
    try {
      const path = new URL(String(pageUrl || ""), location.href).pathname.toLowerCase();
      if (/\/(?:pricing|price|models)\b/.test(path)) {
        return true;
      }
    } catch (error) {
      // Fall through to text markers.
    }
    const source = String(text || "");
    const markers = ["可用令牌分组", "模型倍率", "模型名称", "计费模型", "按Token", "按次", "输入价格", "输出价格", "模型价格"];
    return markers.filter((marker) => source.includes(marker)).length >= 2;
  }

  function amountFromLine(line) {
    const money = String(line || "").match(/[$¥￥]\s*-?\d+(?:\.\d+)?/);
    if (money) {
      return money[0].replace(/\s+/g, " ");
    }
    const pattern = /(?<![A-Za-z0-9_.-])-?\d+(?:\.\d+)?(?![A-Za-z0-9_.-])/g;
    const source = String(line || "");
    let match;
    while ((match = pattern.exec(source)) !== null) {
      const suffix = source.slice(pattern.lastIndex, pattern.lastIndex + 8);
      if (/^\s*(?:x\b|条|次|tokens?\b|rpm\b|tpm\b|[kKmM]\b)/i.test(suffix)) {
        continue;
      }
      return match[0];
    }
    return "";
  }

  function detectRateObjects(text) {
    const rows = [];
    const lines = sourceLines(text);
    for (let idx = 0; idx < lines.length; idx += 1) {
      const line = lines[idx];
      const lower = line.toLowerCase();
      const rateMatch = firstRateMatch(line);
      const hasPrice = /[$¥￥]\s*\d+(?:\.\d+)?/.test(line);
      const hasKeyword = /倍率|分组|号池|缓存|价格|余额|额度/.test(line) || /(group|ratio|price|cache|balance|quota)/i.test(line);
      if (!rateMatch && !(hasPrice && hasKeyword)) {
        continue;
      }
      const context = lines.slice(Math.max(0, idx - 2), Math.min(lines.length, idx + 2)).join(" / ");
      const sourceLine = (hasKeyword ? line : context).slice(0, 220);
      rows.push({
        model: guessRateModel(sourceLine),
        page_rate: rateMatch ? Number(rateMatch.value) : null,
        source_line: sourceLine,
      });
      if (rows.length >= MAX_RATE_LINES) {
        break;
      }
    }
    return dedupeRateRows(rows);
  }

  function trimBeforeSelectGroup(value) {
    const text = String(value || "");
    const marker = text.indexOf("选择分组");
    return marker >= 0 ? text.slice(0, marker) : text;
  }

  function rateMatches(value) {
    const text = String(value || "");
    const matches = [];
    const pattern = /(?<![0-9.])(?:(\d+(?:\.\d+)?)\s*[xX]|×\s*(\d+(?:\.\d+)?)|(\d+(?:\.\d+)?)\s*倍)/g;
    let match;
    while ((match = pattern.exec(text)) !== null) {
      const valueText = match[1] || match[2] || match[3];
      if (valueText) {
        matches.push({ index: match.index, value: valueText, raw: match[0] });
      }
    }
    return matches;
  }

  function firstRateMatch(value) {
    const matches = rateMatches(trimBeforeSelectGroup(value));
    return matches.length ? matches[0] : null;
  }

  function lastRateMatch(value) {
    const matches = rateMatches(trimBeforeSelectGroup(value));
    return matches.length ? matches[matches.length - 1] : null;
  }

  function detectAccountObjects(text) {
    const rows = [];
    const structuredRows = Array.from(
      document.querySelectorAll(
        "tr,[role='row'],.ant-table-row,.el-table__row,.semi-table-row,.v-data-table__tr,[class*='table-row'],[class*='TableRow']"
      )
    );
    for (const node of structuredRows.slice(0, 420)) {
      if (node.closest && node.closest("#fluter-upstream-collector-panel")) {
        continue;
      }
      const line = readableElementLine(node);
      const parsed = parseAccountLine(line);
      if (parsed) {
        rows.push(parsed);
      }
      if (rows.length >= MAX_ACCOUNT_ROWS) {
        return dedupeAccountRows(rows);
      }
    }

    const lines = sourceLines(text);
    for (let idx = 0; idx < lines.length; idx += 1) {
      const windowLine = lines.slice(idx, idx + 8).join(" / ");
      const parsed = parseAccountLine(windowLine);
      if (parsed) {
        rows.push(parsed);
      }
      if (rows.length >= MAX_ACCOUNT_ROWS) {
        break;
      }
    }
    return dedupeAccountRows(rows);
  }

  function readableElementLine(node) {
    const pieces = [];
    const push = (value) => {
      const cleaned = String(value || "").trim().replace(/\s+/g, " ");
      if (cleaned) {
        pieces.push(cleaned);
      }
    };

    push(node.innerText || node.textContent || "");
    for (const el of Array.from(node.querySelectorAll("input,textarea,select,button,[title],[aria-label],[data-name],[data-label]")).slice(0, 80)) {
      push(el.getAttribute("aria-label"));
      push(el.getAttribute("title"));
      push(el.getAttribute("data-name"));
      push(el.getAttribute("data-label"));
      if (el.tagName === "INPUT" || el.tagName === "TEXTAREA") {
        push(el.value);
        push(el.getAttribute("placeholder"));
      } else if (el.tagName === "SELECT") {
        const selected = Array.from(el.selectedOptions || []).map((option) => option.textContent || option.value).join(" ");
        push(selected);
      } else {
        push(el.textContent);
      }
    }

    const deduped = [];
    const seen = new Set();
    for (const piece of pieces) {
      const safe = sanitizeClient(piece)
        .split(/\r?\n/)
        .map((part) => part.trim().replace(/\s+/g, " "))
        .filter(Boolean)
        .join(" / ");
      if (!safe || seen.has(safe)) {
        continue;
      }
      seen.add(safe);
      deduped.push(safe);
      if (deduped.length >= 40) {
        break;
      }
    }
    return deduped.join(" / ").slice(0, 600);
  }

  function parseAccountLine(line) {
    const sourceLine = String(line || "").trim().replace(/\s+/g, " ");
    if (!sourceLine || sourceLine.length < 4) {
      return null;
    }
    if (isHeaderLikeLine(sourceLine)) {
      return null;
    }
    const hasSecretMarker = /sk-[A-Za-z0-9_-]{4,}\.\.\.redacted|\.\.\.redacted-long-token\.\.\.|密钥|令牌|token|key/i.test(sourceLine);
    const hasRate = Boolean(firstRateMatch(sourceLine));
    const hasAccountKeyword = /账号|账户|名称|分组|倍率|号池|codex|claude|gpt|meow|magic|kingdom|kbq|deepseek|gemini|grok|sonnet|opus|haiku|生图|仅文字|文字|plus|pro|team|cc\s*max/i.test(sourceLine);
    if (!hasSecretMarker && !hasRate && !hasAccountKeyword) {
      return null;
    }

    const parsedKeyRow = parseAccountLineWithMaskedKey(sourceLine);
    if (parsedKeyRow) {
      return parsedKeyRow;
    }

    const parts = sourceLine
      .split(/\s+\/\s+|\t| {2,}/)
      .map((part) => part.trim())
      .filter(Boolean);
    const accountName = guessAccountName(parts, sourceLine);
    if (!accountName) {
      return null;
    }
    const rateMatch = lastRateMatch(sourceLine);
    const pageRate = rateMatch ? Number(rateMatch.value) : null;
    const upstreamGroup = guessUpstreamGroup(parts, sourceLine);
    return {
      account_name: accountName.slice(0, 160),
      upstream_group: upstreamGroup.slice(0, 160),
      page_rate: Number.isFinite(pageRate) ? pageRate : null,
      source_line: sourceLine.slice(0, 260),
    };
  }

  function maskedKeyPattern() {
    return /(sk-[A-Za-z0-9_-]{2,}\.\.\.[A-Za-z0-9_-]{2,}|sk-[A-Za-z0-9_-]{8}\.\.\.redacted|sk-[A-Za-z0-9_-]{3,}\*{3,}[A-Za-z0-9_-]{2,}|\.\.\.redacted-long-token\.\.\.)/i;
  }

  function parseAccountLineWithMaskedKey(sourceLine) {
    const keyMatch = String(sourceLine || "").match(maskedKeyPattern());
    if (!keyMatch || keyMatch.index === undefined) {
      return null;
    }
    const beforeKey = sourceLine.slice(0, keyMatch.index).trim().replace(/[/:：|｜-]+$/g, "").trim();
    const afterKey = sourceLine.slice(keyMatch.index + keyMatch[0].length).trim();
    const accountName = guessAccountNameFromBeforeKey(beforeKey);
    if (!accountName) {
      return null;
    }
    const trimmedAfterKey = trimBeforeSelectGroup(afterKey);
    const matches = rateMatches(trimmedAfterKey);
    const rateMatch = matches.length ? matches[matches.length - 1] : null;
    const pageRate = rateMatch ? Number(rateMatch.value) : null;
    let upstreamGroup = rateMatch ? guessGroupBeforeRate(trimmedAfterKey.slice(0, rateMatch.index)) : "";
    if (!rateMatch) {
      const beforeRateMatches = rateMatches(beforeKey);
      const beforeRateMatch = beforeRateMatches.length ? beforeRateMatches[0] : null;
      if (beforeRateMatch) {
        upstreamGroup = guessGroupBeforeRate(beforeKey.slice(0, beforeRateMatch.index));
      }
    }
    upstreamGroup = trimGroupPrefix(upstreamGroup, accountName);
    return {
      account_name: accountName.slice(0, 160),
      upstream_group: upstreamGroup.slice(0, 160),
      page_rate: Number.isFinite(pageRate) ? pageRate : rateFromBeforeKey(beforeKey),
      source_line: sourceLine.slice(0, 260),
    };
  }

  function guessAccountNameFromBeforeKey(beforeKey) {
    const pieces = String(beforeKey || "")
      .split(/\s+\/\s+|\t| {2,}/)
      .map((part) => part.trim())
      .filter(Boolean);
    const candidates = pieces.length ? pieces : [beforeKey];
    for (const candidate of candidates) {
      const cleaned = cleanAccountCandidate(candidate);
      if (cleaned && accountNameScore(cleaned, beforeKey) >= 2) {
        return cleaned;
      }
      const compact = compactAccountPrefix(candidate);
      if (compact) {
        return compact;
      }
    }
    return compactAccountPrefix(beforeKey);
  }

  function compactAccountPrefix(value) {
    const text = String(value || "").trim();
    for (const marker of [" 已启用", " 未启用", " 已禁用", " 禁用", " 无限额度", " 有限额度"]) {
      const idx = text.indexOf(marker);
      if (idx > 0) {
        const cleaned = cleanAccountCandidate(text.slice(0, idx));
        if (cleaned) {
          return cleaned;
        }
      }
    }
    return "";
  }

  function rateFromBeforeKey(beforeKey) {
    const match = firstRateMatch(beforeKey);
    return match ? Number(match.value) : null;
  }

  function trimGroupPrefix(group, accountName) {
    let text = String(group || "").trim();
    const account = String(accountName || "").trim();
    if (account && text.startsWith(account)) {
      text = text.slice(account.length).trim();
    }
    let changed = true;
    const statusWords = ["已启用", "未启用", "已禁用", "禁用", "无限额度", "有限额度", "Select this row", "on", "off"];
    while (changed) {
      changed = false;
      for (const word of statusWords) {
        if (text.startsWith(word)) {
          text = text.slice(word.length).replace(/^[/:：|｜\-\s]+/, "").trim();
          changed = true;
        }
      }
    }
    return cleanGroupCandidate(text) || cleanGroupCandidate(group);
  }

  function guessGroupBeforeRate(beforeRate) {
    const pieces = String(beforeRate || "")
      .replace(/^(?:[:：/|｜-]\s*)+/, "")
      .split(/\s+\/\s+|\t| {2,}/)
      .map((part) => part.trim())
      .filter(Boolean)
      .reverse();
    for (const candidate of pieces.length ? pieces : [beforeRate]) {
      const cleaned = cleanGroupCandidate(candidate);
      if (cleaned) {
        return cleaned;
      }
    }
    return "";
  }

  function isHeaderLikeLine(line) {
    const text = String(line || "");
    if (!text) {
      return true;
    }
    const headerWords = ["名称", "状态", "剩余额度", "总额度", "分组", "密钥", "可用模型", "操作"];
    return headerWords.filter((word) => text.includes(word)).length >= 4 && !/\b\d+(?:\.\d+)?x\b/i.test(text);
  }

  function guessAccountName(parts, sourceLine) {
    const candidates = [];
    for (const part of parts.length ? parts : sourceLines(sourceLine)) {
      const cleaned = cleanAccountCandidate(part);
      if (!cleaned) {
        continue;
      }
      candidates.push({ value: cleaned, score: accountNameScore(cleaned, sourceLine) });
    }
    candidates.sort((a, b) => b.score - a.score || b.value.length - a.value.length);
    return candidates.length && candidates[0].score >= 3 ? candidates[0].value : "";
  }

  function cleanAccountCandidate(part) {
    let value = String(part || "").trim();
    value = value.replace(/^(?:名称|账号名称|账户名称|账号|账户|备注|name)[:：]?\s*/i, "").trim();
    if (!value || value.length < 3 || value.length > 100) {
      return "";
    }
    if (/sk-[A-Za-z0-9_-]{2,}\.\.\.[A-Za-z0-9_-]{2,}|sk-[A-Za-z0-9_-]{4,}|\.\.\.redacted|bearer|api[_ -]?key|https?:\/\//i.test(value)) {
      return "";
    }
    if (/\b\d+(?:\.\d+)?x\b/i.test(value) || /^[$¥￥]?\s*-?\d+(?:\.\d+)?$/.test(value)) {
      return "";
    }
    if (/^(正常|启用|已启用|禁用|错误|已停用|无限制|无限额度|选择分组|编辑|删除|复制|查看|操作|状态|密钥|令牌|token|key|on|off)$/i.test(value)) {
      return "";
    }
    if (/Fluter 上游采集|脚本：|collector|本页识别|最近结果|自动发送快照|刷新后发送|设置 token|可用模型|剩余额度|总额度|请求路径|计费过程|使用记录|日志详情|上次使用时间|创建时间|永久有效|点击更换分组|导入到 CCS|You need to enable JavaScript to run this app/.test(value)) {
      return "";
    }
    if (/^\d{1,2}\s+\d{2}:\d{2}:\d{2}\b/.test(value) || /^\d{4}[/-]\d{2}[/-]\d{2}\b/.test(value)) {
      return "";
    }
    return value;
  }

  function accountNameScore(value, sourceLine) {
    let score = 0;
    const source = String(sourceLine || "");
    if (source.startsWith(value)) score += 3;
    if (/codex|claude|gpt|deepseek|gemini|grok|sonnet|opus|haiku/i.test(value)) score += 4;
    if (/meow|magic|kingdom|kbq|mouubox|congming|超超|钧澈|聪明|神风/i.test(value)) score += 3;
    if (/生图|仅文字|文字|plus|pro|team|cc\s*max|ccmax|0\.\d+/i.test(value)) score += 2;
    if (/^[（(]?修改[）)]/.test(value)) score += 2;
    if (/号池|分组|渠道/.test(value)) score -= 1;
    if (/选择分组|点击更换分组|永久有效|活跃|使用密钥|导入到 CCS/.test(value)) score -= 5;
    if (/^\d/.test(value) && !/[A-Za-z\u4e00-\u9fff]/.test(value.replace(/[0-9.]/g, ""))) score -= 4;
    if (value.length >= 8) score += 1;
    return score;
  }

  function guessUpstreamGroup(parts, sourceLine) {
    for (let idx = 0; idx < parts.length; idx += 1) {
      if (!firstRateMatch(parts[idx])) {
        continue;
      }
      for (let look = idx - 1; look >= 0; look -= 1) {
        const candidate = cleanGroupCandidate(parts[look]);
        if (candidate) {
          return candidate;
        }
      }
    }
    const groupMatch = String(sourceLine || "").match(/(?:分组|号池|渠道)[:：]?\s*([^/]{2,80})/);
    return groupMatch ? cleanGroupCandidate(groupMatch[1]) : "";
  }

  function cleanGroupCandidate(value) {
    const text = String(value || "").trim();
    if (!text || text.length > 100) {
      return "";
    }
    if (/sk-[A-Za-z0-9_-]{4,}|\.\.\.redacted|bearer|api[_ -]?key/i.test(text)) {
      return "";
    }
    if (/^(正常|启用|已启用|禁用|错误|无限额度|选择分组|编辑|删除|复制|查看|操作|on|off)$/i.test(text)) {
      return "";
    }
    if (/You need to enable JavaScript to run this app/.test(text)) {
      return "";
    }
    if (/\b\d+(?:\.\d+)?x\b/i.test(text)) {
      return "";
    }
    return text;
  }

  function dedupeAccountRows(rows) {
    const seen = new Set();
    const deduped = [];
    for (const row of rows) {
      const key = [row.account_name, row.upstream_group, row.page_rate].join("|");
      if (seen.has(key)) {
        continue;
      }
      seen.add(key);
      deduped.push(row);
      if (deduped.length >= MAX_ACCOUNT_ROWS) {
        break;
      }
    }
    return deduped;
  }

  function guessRateModel(line) {
    const match = String(line || "").match(/([A-Za-z0-9_\-[\].:+/]+(?:claude|gpt|codex|deepseek|gemini|grok|image|sonnet|opus|haiku)[A-Za-z0-9_\-[\].:+/]*)/i);
    if (match) {
      return match[1].slice(0, 160);
    }
    const beforeRate = String(line || "").split(/\d+(?:\.\d+)?x/i)[0] || "";
    return beforeRate.replace(/[/:：|｜]+$/g, "").trim().slice(-160);
  }

  function dedupeRateRows(rows) {
    const seen = new Set();
    const deduped = [];
    for (const row of rows) {
      const key = String(row.source_line || "");
      if (seen.has(key)) {
        continue;
      }
      seen.add(key);
      deduped.push(row);
    }
    return deduped;
  }

  function compactExcerpt(text, limit) {
    const interesting = [];
    for (const line of sourceLines(text)) {
      const lower = line.toLowerCase();
      if (
        line.includes("余额") ||
        line.includes("额度") ||
        line.includes("倍率") ||
        line.includes("分组") ||
        line.includes("号池") ||
        line.includes("使用记录") ||
        line.includes("日志") ||
        lower.includes("balance") ||
        lower.includes("quota") ||
        lower.includes("group") ||
        /\b\d+(?:\.\d+)?x\b/i.test(line)
      ) {
        interesting.push(line.slice(0, 180));
      }
      if (interesting.join("\n").length >= limit) {
        break;
      }
    }
    const fallback = interesting.length ? interesting : sourceLines(text).slice(0, 10);
    return fallback.join("\n").slice(0, limit);
  }
})();
