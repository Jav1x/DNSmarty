// Журналы (лаба 9): фильтр-бар DNS/Прокси — домен с живой нормализацией
// (normFqdn) и хинтом, IP/CIDR, пресеты времени 1ч/24ч/7д + маска ДД.ММ.ГГГГ,
// пилюли решение/qtype/rcode, съёмные чипы активных фильтров, гистограмма
// совпадений. «Заблокировано» в таблице = decision='acl'.
//
// Контракт (проверен по internal/panel/api.go s.logs и internal/store/traffic.go):
//   GET /api/logs?kind=dns|proxy&q=&ip=&decision=|status=&before=&before_id=
//     → { rows: [...], next: {at, id} | null }, rows по at DESC, keyset-пагинация.
//   DNS:   { id, at, client_ip, name, qtype, rcode, decision }
//   Proxy: { id, at, client_ip, sni, bytes_up, bytes_down, status, dial_error }
//   Серверная сторона фильтрует только q/ip/decision|status: времени, qtype,
//   rcode и ноды в контракте нет. Время и (у DNS) qtype/rcode фильтруются на
//   клиенте по загруженным строкам — честно, потому что строки идут
//   новее-наверху, а окно всегда кончается «сейчас»; колонки «Узел» нет.
import { useEffect, useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { EmptyRow, Err, SkeletonRows } from "../components/Bits";
import {
  activeFilters,
  fqdnCheck,
  histoBuckets,
  maskDate,
  matchRow,
  normDomainInput,
  parseDay,
  timeRange,
} from "../lib/logsfilters";
import { normFqdn } from "../lib/util";

const DECISIONS = ["acl", "local", "forward", "cached", "fail", "limited"];
const PROXY_STATUSES = ["ok", "refused", "acl", "limited", "dial_error", "overload"];
const QTYPES = ["A", "AAAA", "MX", "TXT", "HTTPS"];
const RCODES = ["NOERROR", "NXDOMAIN", "SERVFAIL"];
const PRESETS = [
  ["1h", "preset1h"],
  ["24h", "preset24h"],
  ["7d", "preset7d"],
];
const EMPTY = { q: "", ip: "", decision: "", status: "", qtype: "", rcode: "", preset: "", day: "" };
const HINT_KEYS = { tooLong: "fqdnTooLong", emptyLabel: "fqdnEmptyLabel", badLabel: "fqdnBadLabel" };
const CHIP_KEYS = { q: "chipDomain", ip: "chipIp", decision: "decision", status: "status", qtype: "qtype", rcode: "rcode" };

// Параметры запроса: q/ip/decision|status уезжают на сервер, курсор — keyset.
function logsParams(kind, applied, cur) {
  const p = new URLSearchParams({ kind, q: applied.q, ip: applied.ip });
  if (kind === "dns" && applied.decision) p.set("decision", applied.decision);
  if (kind === "proxy" && applied.status) p.set("status", applied.status);
  if (cur) {
    p.set("before", cur.at);
    p.set("before_id", cur.id);
  }
  return p;
}

// Подпись чипа: технические значения — как есть (acl, A, NOERROR), время —
// пресет или «ДД.ММ.ГГГГ — сегодня».
function chipLabel(c, t) {
  const name = CHIP_KEYS[c.k] || c.k;
  if (c.k === "time") {
    const value = /^\d{2}\.\d{2}\.\d{4}$/.test(c.v) ? `${c.v} — ${t("today")}` : t(`preset${c.v}`);
    return `${t("chipTime")}: ${value}`;
  }
  return `${t(name)}: ${c.v}`;
}

// Группа пилюль фильтр-бара (лаба 9): «все» + значения, переключение в draft.
function PillRow({ label, options, value, onPick }) {
  const { t } = useI18n();
  return (
    <>
      <span className="lbl">{label}</span>
      <button type="button" className={`pill${value === "" ? " on" : ""}`} aria-pressed={value === ""} onClick={() => onPick("")}>
        {t("filterAll")}
      </button>
      {options.map((o) => (
        <button key={o} type="button" className={`pill${value === o ? " on" : ""}`} aria-pressed={value === o} onClick={() => onPick(o)}>
          {o}
        </button>
      ))}
    </>
  );
}

// Съёмный чип активного фильтра (лаба 9): ✕ снимает фильтр и перезапрашивает.
function FilterChip({ label, onRemove }) {
  return (
    <span className="chip">
      {label} <b onClick={onRemove}>✕</b>
    </span>
  );
}

function dnsCells(row, t) {
  const cls = row.decision === "acl" ? "dec d" : row.decision === "fail" || row.decision === "limited" ? "dec" : "dec a";
  return [
    <td key="t">{new Date(row.at).toLocaleString()}</td>,
    <td key="ip" className="mono">{row.client_ip}</td>,
    <td key="n" className="mono">{row.name}</td>,
    <td key="q">{row.qtype}</td>,
    <td key="r" className={row.rcode && row.rcode !== "NOERROR" ? "hot" : undefined}>{row.rcode}</td>,
    <td key="d" className={cls}>{row.decision === "acl" ? t("blockedCol") : row.decision}</td>,
  ];
}

function proxyCells(row, err) {
  return [
    <td key="t">{new Date(row.at).toLocaleString()}</td>,
    <td key="ip" className="mono">{row.client_ip}</td>,
    <td key="s" className="mono">{row.sni}</td>,
    <td key="b" className="bytes">{row.bytes_up} / {row.bytes_down}</td>,
    <td key="st">{row.status}</td>,
    <td key="d">{row.dial_error ? err(row.dial_error) : ""}</td>,
  ];
}

export function Logs() {
  const { t, err } = useI18n();
  const [kind, setKind] = useState("dns");
  const [draft, setDraft] = useState(EMPTY);
  const [applied, setApplied] = useState(EMPTY);
  const [rows, setRows] = useState(null);
  const [next, setNext] = useState(null);
  const [error, setError] = useState("");

  const setD = (patch) => setDraft((d) => ({ ...d, ...patch }));
  const appliedKey = JSON.stringify(applied);

  // Первая страница при смене вида или применённых фильтров. live-флаг гасит
  // ответ сменённого запроса; таймеров здесь нет.
  useEffect(() => {
    let live = true;
    setRows(null);
    api(`/api/logs?${logsParams(kind, applied, null)}`)
      .then((out) => {
        if (!live) return;
        setRows(out.rows);
        setNext(out.next || null);
        setError("");
      })
      .catch((e) => {
        if (live) setError(e);
      });
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- appliedKey заменяет объект applied
  }, [kind, appliedKey]);

  async function loadMore() {
    try {
      const out = await api(`/api/logs?${logsParams(kind, applied, next)}`);
      setRows((r) => [...(r || []), ...out.rows]);
      setNext(out.next || null);
      setError("");
    } catch (e) {
      setError(e);
    }
  }

  function apply(event) {
    event.preventDefault();
    setApplied({ ...draft, q: normFqdn(draft.q), ip: draft.ip.trim() });
  }

  function reset() {
    setDraft(EMPTY);
    setApplied(EMPTY);
  }

  // Съём чипа убирает фильтр и из применённых, и из черновика — строка тут же
  // перезапрашивается эффектом.
  function removeChip(k) {
    const nextA = k === "time" ? { ...applied, preset: "", day: "" } : { ...applied, [k]: "" };
    setApplied(nextA);
    setDraft(nextA);
  }

  const range = timeRange(applied);
  const visible = (rows || []).filter((r) =>
    matchRow(r, kind === "dns" ? applied.qtype : "", kind === "dns" ? applied.rcode : "", range));
  const chips = activeFilters(kind, applied);
  const histo = visible.length ? histoBuckets(visible.map((r) => r.at)) : null;
  const chk = fqdnCheck(draft.q);
  const dayValid = !draft.day || parseDay(draft.day).ok;
  // Пока самая старая загруженная строка внутри окна — ниже могут быть ещё;
  // как только ушли до его начала — страница последняя (конец окна = сейчас).
  const exhausted = Boolean(range && rows && rows.length && new Date(rows[rows.length - 1].at) < range.start);
  const cols = 6;

  return (
    <>
      <div className="hrow">
        <h1>{t("logs")}</h1>
      </div>

      <form className="fbar" onSubmit={apply}>
        <div className="row1">
          <span className="sel" role="group" aria-label={t("logs")}>
            <button type="button" className={kind === "dns" ? "on" : ""} aria-pressed={kind === "dns"} onClick={() => setKind("dns")}>{t("kindDns")}</button>
            <button type="button" className={kind === "proxy" ? "on" : ""} aria-pressed={kind === "proxy"} onClick={() => setKind("proxy")}>{t("proxyTitle")}</button>
          </span>
          <span className="domwrap">
            <input
              className="finput dom"
              value={draft.q}
              placeholder={t("domainPh")}
              autoComplete="off"
              spellCheck="false"
              aria-label={t("domain")}
              onChange={(e) => setD({ q: normDomainInput(e.target.value) })}
            />
            {draft.q !== "" && (chk.reason
              ? <span className="domhint bad">{t(HINT_KEYS[chk.reason], { s: chk.bad })}</span>
              : <span className="domhint">✓ {chk.canonical}</span>)}
          </span>
          <input
            className="finput ip"
            value={draft.ip}
            placeholder={t("ipPlaceholder")}
            autoComplete="off"
            spellCheck="false"
            aria-label={t("clientIp")}
            onChange={(e) => setD({ ip: e.target.value })}
          />
          <span className="timegrp">
            {PRESETS.map(([key, lbl]) => (
              <button key={key} type="button" className={`quick${draft.preset === key ? " on" : ""}`} aria-pressed={draft.preset === key} onClick={() => setD({ preset: key, day: "" })}>
                {t(lbl)}
              </button>
            ))}
            <input
              className="finput dt"
              value={draft.day}
              placeholder={t("datePh")}
              autoComplete="off"
              aria-label={t("datePh")}
              onChange={(e) => setD({ day: maskDate(e.target.value), preset: "" })}
            />
            <span className="dash">—</span>
            <input className="finput dt" value="" placeholder={t("today")} disabled aria-label={t("today")} />
          </span>
          <button type="submit" className="apply" disabled={!dayValid}>{t("apply")}</button>
          <button type="button" className="apply ghost" onClick={reset}>{t("reset")}</button>
        </div>
        <div className="row2">
          {kind === "dns" ? (
            <>
              <PillRow label={t("decision")} options={DECISIONS} value={draft.decision} onPick={(v) => setD({ decision: v })} />
              <PillRow label={t("qtype")} options={QTYPES} value={draft.qtype} onPick={(v) => setD({ qtype: v })} />
              <PillRow label={t("rcode")} options={RCODES} value={draft.rcode} onPick={(v) => setD({ rcode: v })} />
            </>
          ) : (
            <PillRow label={t("status")} options={PROXY_STATUSES} value={draft.status} onPick={(v) => setD({ status: v })} />
          )}
        </div>
        <div className="activef">
          <span className="lbl">{t("activeLbl")}</span>
          {chips.map((c) => (
            <FilterChip key={c.k} label={chipLabel(c, t)} onRemove={() => removeChip(c.k)} />
          ))}
          {rows !== null && (
            <span className="counts">{t("shown")} <b>{visible.length}</b></span>
          )}
        </div>
      </form>

      {histo && (
        <div className="card">
          <h2>{t("histTitle")}</h2>
          <div className="histo" role="img" aria-label={t("histTitle")}>
            {histo.heights.map((h, i) => (
              <div key={i} className={i === histo.hot ? "hot" : undefined} style={{ height: `${h}%` }} />
            ))}
          </div>
        </div>
      )}

      <div className="card">
        <div className="table-wrap">
          <table>
            <thead>
              {kind === "dns" ? (
                <tr>
                  <th>{t("time")}</th><th>{t("clientIp")}</th><th>{t("domain")}</th>
                  <th>{t("type")}</th><th>{t("rcode")}</th><th>{t("decision")}</th>
                </tr>
              ) : (
                <tr>
                  <th>{t("time")}</th><th>{t("clientIp")}</th><th>{t("sniTag")}</th>
                  <th>{t("bytes")}</th><th>{t("status")}</th><th>{t("dialCol")}</th>
                </tr>
              )}
            </thead>
            <tbody>
              {rows === null && !error && <SkeletonRows cols={cols} />}
              {rows !== null && !visible.length && <EmptyRow colSpan={cols}>{t("noData")}</EmptyRow>}
              {kind === "dns" && visible.map((row) => <tr key={row.id}>{dnsCells(row, t)}</tr>)}
              {kind === "proxy" && visible.map((row) => (
                <tr key={row.id} className={row.dial_error ? "err" : undefined}>{proxyCells(row, err)}</tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {next && !exhausted && (
        <div className="pager">
          <button type="button" className="btn ghost sm" onClick={loadMore}>{t("loadMore")}</button>
        </div>
      )}
    </>
  );
}
