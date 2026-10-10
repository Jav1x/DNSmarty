import { useEffect, useMemo, useState } from "react";
import { useLoad, usePoll } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { EmptyRow, Err, SkeletonRows } from "../components/Bits";
import { sortRows } from "../lib/logsfilters";
import { cleanClientIp, clientStatsPath, fmtBytes, shares, statsPath } from "../lib/util";

const POLL_MS = 30000;

const WINDOWS = [
  ["1h", "window1h"],
  ["6h", "window6h"],
  ["24h", "window24h"],
  ["all", "windowAll"],
];

function WindowPills({ value, onPick }) {
  const { t } = useI18n();
  return (
    <span className="win" role="group" aria-label={t("window")}>
      {WINDOWS.map(([key, label]) => (
        <button
          key={key}
          type="button"
          className={`pill${value === key ? " on" : ""}`}
          aria-pressed={value === key}
          onClick={() => onPick(key)}
        >{t(label)}</button>
      ))}
    </span>
  );
}

function SortTh({ label, k, sort, onSort, cls }) {
  const on = sort.key === k;
  return (
    <th className={`th-sort${cls ? ` ${cls}` : ""}`} onClick={() => onSort(k)} aria-sort={on ? (sort.dir === "desc" ? "descending" : "ascending") : "none"}>
      {label}{on ? (sort.dir === "desc" ? " ▾" : " ▴") : ""}
    </th>
  );
}

function shareMap(rows, key, idOf) {
  const pct = shares((rows || []).map((r) => Number(r[key] || 0)));
  const out = {};
  (rows || []).forEach((r, i) => { out[idOf(r)] = pct[i]; });
  return out;
}

function DataTable({ head, rows, empty, onRowClick, cells, sort, onSort }) {
  return (
    <div className="tscroll">
      <table>
        <thead>
          <tr>
            {head.map((h) => h.sort
              ? <SortTh key={h.k} label={h.label} k={h.k} sort={sort} onSort={onSort} cls={h.cls} />
              : <th key={h.k || h.label} className={h.cls}>{h.label}</th>)}
          </tr>
        </thead>
        <tbody>
          {rows === null && <SkeletonRows cols={head.length} />}
          {rows !== null && !rows.length && <EmptyRow colSpan={head.length}>{empty}</EmptyRow>}
          {rows?.map((r, i) => (
            <tr key={i} className={onRowClick ? "click" : undefined} onClick={onRowClick ? () => onRowClick(r) : undefined}>
              {cells(r, i).map((c, j) => <td key={j} className={c.cls}>{c.v}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ClientDrill({ ip, win, onClose }) {
  const { t } = useI18n();
  const { data, loading, error } = useLoad(clientStatsPath(ip, win));
  const rows = loading ? null : data ? data.rows || [] : error ? [] : null;
  const [sort, setSort] = useState({ key: "queries", dir: "desc" });
  const sorted = rows ? sortRows(rows, sort.key, sort.dir) : rows;
  const clickSort = (k) => setSort((s) => (s.key === k ? { key: k, dir: s.dir === "desc" ? "asc" : "desc" } : { key: k, dir: "desc" }));
  return (
    <div className="drill">
      <div className="dhead">
        <b>{t("clientDomains", { ip })}</b>
        <button type="button" className="btn ghost sm" onClick={onClose}>{t("close")}</button>
      </div>
      <Err text={error} />
      <DataTable
        head={[
          { label: t("domain"), k: "name", sort: true },
          { label: t("queries"), k: "queries", sort: true, cls: "num" },
          { label: t("blockedCol"), k: "acl", sort: true, cls: "num" },
        ]}
        rows={sorted}
        empty={t("noData")}
        sort={sort}
        onSort={clickSort}
        cells={(r) => [{ v: r.name }, { v: num(r.queries), cls: "num" }, { v: num(r.acl), cls: r.acl > 0 ? "num hot" : "num" }]}
      />
    </div>
  );
}

function num(n) {
  return Number(n || 0).toLocaleString();
}

function HeroCell({ label, value, sub, warn }) {
  return (
    <div className={`stcell${warn ? " warn" : ""}`}>
      <span className="v">{value}</span>
      <span className="l">{label}</span>
      {sub ? <span className="s">{sub}</span> : null}
    </div>
  );
}

export function Stats() {
  const { t } = useI18n();
  const [win, setWin] = useState("24h");
  const [draft, setDraft] = useState("");
  const [selected, setSelected] = useState("");
  const [ipMissing, setIpMissing] = useState(false);
  const [updatedAt, setUpdatedAt] = useState(null);
  const [agoSec, setAgoSec] = useState(0);
  const [domSort, setDomSort] = useState({ key: "queries", dir: "desc" });
  const [proxySort, setProxySort] = useState({ key: "bytes", dir: "desc" });
  const [cliSort, setCliSort] = useState({ key: "queries", dir: "desc" });
  const load = useLoad(statsPath(win));
  const { data, loading, error, reload } = load;
  usePoll(reload, POLL_MS);
  useEffect(() => {
    if (!data) return;
    setUpdatedAt(Date.now());
    setAgoSec(0);
  }, [data]);
  useEffect(() => {
    if (updatedAt == null) return;
    const timer = setInterval(() => setAgoSec(Math.round((Date.now() - updatedAt) / 1000)), 1000);
    return () => clearInterval(timer);
  }, [updatedAt]);

  const pickWindow = (w) => {
    setWin(w);
    setSelected("");
    setDomSort({ key: "queries", dir: "desc" });
    setProxySort({ key: "bytes", dir: "desc" });
    setCliSort({ key: "queries", dir: "desc" });
  };
  const openClient = (ip) => {
    setDraft(ip);
    setSelected(ip);
    setIpMissing(false);
  };
  const submitIp = (e) => {
    e.preventDefault();
    const ip = cleanClientIp(draft);
    if (!ip) {
      setIpMissing(true);
      return;
    }
    openClient(ip);
  };
  const clickSort = (set) => (k) => set((s) => (s.key === k ? { key: k, dir: s.dir === "desc" ? "asc" : "desc" } : { key: k, dir: "desc" }));

  const domains = loading ? null : data ? data.domains || [] : error ? [] : null;
  const clients = loading ? null : data ? data.clients || [] : error ? [] : null;
  const proxy = loading ? null : data ? data.proxy || [] : error ? [] : null;
  const totals = data?.totals || {};
  const q = Number(totals.queries || 0);
  const blocked = Number(totals.blocked || 0);
  const blockedPct = q ? Math.round((blocked / q) * 100) : 0;

  const domainShare = useMemo(() => shareMap(domains, "queries", (r) => r.name), [domains]);
  const proxyShare = useMemo(() => shareMap(proxy, "bytes", (r) => r.name), [proxy]);
  const clientShare = useMemo(() => shareMap(clients, "queries", (r) => r.ip), [clients]);
  const sortedDomains = domains ? sortRows(domains, domSort.key, domSort.dir) : domains;
  const sortedProxy = proxy ? sortRows(proxy, proxySort.key, proxySort.dir) : proxy;
  const sortedClients = clients ? sortRows(clients, cliSort.key, cliSort.dir) : clients;

  return (
    <>
      <div className="hrow">
        <h1>{t("stats")}</h1>
        <span className="toolbar">
          <WindowPills value={win} onPick={pickWindow} />
          <form className="toolbar" onSubmit={submitIp}>
            <input
              className="finput stats-ip"
              name="ip"
              placeholder={t("clientIp")}
              aria-label={t("clientIp")}
              value={draft}
              onChange={(e) => { setDraft(e.target.value); setIpMissing(false); }}
            />
            <button type="submit" className="btn ghost sm">{t("show")}</button>
          </form>
        </span>
        {updatedAt != null && (
          <span className="crumb"><b>{t("updatedAgo", { n: agoSec })}</b></span>
        )}
      </div>
      <Err text={error} />
      {ipMissing && <div className="banner err">{t("clientIpEmpty")}</div>}

      <div className="hero st">
        <HeroCell label={t("queries")} value={loading ? "…" : num(q)} />
        <div className="vsep" />
        <HeroCell
          label={t("totBlocked")}
          value={loading ? "…" : num(blocked)}
          sub={q ? `${blockedPct}% ${t("ofQueries")}` : null}
          warn={blocked > 0}
        />
        <div className="vsep" />
        <HeroCell label={t("totClients")} value={loading ? "…" : num(totals.clients)} />
        <div className="vsep" />
        <HeroCell
          label={t("totProxy")}
          value={loading ? "…" : fmtBytes(totals.bytes || 0)}
          sub={loading ? null : `${num(totals.sessions)} ${t("sessionsCol").toLowerCase()}`}
        />
      </div>

      <div className="stats-grid">
        <div className="tablecard">
          <h2>{t("topDomains")} <small>{t("byQueries")}</small></h2>
          <DataTable
            head={[
              { label: t("rankCol"), k: "rank", cls: "rank" },
              { label: t("domain"), k: "name", sort: true },
              { label: t("queries"), k: "queries", sort: true, cls: "num" },
              { label: t("blockedCol"), k: "acl", sort: true, cls: "num" },
              { label: t("clientsCount"), k: "clients", sort: true, cls: "num" },
              { label: t("shareCol"), k: "share" },
            ]}
            rows={sortedDomains}
            empty={t("noData")}
            sort={domSort}
            onSort={clickSort(setDomSort)}
            cells={(r, i) => {
              const pct = domainShare[r.name] || 0;
              return [
                { v: String(i + 1).padStart(2, "0"), cls: "rank" },
                { v: r.name },
                { v: num(r.queries), cls: "num" },
                { v: num(r.acl), cls: r.acl > 0 ? "num hot" : "num" },
                { v: num(r.clients), cls: "num" },
                { v: <><span className="share">{pct}%</span><div className="bar"><div style={{ width: `${pct}%` }} /></div></> },
              ];
            }}
          />
        </div>
        <div className="tablecard">
          <h2>{t("proxyByDomain")} <small>{t("sniTag")}</small></h2>
          <DataTable
            head={[
              { label: t("rankCol"), k: "rank", cls: "rank" },
              { label: t("domain"), k: "name", sort: true },
              { label: t("sessionsCol"), k: "sessions", sort: true, cls: "num" },
              { label: t("bytesCol"), k: "bytes", sort: true, cls: "num" },
              { label: t("shareCol"), k: "share" },
            ]}
            rows={sortedProxy}
            empty={t("noData")}
            sort={proxySort}
            onSort={clickSort(setProxySort)}
            cells={(r, i) => {
              const pct = proxyShare[r.name] || 0;
              return [
                { v: String(i + 1).padStart(2, "0"), cls: "rank" },
                { v: r.name },
                { v: num(r.sessions), cls: "num" },
                { v: fmtBytes(r.bytes), cls: "num" },
                { v: <><span className="share">{pct}%</span><div className="bar"><div style={{ width: `${pct}%` }} /></div></> },
              ];
            }}
          />
        </div>
        <div className="tablecard full">
          <h2>{t("clientsCol")} <small>{t("clientsHint")}</small></h2>
          <DataTable
            head={[
              { label: t("rankCol"), k: "rank", cls: "rank" },
              { label: t("clientIp"), k: "ip", sort: true },
              { label: t("queries"), k: "queries", sort: true, cls: "num" },
              { label: t("blockedCol"), k: "acl", sort: true, cls: "num" },
              { label: t("shareCol"), k: "share" },
            ]}
            rows={sortedClients}
            empty={t("noData")}
            onRowClick={(r) => openClient(r.ip)}
            sort={cliSort}
            onSort={clickSort(setCliSort)}
            cells={(r, i) => {
              const pct = clientShare[r.ip] || 0;
              return [
                { v: String(i + 1).padStart(2, "0"), cls: "rank" },
                { v: r.ip },
                { v: num(r.queries), cls: "num" },
                { v: num(r.acl), cls: r.acl > 0 ? "num hot" : "num" },
                { v: <><span className="share">{pct}%</span><div className="bar"><div style={{ width: `${pct}%` }} /></div></> },
              ];
            }}
          />
          {selected && <ClientDrill ip={selected} win={win} onClose={() => setSelected("")} />}
        </div>
      </div>
      <div className="hint">{t("statsHint")}</div>
    </>
  );
}
