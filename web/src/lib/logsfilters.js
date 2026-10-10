export function normDomainInput(s) {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9.\-]/g, "")
    .replace(/\.{2,}/g, ".")
    .replace(/^\.+/, "");
}

const LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;


export function fqdnCheck(vRaw) {
  const v = vRaw.toLowerCase();
  if (!v) return { ok: true, reason: "", canonical: "", bad: "" };
  if (v.length > 253) return { ok: false, reason: "tooLong", canonical: "", bad: "" };
  const labels = v.replace(/\.$/, "").split(".");
  for (const l of labels) {
    if (!l) return { ok: false, reason: "emptyLabel", canonical: "", bad: "" };
    if (!LABEL.test(l)) return { ok: false, reason: "badLabel", canonical: "", bad: l };
  }
  return { ok: true, reason: "", canonical: v.endsWith(".") ? v : `${v}.`, bad: "" };
}

export function maskDate(v) {
  let d = String(v).replace(/[^\d]/g, "").slice(0, 8);
  if (d.length > 4) return `${d.slice(0, 2)}.${d.slice(2, 4)}.${d.slice(4)}`;
  if (d.length > 2) return `${d.slice(0, 2)}.${d.slice(2)}`;
  return d;
}

export function parseDay(s) {
  if (!s) return { ok: true, t: null };
  const m = /^(\d{2})\.(\d{2})\.(\d{4})$/.exec(s);
  if (!m) return { ok: false, t: null };
  const [, d, mo, y] = m.map(Number);
  if (mo < 1 || mo > 12 || d < 1) return { ok: false, t: null };
  const t = new Date(y, mo - 1, d);
  if (t.getMonth() !== mo - 1 || t.getDate() !== d) return { ok: false, t: null };
  return { ok: true, t };
}

const PRESET_MS = { "1h": 3600_000, "24h": 86400_000, "7d": 7 * 86400_000 };

export function timeRange(applied, now = new Date()) {
  if (applied.day) {
    const r = parseDay(applied.day);
    return r.ok ? { start: r.t } : null;
  }
  if (applied.preset && PRESET_MS[applied.preset]) {
    return { start: new Date(now.getTime() - PRESET_MS[applied.preset]) };
  }
  return null;
}

export function withinRange(iso, range) {
  if (!range) return true;
  return new Date(iso).getTime() >= range.start.getTime();
}

export function histoBuckets(isoTimes, n = 24) {
  if (!isoTimes.length) return null;
  const times = isoTimes.map((s) => new Date(s).getTime());
  const min = Math.min(...times);
  const max = Math.max(...times);
  const span = max - min;
  const counts = Array.from({ length: n }, () => 0);
  for (const t of times) {
    const idx = span ? Math.min(n - 1, Math.floor(((t - min) / span) * n)) : 0;
    counts[idx] += 1;
  }
  const top = Math.max(...counts);
  let hot = 0;
  counts.forEach((c, i) => {
    if (c > counts[hot]) hot = i;
  });
  return { heights: counts.map((c) => Math.round((c / top) * 100)), hot };
}

export function activeFilters(kind, applied) {
  const out = [];
  if (applied.q) out.push({ k: "q", v: applied.q });
  if (applied.ip) out.push({ k: "ip", v: applied.ip });
  if (kind === "dns") {
    if (applied.decision) out.push({ k: "decision", v: applied.decision });
    if (applied.qtype) out.push({ k: "qtype", v: applied.qtype });
    if (applied.rcode) out.push({ k: "rcode", v: applied.rcode });
  } else if (applied.status) {
    out.push({ k: "status", v: applied.status });
  }
  if (applied.preset) out.push({ k: "time", v: applied.preset });
  else if (applied.day) out.push({ k: "time", v: applied.day });
  return out;
}

export function matchRow(row, qtype, rcode, range) {
  if (qtype && row.qtype !== qtype) return false;
  if (rcode && row.rcode !== rcode) return false;
  return withinRange(row.at, range);
}

export function auditGroup(action) {
  if (action === "login" || action === "login.fail" || action === "logout") return "login";
  const prefix = action.split(".")[0];
  if (prefix === "totp" || prefix === "password") return "login";
  if (prefix === "oauth") return "oauth";
  if (prefix === "client" || prefix === "lists") return "acl";
  if (prefix === "upstream") return "settings";
  if (prefix === "domain" || prefix === "template") return "service";
  if (["node", "session", "service", "settings", "acl"].includes(prefix)) return prefix;
  return "other";
}

export function sortRows(rows, key, dir) {
  const mul = dir === "asc" ? 1 : -1;
  return [...rows].sort((a, b) => {
    const va = a[key];
    const vb = b[key];
    if (va == null && vb == null) return 0;
    if (va == null) return 1;
    if (vb == null) return -1;
    if (typeof va === "number" && typeof vb === "number") return (va - vb) * mul;
    return String(va).localeCompare(String(vb), undefined, { numeric: true }) * mul;
  });
}

export function auditDetail(_action, raw) {
  if (!raw || raw === "{}") return [];
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [{ k: "raw", v: raw }];
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    return [{ k: "raw", v: raw }];
  }
  return Object.entries(parsed).map(([k, v]) => ({ k, v }));
}

export function mergeById(prev, add) {
  const base = prev || [];
  const seen = new Set(base.map((r) => r.id));
  const fresh = [];
  for (const r of add) {
    if (seen.has(r.id)) continue;
    seen.add(r.id);
    fresh.push(r);
  }
  return [...base, ...fresh];
}
