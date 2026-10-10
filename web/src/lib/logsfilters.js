// Чистая логика страниц «Журналы» (лаба 9) и «Аудит» (лаба 16) — покрыта
// logsfilters.test.js. Контракт сервера (internal/panel/api.go, s.logs/s.auditLog):
// /api/logs фильтрует по q/ip и decision|status на своей стороне; времени,
// qtype и rcode там нет — они применяются на клиенте к загруженным строкам
// (rows идут по at DESC, поэтому срез «не новее начала окна» честный).

// Живое форматирование ввода домена (скрипт лабы 9): нижний регистр, только
// допустимые символы, двойные точки схлопываются, ведущие — срезаются.
export function normDomainInput(s) {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9.\-]/g, "")
    .replace(/\.{2,}/g, ".")
    .replace(/^\.+/, "");
}

const LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

// Валидация домена с живым хинтом (лаба 9): reason — код для t(), canonical —
// канонический FQDN с точкой (показывается в хинте при ok).
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

// Маска ДД.ММ.ГГГГ на лету (лаба 9): только цифры, точки после 2-й и 4-й.
export function maskDate(v) {
  let d = String(v).replace(/[^\d]/g, "").slice(0, 8);
  if (d.length > 4) return `${d.slice(0, 2)}.${d.slice(2, 4)}.${d.slice(4)}`;
  if (d.length > 2) return `${d.slice(0, 2)}.${d.slice(2)}`;
  return d;
}

// Разбор замаскированной даты: пустая строка — «фильтра нет»; невозможная
// дата (32.10…, 31.02…) — ok:false.
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

// Время фильтрации: окно [start, now] из пресета или начала суток дня.
// День, если задан, сильнее пресета (UI держит их взаимоисключающими).
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

// Строка внутри окна. Конец окна — «сейчас», строки из будущего не приходят.
export function withinRange(iso, range) {
  if (!range) return true;
  return new Date(iso).getTime() >= range.start.getTime();
}

// Гистограмма лабы 9: n колонок по размаху загруженных времён, высоты в %,
// hot — индекс максимума (первый). Пусто — гистограммы нет.
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

// Сериализация применённых фильтров в съёмные чипы (лаба 9, .activef):
// [{k, v}] в порядке фильтр-бара; v — сырое значение, переводит t() в компоненте.
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

// Клиентская часть фильтра строки: qtype/rcode (DNS) и окно времени.
export function matchRow(row, qtype, rcode, range) {
  if (qtype && row.qtype !== qtype) return false;
  if (rcode && row.rcode !== rcode) return false;
  return withinRange(row.at, range);
}

// Группа действия аудита для пилюль типов (лаба 16). Фаза 1 знает только
// login/login.fail/logout и node.check — остальные префиксы на будущее.
export function auditGroup(action) {
  if (action === "login" || action === "login.fail" || action === "logout") return "login";
  const prefix = action.split(".")[0];
  if (["node", "session", "service", "settings", "acl"].includes(prefix)) return prefix;
  return "other";
}

// Человекочитаемые детали аудита: [{k, v}] — k отдаётся в t() компонентом
// (detIp/detId/detNodeOk/detNodeFail), raw — непонятный payload как есть.
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

// Склейка страниц: дубликаты по id (двойной клик «Загрузить ещё») отбрасываются.
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
