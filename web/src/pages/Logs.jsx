import { useState } from "react";
import { useLoad } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { Err, SkeletonRows } from "../components/Bits";

export function Logs() {
  const { t, err } = useI18n();
  const [q, setQ] = useState("");
  const [ip, setIp] = useState("");
  const [applied, setApplied] = useState({ q: "", ip: "" });
  const dns = useLoad(`/api/logs?kind=dns&q=${encodeURIComponent(applied.q)}&ip=${encodeURIComponent(applied.ip)}`);
  const proxy = useLoad(`/api/logs?kind=proxy&q=${encodeURIComponent(applied.q)}&ip=${encodeURIComponent(applied.ip)}`);
  function submit(event) {
    event.preventDefault();
    const next = { q: q.trim(), ip: ip.trim() };
    if (next.q === applied.q && next.ip === applied.ip) { dns.reload(); proxy.reload(); }
    else setApplied(next);
  }
  return (
    <>
      <section className="mod">
        <h2>{t("filter")}</h2>
        <form onSubmit={submit}>
          <div className="grid">
            <div><label>{t("name")}</label><input value={q} onChange={(e) => setQ(e.target.value)} /></div>
            <div><label>{t("clientIp")}</label><input value={ip} onChange={(e) => setIp(e.target.value)} placeholder={t("ipPlaceholder")} /></div>
          </div>
          <p><button type="submit">{t("show")}</button></p>
        </form>
      </section>
      <section className="mod">
        <h2>DNS</h2>
        <Err text={dns.error} />
        <div className="table-wrap">
          <table>
            <thead><tr><th>{t("time")}</th><th>{t("clientIp")}</th><th>{t("name")}</th><th>{t("type")}</th><th>{t("code")}</th><th>{t("decision")}</th></tr></thead>
            <tbody>
              {dns.loading && !dns.data && <SkeletonRows cols={6} />}
              {dns.data?.rows?.map((row) => (
                <tr key={row.id}>
                  <td>{new Date(row.at).toLocaleString()}</td>
                  <td className="mono">{row.client_ip}</td>
                  <td className="mono">{row.name}</td>
                  <td>{row.qtype}</td>
                  <td>{row.rcode}</td>
                  <td>{row.decision}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
      <section className="mod">
        <h2>{t("proxyTitle")}</h2>
        <p className="empty">{t("noBody")}</p>
        <Err text={proxy.error} />
        <div className="table-wrap">
          <table>
            <thead><tr><th>{t("time")}</th><th>{t("clientIp")}</th><th>SNI</th><th>{t("bytes")}</th><th>{t("status")}</th><th>Dial</th></tr></thead>
            <tbody>
              {proxy.loading && !proxy.data && <SkeletonRows cols={6} />}
              {proxy.data?.rows?.map((row) => (
                <tr key={row.id}>
                  <td>{new Date(row.at).toLocaleString()}</td>
                  <td className="mono">{row.client_ip}</td>
                  <td className="mono">{row.sni}</td>
                  <td>{row.bytes_up} / {row.bytes_down}</td>
                  <td>{row.status}</td>
                  <td>{row.dial_error ? err(row.dial_error) : ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}
