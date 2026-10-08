import { useLoad } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { Err, SkeletonRows } from "../components/Bits";

export function Audit() {
  const { t } = useI18n();
  const { data, loading, error } = useLoad("/api/audit");
  const rows = data?.rows || [];
  return (
    <section className="mod">
      <h2>{t("auditTitle")}</h2>
      <Err text={error} />
      <div className="table-wrap">
        <table>
          <thead><tr><th>{t("time")}</th><th>{t("who")}</th><th>{t("action")}</th><th>{t("detail")}</th></tr></thead>
          <tbody>
            {loading && !data && <SkeletonRows cols={4} />}
            {rows.map((row) => (
              <tr key={row.id}>
                <td>{new Date(row.at).toLocaleString()}</td>
                <td>{row.actor}</td>
                <td className="mono">{row.action}</td>
                <td><code style={{ fontSize: 12 }}>{row.detail}</code></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
