import { useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { Err, SkeletonRows } from "../components/Bits";

// LogTable renders one kind's rows with a Load more cursor.
// props: kind, title, filters {decision|status}, applied {q, ip}, cols, header, rowCells(row, err) -> <td>[]
function LogTable({ kind, title, filters = {}, applied, cols, header, rowCells }) {
  const { t, err } = useI18n();
  const [rows, setRows] = useState(null);
  const [next, setNext] = useState(null);
  const [error, setError] = useState("");
  const key = JSON.stringify([kind, filters, applied]);
  async function fetchPage(cur, append) {
    const p = new URLSearchParams({ kind, q: applied.q, ip: applied.ip });
    for (const [k, v] of Object.entries(filters)) {
      if (v) p.set(k, v);
    }
    if (cur) { p.set("before", cur.at); p.set("before_id", cur.id); }
    try {
      const out = await api(`/api/logs?${p}`);
      setRows((r) => (append ? [...(r || []), ...out.rows] : out.rows));
      setNext(out.next);
      setError("");
    } catch (e) { setError(e); }
  }
  // Fetch the first page whenever the applied filter changes.
  const [appliedKey, setAppliedKey] = useState(key);
  if (appliedKey !== key) {
    setAppliedKey(key);
    setRows(null);
    setNext(null);
    fetchPage("", false);
  }
  return (
    <section className="mod">
      <h2>{title}</h2>
      <Err text={error} />
      <div className="table-wrap">
        <table>
          <thead><tr>{header.map((h) => <th key={h}>{h}</th>)}</tr></thead>
          <tbody>
            {rows === null && !error && <SkeletonRows cols={cols} />}
            {rows?.map((row) => <tr key={row.id}>{rowCells(row, err)}</tr>)}
          </tbody>
        </table>
      </div>
      {next && (
        <div className="pager">
          <button type="button" className="ghost tiny" onClick={() => fetchPage(next, true)}>{t("loadMore")}</button>
        </div>
      )}
    </section>
  );
}

export function Logs() {
  const { t } = useI18n();
  const [q, setQ] = useState("");
  const [ip, setIp] = useState("");
  const [decision, setDecision] = useState("");
  const [status, setStatus] = useState("");
  const [applied, setApplied] = useState({ q: "", ip: "", decision: "", status: "" });
  function submit(event) {
    event.preventDefault();
    setApplied({ q: q.trim(), ip: ip.trim(), decision, status });
  }
  return (
    <>
      <section className="mod">
        <h2>{t("filter")}</h2>
        <form onSubmit={submit}>
          <div className="grid">
            <div><label>{t("name")}</label><input value={q} onChange={(e) => setQ(e.target.value)} /></div>
            <div><label>{t("clientIp")}</label><input value={ip} onChange={(e) => setIp(e.target.value)} placeholder={t("ipPlaceholder")} /></div>
            <div>
              <label>{t("decision")}</label>
              <select value={decision} onChange={(e) => setDecision(e.target.value)}>
                <option value="">{t("filterAll")}</option>
                <option value="acl">acl</option>
                <option value="local">local</option>
                <option value="forward">forward</option>
                <option value="cached">cached</option>
                <option value="fail">fail</option>
                <option value="limited">limited</option>
              </select>
            </div>
            <div>
              <label>{t("status")}</label>
              <select value={status} onChange={(e) => setStatus(e.target.value)}>
                <option value="">{t("filterAll")}</option>
                <option value="ok">ok</option>
                <option value="refused">refused</option>
                <option value="acl">acl</option>
                <option value="limited">limited</option>
                <option value="dial_error">dial_error</option>
                <option value="overload">overload</option>
              </select>
            </div>
          </div>
          <p><button type="submit">{t("show")}</button></p>
        </form>
      </section>
      <LogTable
        kind="dns"
        title="DNS"
        filters={{ decision: applied.decision }}
        applied={{ q: applied.q, ip: applied.ip }}
        cols={6}
        header={[t("time"), t("clientIp"), t("name"), t("type"), t("code"), t("decision")]}
        rowCells={(row) => [
          <td key="t">{new Date(row.at).toLocaleString()}</td>,
          <td key="ip" className="mono">{row.client_ip}</td>,
          <td key="n" className="mono">{row.name}</td>,
          <td key="q">{row.qtype}</td>,
          <td key="r">{row.rcode}</td>,
          <td key="d">{row.decision}</td>,
        ]}
      />
      <LogTable
        kind="proxy"
        title={t("proxyTitle")}
        filters={{ status: applied.status }}
        applied={{ q: applied.q, ip: applied.ip }}
        cols={6}
        header={[t("time"), t("clientIp"), "SNI", t("bytes"), t("status"), "Dial"]}
        rowCells={(row, err) => [
          <td key="t">{new Date(row.at).toLocaleString()}</td>,
          <td key="ip" className="mono">{row.client_ip}</td>,
          <td key="s" className="mono">{row.sni}</td>,
          <td key="b">{row.bytes_up} / {row.bytes_down}</td>,
          <td key="st">{row.status}</td>,
          <td key="d">{row.dial_error ? err(row.dial_error) : ""}</td>,
        ]}
      />
    </>
  );
}
