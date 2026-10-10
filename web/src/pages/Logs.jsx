import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { EmptyRow, Err, SkeletonRows } from "../components/Bits";
import {
  activeFilters,
  fqdnCheck,
  histoBuckets,
  maskDate,
  matchRow,
  mergeById,
  normDomainInput,
  parseDay,
  sortRows,
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

function chipLabel(c, t) {
  const name = CHIP_KEYS[c.k] || c.k;
  if (c.k === "time") {
    const value = /^\d{2}\.\d{2}\.\d{4}$/.test(c.v) ? `${c.v} — ${t("today")}` : t(`preset${c.v}`);
    return `${t("chipTime")}: ${value}`;
  }
  return `${t(name)}: ${c.v}`;
}

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
    <td key="l" className="mono">{row.latency_ms == null ? "—" : `${row.latency_ms} ms`}</td>,
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
  const [loadingMore, setLoadingMore] = useState(false);
  const [sort, setSort] = useState({ key: "at", dir: "desc" });
  const genRef = useRef(0);
  const moreRef = useRef(null);

  const setD = (patch) => setDraft((d) => ({ ...d, ...patch }));
  const appliedKey = JSON.stringify(applied);

  useEffect(() => {
    let live = true;
    const g = ++genRef.current;
    moreRef.current = null;
    setLoadingMore(false);
    setRows(null);
    api(`/api/logs?${logsParams(kind, applied, null)}`)
      .then((out) => {
        if (!live || g !== genRef.current) return;
        setRows(out.rows);
        setNext(out.next || null);
        setError("");
      })
      .catch((e) => {
        if (live && g === genRef.current) setError(e);
      });
    return () => {
      live = false;
      genRef.current += 1;
    };
  }, [kind, appliedKey]);

  async function loadMore() {
    if (moreRef.current !== null || !next) return;
    const g = genRef.current;
    moreRef.current = g;
    setLoadingMore(true);
    try {
      const out = await api(`/api/logs?${logsParams(kind, applied, next)}`);
      if (g !== genRef.current) return;
      setRows((r) => mergeById(r, out.rows));
      setNext(out.next || null);
      setError("");
    } catch (e) {
      if (g === genRef.current) setError(e);
    } finally {
      if (moreRef.current === g) moreRef.current = null;
      if (g === genRef.current) setLoadingMore(false);
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

  function removeChip(k) {
    const nextA = k === "time" ? { ...applied, preset: "", day: "" } : { ...applied, [k]: "" };
    setApplied(nextA);
    setDraft(nextA);
  }

  function clickSort(key) {
    setSort((s) => (s.key === key ? { key, dir: s.dir === "desc" ? "asc" : "desc" } : { key, dir: key === "at" ? "desc" : "asc" }));
  }
  function sortMark(key) {
    if (sort.key !== key) return "";
    return sort.dir === "desc" ? " ▾" : " ▴";
  }

  const range = timeRange(applied);
  const visible = sortRows(
    (rows || []).filter((r) =>
      matchRow(r, kind === "dns" ? applied.qtype : "", kind === "dns" ? applied.rcode : "", range)),
    sort.key,
    sort.dir,
  );
  const chips = activeFilters(kind, applied);
  const histo = visible.length ? histoBuckets(visible.map((r) => r.at)) : null;
  const chk = fqdnCheck(draft.q);
  const dayValid = !draft.day || parseDay(draft.day).ok;
  const exhausted = Boolean(range && rows && rows.length && new Date(rows[rows.length - 1].at) < range.start);
  const cols = kind === "dns" ? 7 : 6;

  return (
    <>
      <div className="hrow">
        <h1>{t("logs")}</h1>
      </div>

      <form className="fbar" onSubmit={apply}>
        <div className="row1">
          <span className="sel" role="group" aria-label={t("logs")}>
            <button type="button" className={kind === "dns" ? "on" : ""} aria-pressed={kind === "dns"} onClick={() => { setKind("dns"); setSort({ key: "at", dir: "desc" }); }}>{t("kindDns")}</button>
            <button type="button" className={kind === "proxy" ? "on" : ""} aria-pressed={kind === "proxy"} onClick={() => { setKind("proxy"); setSort({ key: "at", dir: "desc" }); }}>{t("proxyTitle")}</button>
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
          <button type="submit" className="go" disabled={!dayValid}>{t("apply")}</button>
          <button type="button" className="go ghost" onClick={reset}>{t("reset")}</button>
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
          {rows !== null && (next
            ? <span className="counts">{t("loadedCount")} <b>{visible.length}</b> / {rows.length}</span>
            : <span className="counts">{t("shown")} <b>{visible.length}</b></span>)}
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
                  <th className="th-sort" onClick={() => clickSort("at")}>{t("time")}{sortMark("at")}</th>
                  <th className="th-sort" onClick={() => clickSort("client_ip")}>{t("clientIp")}{sortMark("client_ip")}</th>
                  <th className="th-sort" onClick={() => clickSort("name")}>{t("domain")}{sortMark("name")}</th>
                  <th className="th-sort" onClick={() => clickSort("qtype")}>{t("type")}{sortMark("qtype")}</th>
                  <th className="th-sort" onClick={() => clickSort("rcode")}>{t("rcode")}{sortMark("rcode")}</th>
                  <th className="th-sort" onClick={() => clickSort("decision")}>{t("decision")}{sortMark("decision")}</th>
                  <th className="th-sort" onClick={() => clickSort("latency_ms")}>{t("latencyCol")}{sortMark("latency_ms")}</th>
                </tr>
              ) : (
                <tr>
                  <th className="th-sort" onClick={() => clickSort("at")}>{t("time")}{sortMark("at")}</th>
                  <th className="th-sort" onClick={() => clickSort("client_ip")}>{t("clientIp")}{sortMark("client_ip")}</th>
                  <th className="th-sort" onClick={() => clickSort("sni")}>{t("sniTag")}{sortMark("sni")}</th>
                  <th className="th-sort" onClick={() => clickSort("bytes_down")}>{t("bytes")}{sortMark("bytes_down")}</th>
                  <th className="th-sort" onClick={() => clickSort("status")}>{t("status")}{sortMark("status")}</th>
                  <th>{t("dialCol")}</th>
                </tr>
              )}
            </thead>
            <tbody>
              {rows === null && !error && <SkeletonRows cols={cols} />}
              {rows !== null && !visible.length && (
                <EmptyRow colSpan={cols}>{next && !exhausted ? t("noMatchLoaded") : t("noMatch")}</EmptyRow>
              )}
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
          <button type="button" className="btn ghost sm" disabled={loadingMore} onClick={loadMore}>{t("loadMore")}</button>
        </div>
      )}
    </>
  );
}
