import { fqdnCheck } from "./logsfilters";

// Разбор вставки доменов: разделители — пробелы, запятые, переводы строк.
// Дубликаты схлопываются, невалидные имена уходят в bad с кодом причины.
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
