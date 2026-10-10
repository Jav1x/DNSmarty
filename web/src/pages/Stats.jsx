// Статистика (лаба 16): пилюли окон 1ч/6ч/24ч/всё, карточки «Топ доменов» и
// «Прокси · трафик по SNI» в сетке 1.25fr/1fr, «Клиенты» на всю ширину с
// drill-down «Домены клиента {ip}» (клик по строке или прямой ввод IP).
//
// Контракт (проверен по internal/panel/server.go и api.go, все GET):
//   GET /api/stats?window=1h|6h|24h|all → { window, domains:[{name,queries,acl,clients}],
//                                            clients:[{ip,queries,acl}], proxy:[{name,sessions,bytes}] }
//   GET /api/stats/client?ip=…&window=… → { rows:[{name,queries,acl}] }
// statsWindow (api.go) принимает именно эти ключи окна; "all" = 0 (весь retention).
// Ошибку неверного IP отдаёт сервер (400, field IP) — показываем через Err.
//
// usePoll(30 с) и тикер «N с назад» чистятся в cleanup своих эффектов;
// у ClientDrill таймеров нет — только useLoad, который перезагружается по path.
import { useEffect, useState } from "react";
import { useLoad, usePoll } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { EmptyRow, Err, SkeletonRows } from "../components/Bits";
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

// head: [{ label, sorted }]; rows: null — грузится (скелет), [] — пусто; cells(r, i): [{ v, cls }].
function DataTable({ head, rows, empty, onRowClick, cells }) {
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            {head.map((h) => (
              <th key={h.label}>
                {h.label}
                {h.sorted && <span className="ar"> ▾</span>}
              </th>
            ))}
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

// Домены одного клиента. Монтируется только с непустым ip; useLoad перезагружает
// по path, так что смена окна или клиента подтягивает свежие данные.
function ClientDrill({ ip, win, onClose }) {
  const { t } = useI18n();
  const { data, error } = useLoad(clientStatsPath(ip, win));
  const rows = data ? data.rows || [] : error ? [] : null;
  return (
    <div className="drill">
      <div className="dhead">
        <b>{t("clientDomains", { ip })}</b>
        <button type="button" className="btn ghost sm" onClick={onClose}>{t("close")}</button>
      </div>
      <Err text={error} />
      <DataTable
        head={[{ label: t("domain") }, { label: t("queries"), sorted: true }, { label: t("blockedCol") }]}
        rows={rows}
        empty={t("noData")}
        cells={(r) => [{ v: r.name }, { v: r.queries, cls: "num" }, { v: r.acl, cls: r.acl > 0 ? "hot" : undefined }]}
      />
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
  const load = useLoad(statsPath(win));
  const { data, error, reload } = load;
  usePoll(reload, POLL_MS);
  useEffect(() => {
    if (!data) return;
    setUpdatedAt(Date.now());
    setAgoSec(0);
  }, [data]);
  // Тикер держит «Обновлено N с назад» честным между опросами.
  useEffect(() => {
    if (updatedAt == null) return;
    const timer = setInterval(() => setAgoSec(Math.round((Date.now() - updatedAt) / 1000)), 1000);
    return () => clearInterval(timer);
  }, [updatedAt]);

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

  const domains = data ? data.domains || [] : error ? [] : null;
  const clients = data ? data.clients || [] : error ? [] : null;
  const proxy = data ? data.proxy || [] : error ? [] : null;
  const clientShare = shares((clients || []).map((c) => c.queries));

  return (
    <>
      <div className="hrow">
        <h1>{t("stats")}</h1>
        <span className="toolbar">
          <WindowPills value={win} onPick={setWin} />
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

      <div className="stats-grid">
        <div className="card">
          <h2>{t("topDomains")} <small>{t("byQueries")}</small></h2>
          <DataTable
            head={[{ label: t("domain") }, { label: t("queries"), sorted: true }, { label: t("blockedCol") }, { label: t("clientsCount") }]}
            rows={domains}
            empty={t("noData")}
            cells={(r) => [
              { v: r.name },
              { v: r.queries, cls: "num" },
              { v: r.acl, cls: r.acl > 0 ? "hot" : undefined },
              { v: r.clients },
            ]}
          />
        </div>
        <div className="card">
          <h2>{t("proxyByDomain")} <small>{t("sniTag")}</small></h2>
          <DataTable
            head={[{ label: t("domain") }, { label: t("sessionsCol") }, { label: t("bytesCol"), sorted: true }]}
            rows={proxy}
            empty={t("noData")}
            cells={(r) => [{ v: r.name }, { v: r.sessions }, { v: fmtBytes(r.bytes), cls: "num" }]}
          />
        </div>
        <div className="card full">
          <h2>{t("clientsCol")} <small>{t("clientsHint")}</small></h2>
          <DataTable
            head={[{ label: t("clientIp") }, { label: t("queries"), sorted: true }, { label: t("blockedCol") }, { label: t("shareCol") }]}
            rows={clients}
            empty={t("noData")}
            onRowClick={(r) => openClient(r.ip)}
            cells={(r, i) => {
              const pct = clientShare[i];
              return [
                { v: r.ip },
                { v: r.queries, cls: "num" },
                { v: r.acl, cls: r.acl > 0 ? "hot" : undefined },
                {
                  v: (
                    <>
                      <span className="share">{pct}%</span>
                      <div className="bar"><div style={{ width: `${pct}%` }} /></div>
                    </>
                  ),
                },
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
