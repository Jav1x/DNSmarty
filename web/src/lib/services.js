import { fqdnCheck } from "./logsfilters";

export function parseDomainsInput(text) {
  const ok = [];
  const bad = [];
  const seen = new Set();
  for (const raw of text.split(/[\s,]+/)) {
    if (!raw) continue;
    const r = fqdnCheck(raw);
    if (!r.ok) {
      bad.push({ raw, reason: r.reason });
      continue;
    }
    const name = r.canonical.replace(/\.$/, "");
    if (!name || seen.has(name)) continue;
    seen.add(name);
    ok.push(name);
  }
  return { ok, bad };
}

export function templateSummary(tpl, t) {
  return t("tplSummary", { name: tpl.name, count: tpl.domains.length });
}

export function isBuiltinTemplate(id) {
  return String(id || "").startsWith("builtin:");
}

export function readTemplatePayload(raw) {
  const empty = { domains: [], strategy: "round_robin", proxies: [] };
  if (!raw) return empty;
  let p = raw;
  if (typeof p === "string") {
    try { p = JSON.parse(p); } catch { return empty; }
  }
  if (typeof p !== "object") return empty;
  return {
    domains: Array.isArray(p.domains) ? p.domains : [],
    strategy: p.strategy || "round_robin",
    proxies: Array.isArray(p.proxies) ? p.proxies : [],
  };
}

export function templatePayloadFromService(svc) {
  return {
    domains: (svc.members || []).map((m) => ({
      name: m.name,
      match: m.match,
      enabled: m.enabled,
      comment: m.comment || "",
    })),
    strategy: svc.strategy || "round_robin",
    proxies: (svc.proxies || []).filter((p) => p.on).map((p) => ({
      proxy_id: p.proxy_id,
      weight: p.weight,
    })),
  };
}
