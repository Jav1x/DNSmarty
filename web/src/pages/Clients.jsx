import { useEffect, useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { Err, StatusLamp, SkeletonRows } from "../components/Bits";
import { useConfirm } from "../components/Toast";

export function Clients() {
  const { t } = useI18n();
  const [data, setData] = useState(null);
  const [error, setError] = useState("");
  const [ask, confirmRow] = useConfirm();
  async function reload() {
    try {
      setData(await api("/api/clients"));
      setError("");
    } catch (e) { setError(e); }
  }
  useEffect(() => { reload(); }, []);
  async function create(kind, event) {
    event.preventDefault();
    const form = new FormData(event.target);
    try {
      await api("/api/clients", { method: "POST", body: JSON.stringify({ cidr: form.get("cidr"), label: form.get("label"), list_kind: kind, enabled: form.get("enabled") === "on" }) });
      event.target.reset();
      reload();
    } catch (err) { setError(err); }
  }
  async function remove(c) {
    ask(t("confirmDelete", { name: c.cidr }), async () => {
      try {
        await api(`/api/clients/${c.id}`, { method: "DELETE" });
        reload();
      } catch (err) { setError(err); }
    });
  }
  const rows = data || [];
  return (
    <>
      {confirmRow}
      <Err text={error} />
      <p className="empty">{t("allowHint")}</p>
      <div className="clients">
        {["allow", "deny"].map((kind) => (
          <section className="mod" key={kind}>
            <h2>{kind === "allow" ? t("whitelist") : t("blacklist")}</h2>
            <form onSubmit={(event) => create(kind, event)}>
              <div className="grid">
                <div><label>CIDR</label><input name="cidr" placeholder="198.51.100.10/32" required /></div>
                <div><label>{t("label")}</label><input name="label" /></div>
              </div>
              <p><label className="check"><input type="checkbox" name="enabled" defaultChecked />{t("enabledFem")}</label></p>
              <p><button type="submit">{t("add")}</button></p>
            </form>
            <div className="table-wrap">
              <table>
                <tbody>
                  {data === null && <SkeletonRows cols={4} rows={3} />}
                  {data !== null && rows.filter((c) => (c.list_kind || "allow") === kind).map((c) => (
                    <tr key={c.id}>
                      <td className="mono">{c.cidr}</td>
                      <td>{c.label}</td>
                      <td><StatusLamp on={c.enabled} /></td>
                      <td><button type="button" className="ghost tiny" onClick={() => remove(c)}>{t("delete")}</button></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        ))}
      </div>
    </>
  );
}
