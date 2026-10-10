// Обзор (лаба 16): hero-банд (живой QPS + спарклайны DNS / заблокировано)
// над доской 2×2 (DNS · сейчас, Прокси · сейчас, Ноды · DNS, Ноды · Прокси)
// и «Топ доменов» из существующего GET /api/stats (window=24h).
//
// Контракт (проверен по internal/panel/server.go — все три маршрута GET):
//   GET /api/overview          → { overview:{qps,refused,sessions,bytes,…}, nodes }
//   GET /api/overview/series?window=1h → { step_sec, points:[{t,dns,refused,…}] }
//   GET /api/stats?window=24h&limit=8  → { domains:[{name,queries,acl,clients}], … }
// «Заблокировано» — доля decision='acl': в series refused считается как
// count(*) FILTER (WHERE decision = 'acl') (traffic.go), сумма точек окна — доля.
// «Время ответа» не рендерится (данных нет до фазы 2), доли/трафик нод — тоже
// (в контракте ноды этих полей нет; см. отчёт задачи 9).
//
// Спарклайны живые: опрос usePoll(10 s) толкает значения в скользящий буфер
// pushHist (последние ~30 точек, ≈5 минут); usePoll/useEffect чистят таймеры.
import { useCallback, useEffect, useState } from "react";
import { useLoad, usePoll } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { EmptyRow, Err, SkeletonRows } from "../components/Bits";
import { aclShare, ago, fmtBytes, pushHist, shares, sparkPaths } from "../lib/util";

const POLL_MS = 10000;
const HIST_N = 30;

// Спарклайновая ячейка hero-банда: подпись + svg из d-строк (lab16: path.fill/path.a).
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

// Строка ноды в тайле: точка живости + имя, справа статус из существующих полей
// (fresh/enabled/last_seen_at) — долей и трафика по нодам в контракте нет.
function NodeTileRow({ n }) {
  const { t } = useI18n();
  const offline = n.last_seen_at ? `${t("noLink")} · ${ago(n.last_seen_at, t)}` : t("noLink");
  const status = !n.enabled ? t("nodeOff") : n.fresh ? t("inRotation") : offline;
  return (
    <div className="row">
      <span className="dname"><span className={`dot${n.fresh ? "" : " off"}`} />{n.name}</span>
      <b className={n.fresh && n.enabled ? "ac" : "muted"}>{status}</b>
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
  const reload = useCallback(async () => {
    await Promise.all([load.reload(), series.reload(), stats.reload()]);
    setUpdatedAt(Date.now());
  }, [load.reload, series.reload, stats.reload]);
  usePoll(reload, POLL_MS);
  // Ticker keeps "updated N s ago" honest between reloads.
  useEffect(() => {
    const timer = setInterval(() => {
      if (updatedAt) setAgoSec(Math.round((Date.now() - updatedAt) / 1000));
    }, 1000);
    return () => clearInterval(timer);
  }, [updatedAt]);
  // Скользящая история спарклайнов: одна точка на опрос (N≈30). Триггер —
  // updatedAt, он ставится после Promise.all, значит load/series уже свежие.
  useEffect(() => {
    if (updatedAt == null || !load.data) return;
    const share = series.data ? aclShare(series.data.points || []) : null;
    setHist((h) => ({
      qps: pushHist(h.qps, load.data.overview?.qps || 0, HIST_N),
      blocked: share === null ? h.blocked : pushHist(h.blocked, share, HIST_N),
      lat: load.data.overview?.latency_avg_ms == null ? h.lat : pushHist(h.lat, load.data.overview.latency_avg_ms, HIST_N),
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
  const domains = stats.data?.domains || [];
  const domainShare = shares(domains.map((d) => d.queries));
  const num = (n) => Number(n).toLocaleString();
  return (
    <>
      <div className="hrow">
        <h1>{t("overview")}</h1>
        {updatedAt != null && (
          <span className="crumb">
            <b>{t("updatedAgo", { n: agoSec })}</b> · {t("autoEvery", { n: POLL_MS / 1000 })}
          </span>
        )}
      </div>

      {/* приборная полоса: живой QPS + спарклайны */}
      <div className="hero ov no-rt">
        <div className="bigqps">
          <span className="v">{o.qps}</span>
          <span className="l">{t("qps")}</span>
        </div>
        <div className="vsep" />
        <HeroSpark cls="a" label={t("sparkDns")} value={`${o.qps} ${t("qpsShort")}`} values={hist.qps} />
        <div className="vsep" />
        <HeroSpark cls="d" label={t("blockedCol")} value={series.data ? `${share.toFixed(1)}%` : "—"} values={hist.blocked} />
        <div className="vsep" />
        <HeroSpark cls="t" label={t("sparkLatency")} value={o.latency_avg_ms == null ? "—" : `${Math.round(o.latency_avg_ms)} ms`} values={hist.lat} />
      </div>

      {/* доска */}
      <div className="board">
        <Tile title={t("tileDns")}>
          <div className="row"><span>{t("queriesHour")}</span><b className="ac">{num(hourDns)}</b></div>
          <div className="row"><span>{t("aclHour")}</span><b style={{ color: "var(--danger)" }}>{num(hourAcl)}</b></div>
          <div className="row"><span>{t("aclHourShare")}</span><b>{series.data ? `${share.toFixed(1)}%` : "—"}</b></div>
        </Tile>
        <Tile title={t("tileProxy")}>
          <div className="row"><span>{t("sessionsLive")}</span><b className="ac">{num(o.sessions)}</b></div>
          <div className="row"><span>{t("bytes24")}</span><b>{fmtBytes(o.bytes)}</b></div>
        </Tile>
        <Tile title={t("tileNodesDns")}>
          {dnsNodes.length
            ? dnsNodes.map((n) => <NodeTileRow key={n.id} n={n} />)
            : <div className="row"><span className="muted">{t("noNodes")}</span></div>}
        </Tile>
        <Tile title={t("tileNodesProxy")}>
          {proxyNodes.length
            ? proxyNodes.map((n) => <NodeTileRow key={n.id} n={n} />)
            : <div className="row"><span className="muted">{t("noNodes")}</span></div>}
        </Tile>
      </div>

      {/* топ доменов · 24 часа (переименование блока — фаза 2) */}
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
