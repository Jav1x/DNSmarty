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

// Русские короткие формы: «12 с», «3 мин», «2 ч», «27 д».
export function ago(iso) {
  const sec = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${sec} с`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} мин`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h} ч`;
  return `${Math.floor(h / 24)} д`;
}
