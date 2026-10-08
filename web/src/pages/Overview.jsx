import { useCallback } from "react";
import { useLoad, usePoll } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { Err, StatusLamp, SkeletonRows, EmptyRow } from "../components/Bits";
import { Sparkline } from "../components/Sparkline";

function ipOf(n) {
  if (n.public_ipv4 && n.public_ipv6) return `${n.public_ipv4} / ${n.public_ipv6}`;
  return n.public_ipv4 || n.public_ipv6 || "—";
}

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

export function NodeTable({ nodes, cols = 5 }) {
  const { t, err } = useI18n();
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th>{t("name")}</th><th>{t("role")}</th><th>{t("publicIp")}</th><th>{t("agent")}</th>
            {cols > 4 && <th>{t("error")}</th>}
          </tr>
        </thead>
        <tbody>
          {nodes?.length ? nodes.map((n) => (
            <tr key={n.id}>
              <td><StatusLamp on={n.fresh} />{n.name}</td>
              <td>{n.role}</td>
              <td>{ipOf(n)}</td>
              <td className="mono">{n.agent_host}:{n.agent_port}</td>
              {cols > 4 && <td>{n.last_error ? err(n.last_error) : "—"}</td>}
            </tr>
          )) : <EmptyRow colSpan={cols}>{t("noNodes")}</EmptyRow>}
        </tbody>
      </table>
    </div>
  );
}

export function Overview() {
  const { t } = useI18n();
  const load = useLoad("/api/overview");
  const { data, error } = load;
  const series = useLoad("/api/overview/series?window=1h");
  const reload = useCallback(() => { load.reload(); series.reload(); }, [load.reload, series.reload]);
  usePoll(reload, 10000);
  if (!data) return <div className="mod"><Err text={error} />{!error && <SkeletonRows cols={5} />}</div>;
  const o = data.overview;
  const points = series.data?.points || [];
  return (
    <>
      <section className="meters">
        <div className="meter"><i>{t("qps")}</i><b>{o.qps}</b></div>
        <div className="meter"><i>{t("sessions60")}</i><b>{o.sessions}</b></div>
        <div className="meter"><i>{t("bytes24")}</i><b>{formatBytes(o.bytes)}</b></div>
        <div className="meter"><i>{t("acl60")}</i><b>{o.refused}</b></div>
        <div className="meter"><i>{t("domains")}</i><b>{o.rows?.length || 0}</b></div>
      </section>
      <section className="mod chart">
        <h2>{t("qps")}</h2>
        <Sparkline values={points.map((p) => p.dns)} />
        <h2 style={{ marginTop: 12 }}>{t("acl60")}</h2>
        <Sparkline values={points.map((p) => p.refused)} danger />
      </section>
      <section className="mod">
        <h2>{t("nodeCol")}</h2>
        <NodeTable nodes={data.nodes} />
      </section>
      <Err text={error} />
    </>
  );
}
