import { useEffect, useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { Err, StatusLamp, SkeletonRows } from "../components/Bits";
import { Switch } from "../ui/Switch";
import { useConfirm, useToast } from "../components/Toast";

export function Clients() {
  const { t } = useI18n();
  const [data, setData] = useState(null);
  const [error, setError] = useState("");
  const [allowOn, setAllowOn] = useState(true);
  const [denyOn, setDenyOn] = useState(true);
  const [ask, confirmRow] = useConfirm();
  const toast = useToast();
  async function reload() {
    try {
      const out = await api("/api/clients");
      setData(out.rows);
      setAllowOn(out.allow_enabled);
      setDenyOn(out.deny_enabled);
      setError("");
    } catch (e) { setError(e); }
  }
  useEffect(() => { reload(); }, []);
  // Toggle sends both flags: the endpoint sets the pair in one transaction.
  async function toggleKinds(allowEnabled, denyEnabled) {
    const prev = { allowOn, denyOn };
    setAllowOn(allowEnabled);
    setDenyOn(denyEnabled);
    try {
      await api("/api/clients/kinds", { method: "POST", body: JSON.stringify({ allow_enabled: allowEnabled, deny_enabled: denyEnabled }) });
      toast(t("saved"));
    } catch (e) {
      setAllowOn(prev.allowOn);
      setDenyOn(prev.denyOn);
      setError(e);
    }
  }
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
        {[
          ["allow", t("whitelist"), allowOn, (v) => toggleKinds(v, denyOn)],
          ["deny", t("blacklist"), denyOn, (v) => toggleKinds(allowOn, v)],
        ].map(([kind, title, on, toggle]) => (
          <section className="mod" key={kind}>
            <div className="toolbar">
              <h2>{title}</h2>
              <Switch on={on} onChange={toggle} label={`${title}: ${on ? t("enabled") : t("listDisabled")}`} />
            </div>
            <div className={on ? "" : "block-off"} aria-disabled={!on}>
              <form onSubmit={(event) => create(kind, event)}>
                <div className="grid">
                  <div><label>CIDR</label><input name="cidr" placeholder="198.51.100.10/32" required disabled={!on} /></div>
                  <div><label>{t("label")}</label><input name="label" disabled={!on} /></div>
                </div>
                <p><label className="check"><input type="checkbox" name="enabled" defaultChecked disabled={!on} />{t("enabledFem")}</label></p>
                <p><button type="submit" disabled={!on}>{t("add")}</button></p>
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
                        <td><button type="button" className="ghost tiny" onClick={() => remove(c)} disabled={!on}>{t("delete")}</button></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </section>
        ))}
      </div>
    </>
  );
}
