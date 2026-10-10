import { useCallback, useEffect, useState } from "react";
import { useLoad, usePoll } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { EmptyRow, Err, SkeletonRows } from "../components/Bits";
import { aclShare, ago, fmtBytes, pushHist, shares, sparkPaths } from "../lib/util";

const POLL_QPS_MS = 800;
const POLL_BOARD_MS = 10000;
const HIST_N = 75;

function fmtMs(v) {
  if (v == null) return "—";
  return `${Math.round(v)} ms`;
}

function HeroSpark({ cls, label, value, values }) {
  const p = sparkPaths(values);
  return (
    <div className={`spark ${cls}`}>
      <span className="l">{label} <b>{value}</b></span>
      {p && (
        <svg viewBox="0 0 200 40" preserveAspectRatio="none" aria-hidden="true">
          <path className="fill" d={p.fill} />
          <path className="a" d={p.line} />
        </svg>
      )}
    </div>
  );
}

function Tile({ title, children }) {
  return (
    <div className="tile">
      <h3>{title}</h3>
      {children}
    </div>
  );
}

function NodeTileRow({ n, metric, pct }) {
  const { t } = useI18n();
  const dim = !n.enabled || !n.fresh;
  const offline = n.last_seen_at ? `${t("noLink")} · ${ago(n.last_seen_at, t)}` : t("noLink");
  return (
    <div className={`nrow${dim ? " dim" : ""}`}>
      <span className="dname"><span className={`dot${n.fresh ? "" : " off"}`} />{n.name}</span>
      <b className={dim ? "muted" : "ac"} title={!n.enabled ? t("nodeOff") : n.fresh ? t("inRotation") : offline}>{metric}</b>
      <div className="bar"><div style={{ width: `${pct}%` }} /></div>
    </div>
  );
}

export function Overview() {
  const { t } = useI18n();
  const [updatedAt, setUpdatedAt] = useState(null);
  const [agoSec, setAgoSec] = useState(0);
  const [hist, setHist] = useState({ qps: [], blocked: [], lat: [] });
  const load = useLoad("/api/overview");
  const series = useLoad("/api/overview/series?window=1h");
  const stats = useLoad("/api/stats?window=24h&limit=8");
  const tickLive = useCallback(async () => {
    await load.reload();
    setUpdatedAt(Date.now());
  }, [load.reload]);
  const tickBoard = useCallback(async () => {
    await Promise.all([series.reload(), stats.reload()]);
  }, [series.reload, stats.reload]);
  usePoll(tickLive, POLL_QPS_MS);
  usePoll(tickBoard, POLL_BOARD_MS);
  // Ticker keeps "updated N s ago" honest between reloads.
  useEffect(() => {
    const timer = setInterval(() => {
      if (updatedAt) setAgoSec(Math.round((Date.now() - updatedAt) / 1000));
    }, 1000);
    return () => clearInterval(timer);
  }, [updatedAt]);

  useEffect(() => {
    if (updatedAt == null || !load.data) return;
    const o = load.data.overview || {};
    const liveShare = o.qps ? (100 * (o.refused || 0) / o.qps) : 0;
    setHist((h) => ({
      qps: pushHist(h.qps, o.qps || 0, HIST_N),
      blocked: pushHist(h.blocked, liveShare, HIST_N),
      lat: o.latency_avg_ms == null ? h.lat : pushHist(h.lat, o.latency_avg_ms, HIST_N),
    }));
  }, [updatedAt]);
  if (!load.data) return <div className="mod"><Err text={load.error} />{!load.error && <SkeletonRows cols={5} />}</div>;

  const o = load.data.overview;
  const points = series.data?.points || [];
  const hourDns = points.reduce((a, p) => a + p.dns, 0);
  const hourAcl = points.reduce((a, p) => a + p.refused, 0);
  const share = aclShare(points);
  const nodes = load.data.nodes || [];
  const dnsNodes = nodes.filter((n) => n.role === "dns");
  const proxyNodes = nodes.filter((n) => n.role === "proxy");
  const dnsShare = shares(dnsNodes.map((n) => n.queries_24h || 0));
  const proxyShare = shares(proxyNodes.map((n) => n.bytes_24h || 0));
  const domains = stats.data?.domains || [];
  const domainShare = shares(domains.map((d) => d.queries));
  const num = (n) => Number(n).toLocaleString();
  return (
    <>
      <div className="hrow">
        <h1>{t("overview")}</h1>
        {updatedAt != null && (
          <span className="crumb">
            <b>{t("updatedAgo", { n: agoSec })}</b> · {t("autoEvery", { n: POLL_QPS_MS / 1000 })}
          </span>
        )}
      </div>

      <div className="hero ov">
        <div className="bigqps">
          <span className="v">{o.qps}</span>
          <span className="l">{t("qps")}</span>
        </div>
        <div className="vsep" />
        <HeroSpark cls="a" label={t("sparkDns")} value={`${o.qps} ${t("qpsShort")}`} values={hist.qps} />
        <div className="vsep" />
        <HeroSpark cls="d" label={t("blockedCol")} value={`${(o.qps ? (100 * (o.refused || 0) / o.qps) : 0).toFixed(1)}%`} values={hist.blocked} />
        <div className="vsep" />
        <HeroSpark cls="t" label={t("sparkLatency")} value={fmtMs(o.latency_avg_ms)} values={hist.lat} />
      </div>

      <div className="board">
        <Tile title={t("tileDns")}>
          <div className="row"><span>{t("queriesHour")}</span><b className="ac">{num(hourDns)}</b></div>
          <div className="row"><span>{t("aclHour")}</span><b style={{ color: "var(--danger)" }}>{num(hourAcl)}</b></div>
          <div className="row"><span>{t("aclHourShare")}</span><b>{series.data ? `${share.toFixed(1)}%` : "—"}</b></div>
          <div className="row"><span>{t("latAvg")}</span><b>{fmtMs(o.latency_avg_ms)}</b></div>
          <div className="row"><span>{t("latP95")}</span><b>{fmtMs(o.latency_p95_ms)}</b></div>
        </Tile>
        <Tile title={t("tileProxy")}>
          <div className="row"><span>{t("sessionsLive")}</span><b className="ac">{num(o.sessions)}</b></div>
          <div className="row"><span>{t("bytes24")}</span><b>{fmtBytes(o.bytes)}</b></div>
        </Tile>
        <Tile title={t("tileNodesDns")}>
          {dnsNodes.length
            ? dnsNodes.map((n, i) => (
              <NodeTileRow key={n.id} n={n} metric={`${dnsShare[i]}%`} pct={dnsShare[i]} />
            ))
            : <div className="row"><span className="muted">{t("noNodes")}</span></div>}
        </Tile>
        <Tile title={t("tileNodesProxy")}>
          {proxyNodes.length
            ? proxyNodes.map((n, i) => (
              <NodeTileRow key={n.id} n={n} metric={fmtBytes(n.bytes_24h || 0)} pct={proxyShare[i]} />
            ))
            : <div className="row"><span className="muted">{t("noNodes")}</span></div>}
        </Tile>
      </div>

      <div className="card">
        <h2>{t("topDomains")} <small>{t("fromStats")}</small></h2>
        <table>
          <thead>
            <tr>
              <th>{t("domain")}</th><th>{t("queries")}</th><th>{t("blockedCol")}</th><th>{t("shareCol")}</th>
            </tr>
          </thead>
          <tbody>
            {domains.length ? domains.map((d, i) => (
              <tr key={d.name}>
                <td>{d.name}</td>
                <td className="num">{num(d.queries)}</td>
                <td className="num" style={d.acl ? { color: "var(--danger)" } : undefined}>{num(d.acl)}</td>
                <td>
                  <span className="share">{domainShare[i]}%</span>
                  <div className="bar"><div style={{ width: `${domainShare[i]}%` }} /></div>
                </td>
              </tr>
            )) : <EmptyRow colSpan={4}>{t("noDomains")}</EmptyRow>}
          </tbody>
        </table>
      </div>
      <div className="hint">{t("ovHint")}</div>
      <Err text={load.error || series.error || stats.error} />
    </>
  );
}
