// Чистые утилиты редизайна — без зависимостей, покрыты util.test.js.

export function normFqdn(s) {
  return s.trim().toLowerCase().replace(/\.+$/, '');
}

// Валидация CIDR — msg по лабе 12 (.hint логика).
export function parseCidr(s) {
  if (!s) return { ok: true, msg: '' };
  if (s.includes(':')) return { ok: false, msg: 'IPv6 — во второй фазе' };
  const m = s.match(/^(\d{1,3}(?:\.\d{1,3}){3})\/(\d{1,3})$/);
  if (!m) return { ok: false, msg: 'формат: a.b.c.d/len' };
  const oct = m[1].split('.').map(Number);
  if (oct.some((o) => o > 255)) return { ok: false, msg: 'октет > 255' };
  const len = Number(m[2]);
  if (len > 32) return { ok: false, msg: 'маска > 32' };
  const addrs = 2 ** (32 - len);
  if (len === 32) return { ok: true, msg: '✓ одиночный адрес (/32)', addrs };
  if (len === 24) return { ok: true, msg: '✓ подсеть /24 — 256 адресов', addrs };
  return { ok: true, msg: `✓ подсеть /${len} — ${addrs} адресов`, addrs };
}

// Доли в процентах, сумма ровно 100; нули/пустой массив — без NaN.
export function shares(weights) {
  const total = weights.reduce((a, b) => a + b, 0);
  if (!total) return weights.map(() => 0);
  const pct = weights.map((w) => Math.floor((w / total) * 100));
  pct[pct.length - 1] += 100 - pct.reduce((a, b) => a + b, 0);
  return pct;
}

// KB/MB/GB как formatBytes в Overview.jsx.
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

// Протокол апстрима — вычисляется на клиенте из адреса (лаба 13):
// https://… → DoH, tls://… или порт 853 → DoT, иначе UDP.
export function protoOf(addr) {
  const s = String(addr).trim().toLowerCase();
  if (s.startsWith('https://')) return 'doh';
  if (s.startsWith('tls://') || /:853$/.test(s)) return 'dot';
  return 'udp';
}

// Короткие формы «12 s» / «12 с»: единицы — сокращения, не склоняются, поэтому
// плюрал-функции не нужны; t передаётся аргументом (модуль вне React).
export function ago(iso, t) {
  const sec = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return t("agoSec", { n: sec });
  const min = Math.floor(sec / 60);
  if (min < 60) return t("agoMin", { n: min });
  const h = Math.floor(min / 60);
  if (h < 24) return t("agoHour", { n: h });
  return t("agoDay", { n: Math.floor(h / 24) });
}

// Обзор (лаба 16): скользящий буфер живых спарклайнов hero-банда —
// хранит последние n точек (N≈30 опросов по 10 с).
export function pushHist(hist, value, n) {
  const next = [...hist, value];
  return next.length > n ? next.slice(next.length - n) : next;
}

// d-строки inline-SVG спарклайна lab16 (path.fill — площадь, path.a — линия),
// viewBox w×h с отступом pad. Меньше двух точек — пути нет.
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

// Доля decision='acl' среди DNS-запросов серии точек /api/overview/series
// (у точки поля dns/refused — из count(*) FILTER decision='acl').
export function aclShare(points) {
  const dns = points.reduce((a, p) => a + (p.dns || 0), 0);
  if (!dns) return 0;
  return (points.reduce((a, p) => a + (p.refused || 0), 0) / dns) * 100;
}

// Статистика (задача 10): адреса GET /api/stats и GET /api/stats/client.
// window — ключ пилюли: "1h" | "6h" | "24h" | "all" (сервер принимает их как есть).
export function statsPath(win) {
  return `/api/stats?window=${win}`;
}

// Drill-down клиента: ip кодируется (у IPv6 есть двоеточия), window — тот же ключ.
export function clientStatsPath(ip, win) {
  return `/api/stats/client?ip=${encodeURIComponent(ip)}&window=${win}`;
}

// Минимальная очистка ввода IP: пробелы по краям; пустая строка — «не введено».
// Формат проверяет сервер (netip.ParseAddr) и отдаёт ошибку через Err.
export function cleanClientIp(s) {
  return String(s ?? "").trim();
}

// Надёжность пароля 0–4 для живого индикатора аккаунта (лаба 14) — тот же
// счёт, что в скрипте лабы: балл за длину (12+) и за разнообразие символов
// (смешанный регистр; цифра или знак; знак при длине 16+). Кап 4.
export function pwStrength(pw) {
  const v = String(pw ?? "");
  let s = 0;
  if (v.length >= 12) s++;
  if (/[a-z]/.test(v) && /[A-Z]/.test(v)) s++;
  if (/\d/.test(v) || /[^a-zA-Z0-9]/.test(v)) s++;
  if (v.length >= 16 && /[^a-zA-Z0-9]/.test(v)) s++;
  return s;
}

// Иконка устройства по user-agent (лаба 14): эвристика по подстрокам —
// мобильные (iPhone/Android/Mobile) — телефон, всё прочее (и пустое) — компьютер.
export function deviceIcon(userAgent) {
  return /iphone|android|mobile/i.test(String(userAgent ?? "")) ? "📱" : "💻";
}
