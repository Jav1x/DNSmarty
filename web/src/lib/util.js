export function normFqdn(s) {
  return s.trim().toLowerCase().replace(/\.+$/, '');
}

export function parseCidr(s) {
  if (!s) return { ok: true, key: "", params: {} };
  if (s.includes(":")) return { ok: false, key: "cidrIpv6Later", params: {} };
  const m = s.match(/^(\d{1,3}(?:\.\d{1,3}){3})\/(\d{1,3})$/);
  if (!m) return { ok: false, key: "cidrFormat", params: {} };
  const oct = m[1].split(".").map(Number);
  if (oct.some((o) => o > 255)) return { ok: false, key: "cidrOctet", params: {} };
  const len = Number(m[2]);
  if (len > 32) return { ok: false, key: "cidrMask", params: {} };
  const addrs = 2 ** (32 - len);
  if (len === 32) return { ok: true, key: "cidrSingle", params: {}, addrs };
  if (len === 24) return { ok: true, key: "cidrSubnet24", params: {}, addrs };
  return { ok: true, key: "cidrSubnet", params: { len, addrs }, addrs };
}

export function shares(weights) {
  const total = weights.reduce((a, b) => a + b, 0);
  if (!total) return weights.map(() => 0);
  const pct = weights.map((w) => Math.floor((w / total) * 100));
  pct[pct.length - 1] += 100 - pct.reduce((a, b) => a + b, 0);
  return pct;
}

export function fmtBytes(n) {
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB'];
  let v = n;
  for (const u of units) {
    v /= 1024;
    if (v < 1024) return `${v.toFixed(1)} ${u}`;
  }
  return `${(v / 1024).toFixed(1)} TB`;
}

export function protoOf(addr) {
  const s = String(addr).trim().toLowerCase();
  if (s.startsWith('https://')) return 'doh';
  if (s.startsWith('tls://') || /:853$/.test(s)) return 'dot';
  return 'udp';
}

export function ago(iso, t) {
  const sec = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return t("agoSec", { n: sec });
  const min = Math.floor(sec / 60);
  if (min < 60) return t("agoMin", { n: min });
  const h = Math.floor(min / 60);
  if (h < 24) return t("agoHour", { n: h });
  return t("agoDay", { n: Math.floor(h / 24) });
}

export function pushHist(hist, value, n) {
  const next = [...hist, value];
  return next.length > n ? next.slice(next.length - n) : next;
}

export function sparkPaths(values, w = 200, h = 40, pad = 3) {
  const n = values.length;
  if (n < 2) return null;
  const max = Math.max(...values);
  const min = Math.min(...values);
  const y = (v) => (max === min ? h / 2 : h - pad - ((v - min) / (max - min)) * (h - 2 * pad));
  const f = (x) => String(Math.round(x * 100) / 100);
  const pts = values.map((v, i) => `${f((i / (n - 1)) * w)},${f(y(v))}`);
  const line = `M${pts.join(" L")}`;
  return { line, fill: `${line} L${w},${h} L0,${h} Z` };
}

export function aclShare(points) {
  const dns = points.reduce((a, p) => a + (p.dns || 0), 0);
  if (!dns) return 0;
  return (points.reduce((a, p) => a + (p.refused || 0), 0) / dns) * 100;
}

export function statsPath(win) {
  return `/api/stats?window=${win}`;
}

export function clientStatsPath(ip, win) {
  return `/api/stats/client?ip=${encodeURIComponent(ip)}&window=${win}`;
}

export function cleanClientIp(s) {
  return String(s ?? "").trim();
}

export function pwStrength(pw) {
  const v = String(pw ?? "");
  let s = 0;
  if (v.length >= 12) s++;
  if (/[a-z]/.test(v) && /[A-Z]/.test(v)) s++;
  if (/\d/.test(v) || /[^a-zA-Z0-9]/.test(v)) s++;
  if (v.length >= 16 && /[^a-zA-Z0-9]/.test(v)) s++;
  return s;
}

export function deviceIcon(userAgent) {
  return /iphone|android|mobile/i.test(String(userAgent ?? "")) ? "📱" : "💻";
}

export function fmtUptime(sec) {
  if (sec == null || Number.isNaN(Number(sec))) return "—";
  const s = Math.max(0, Math.floor(Number(sec)));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m`;
  return `${s}s`;
}

export function fmtMbps(v) {
  if (v == null || Number.isNaN(Number(v))) return "—";
  return `${Number(v).toFixed(1)} Mbps`;
}

export function hwRows(hw) {
  const fmt = (v, f) => (v == null || v === "" ? "—" : f(v));
  return [
    ["hwCpu", fmt(hw.cpu_model, (m) => (hw.cpu_cores ? `${m} · ${hw.cpu_cores}` : m))],
    ["hwMem", fmt(hw.mem_total_mb, (mb) => `${(mb / 1024).toFixed(1)} GB · ${fmt(hw.mem_used_pct, (p) => `${Math.round(p)}%`)}`)],
    ["hwOs", fmt(hw.os, (o) => (hw.kernel ? `${o} · ${hw.kernel}` : o))],
    ["hwDisk", fmt(hw.disk_total_gb, (tot) => `${fmt(hw.disk_used_gb, (u) => u.toFixed(1))} / ${tot.toFixed(1)} GB`)],
  ];
}
