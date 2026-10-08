import { useEffect, useState } from "react";
import { useLoad, usePoll } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { Err, EmptyRow, SkeletonRows } from "../components/Bits";

// The window buttons mirror the backend: 1h/6h/24h windows and "all" (0, retention-bound).
const WINDOWS = [
  ["1h", "window1h"],
  ["6h", "window6h"],
  ["24h", "window24h"],
  ["all", "windowAll"],
];

function formatBytes(n) {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB"];
  let v = n;
  for (const u of units) {
    v /= 1024;
    if (v < 1024) return `${v.toFixed(1)} ${u}`;
  }
  return `${(v / 1024).toFixed(1)} TB`;
}

function WindowSelector({ value, onPick }) {
  const { t } = useI18n();
  return (
    <span className="lang" role="group" aria-label={t("window")}>
      {WINDOWS.map(([key, label]) => (
        <button
          key={key}
          type="button"
          className={value === key ? "on" : ""}
          onClick={() => onPick(key)}
          aria-pressed={value === key}
        >{t(label)}</button>
      ))}
    </span>
  );
}

function StatTable({ cols, header, rows, empty, onRowClick, cells }) {
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>{header.map((h, i) => <th key={i}>{h}</th>)}</tr>
        </thead>
        <tbody>
          {rows === null && <SkeletonRows cols={cols} />}
          {rows !== null && !rows.length && <EmptyRow colSpan={cols}>{empty}</EmptyRow>}
          {rows?.map((r, i) => (
            <tr key={i} className={onRowClick ? "click" : ""} onClick={onRowClick ? () => onRowClick(r) : undefined}>
              {cells(r).map((cell, j) => <td key={j} className={cell.mono ? "mono" : ""}>{cell.v}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// Drill-down into one client's domains. Reloads on every ip/window change.
// Mounted only with a non-empty ip, so useLoad always gets a valid path.
export function ClientDomains({ ip, window: win, onClose }) {
  const { t } = useI18n();
  const load = useLoad(`/api/stats/client?ip=${encodeURIComponent(ip)}&window=${win}`);
  const { data, error } = load;
  const rows = data?.rows || null;
  return (
    <section className="mod">
      <div className="toolbar">
        <h2>{t("clientDomains", { ip })}</h2>
        <button type="button" className="ghost tiny" onClick={onClose}>{t("close")}</button>
      </div>
      {error && <Err text={error} />}
      <StatTable
        cols={3}
        header={[t("domain"), t("queries"), t("aclCount")]}
        rows={rows}
        empty={t("noData")}
        cells={(r) => [{ v: r.name, mono: true }, { v: r.queries }, { v: r.acl }]}
      />
    </section>
  );
}

export function Stats() {
  const { t } = useI18n();
  const [win, setWin] = useState("24h");
  const [picked, setPicked] = useState("");
  // The drill-down opens on click; the input commits the typed ip on submit.
  const [selected, setSelected] = useState("");
  const [updatedAt, setUpdatedAt] = useState(null);
  const load = useLoad(`/api/stats?window=${win}`);
  const { data, error, reload } = load;
  usePoll(reload, 30000);
  useEffect(() => { setUpdatedAt(Date.now()); }, [data]);

  const pick = (key) => { setWin(key); setPicked(""); };
  const openClient = (ip) => { setSelected(ip); setPicked(ip); };
  const domains = data?.domains || null;
  const clients = data?.clients || null;
  const proxy = data?.proxy || null;
  return (
    <>
      <div className="toolbar">
        <WindowSelector value={win} onPick={pick} />
        <form
          onSubmit={(e) => { e.preventDefault(); openClient(picked.trim()); }}
          style={{ display: "flex", gap: 8 }}
        >
          <input
            name="ip"
            placeholder={t("clientIp")}
            aria-label={t("clientIp")}
            value={picked}
            onChange={(e) => setPicked(e.target.value)}
            style={{ maxWidth: 220 }}
          />
          <button type="submit" className="ghost">{t("show")}</button>
        </form>
      </div>
      <Err text={error} />
      {selected && <ClientDomains ip={selected} window={win} onClose={() => setSelected("")} />}
      <section className="mod">
        <div className="toolbar">
          <h2>{t("topDomains")}</h2>
          {updatedAt && <span className="count">{t("updatedAgo", { n: Math.round((Date.now() - updatedAt) / 1000) })}</span>}
        </div>
        <StatTable
          cols={4}
          header={[t("domain"), t("queries"), t("aclCount"), t("clientsCount")]}
          rows={domains}
          empty={t("noData")}
          cells={(r) => [{ v: r.name, mono: true }, { v: r.queries }, { v: r.acl }, { v: r.clients }]}
        />
      </section>
      <section className="mod">
        <h2>{t("proxyByDomain")}</h2>
        <StatTable
          cols={3}
          header={[t("domain"), t("sessionsCol"), t("bytesCol")]}
          rows={proxy}
          empty={t("noData")}
          cells={(r) => [{ v: r.name, mono: true }, { v: r.sessions }, { v: formatBytes(r.bytes) }]}
        />
      </section>
      <section className="mod">
        <h2>{t("clientsCol")}</h2>
        <StatTable
          cols={3}
          header={[t("clientIp"), t("queries"), t("aclCount")]}
          rows={clients}
          empty={t("noData")}
          onRowClick={(r) => openClient(r.ip)}
          cells={(r) => [{ v: r.ip, mono: true }, { v: r.queries }, { v: r.acl }]}
        />
      </section>
    </>
  );
}
