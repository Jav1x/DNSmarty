import { useLoad } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { Err, SkeletonRows } from "../components/Bits";
import { useConfirm, useToast } from "../components/Toast";

export function Settings() {
  const { t, err } = useI18n();
  const { data, loading, error, setError, reload } = useLoad("/api/settings");
  const [ask, confirmRow] = useConfirm();
  const toast = useToast();
  if (loading && !data) return <div className="mod"><Err text={error} />{!error && <SkeletonRows cols={4} />}</div>;
  if (!data) return <Err text={error} />;
  const s = data.settings;
  async function save(event) {
    event.preventDefault();
    const form = new FormData(event.target);
    const body = {};
    for (const [k, v] of form.entries()) body[k] = Number.isNaN(Number(v)) || v === "" || k === "bootstrap_cidr" || k === "agent_image" ? v : Number(v);
    try {
      await api("/api/settings", { method: "POST", body: JSON.stringify(body) });
      toast(t("saved"));
      reload();
    } catch (e) { setError(e.message); }
  }
  async function addUpstream(event) {
    event.preventDefault();
    try {
      await api("/api/upstreams", { method: "POST", body: JSON.stringify({ addr: new FormData(event.target).get("addr") }) });
      event.target.reset();
      reload();
    } catch (e) { setError(e.message); }
  }
  async function saveUpstream(u, event) {
    event.preventDefault();
    const form = new FormData(event.target);
    try {
      await api(`/api/upstreams/${u.id}`, { method: "POST", body: JSON.stringify({ addr: form.get("addr"), ordinal: Number(form.get("ordinal")) }) });
      reload();
    } catch (e) { setError(e.message); }
  }
  async function removeUpstream(u) {
    ask(t("confirmDelete", { name: u.addr }), async () => {
      try {
        await api(`/api/upstreams/${u.id}`, { method: "DELETE" });
        reload();
      } catch (e) { setError(e.message); }
    });
  }
  return (
    <>
      {confirmRow}
      <Err text={error} />
      <section className="mod">
        <h2>{t("params")}</h2>
        <form onSubmit={save}>
          <div className="grid">
            <div><label>{t("ttl")}</label><input name="ttl" type="number" min="1" max="300" defaultValue={s.ttl} /></div>
            <div><label>{t("pushInterval")}</label><input name="pull_interval_sec" type="number" min="2" max="15" defaultValue={s.pull_interval_sec} /></div>
            <div><label>{t("retention")}</label><input name="retention_days" type="number" min="1" max="30" defaultValue={s.retention_days} /></div>
            <div><label>Bootstrap CIDR</label><input name="bootstrap_cidr" defaultValue={s.bootstrap_cidr} /></div>
            <div><label>{t("sessionsPerIp")}</label><input name="session_limit" type="number" min="1" max="10000" defaultValue={s.session_limit} /></div>
            <div><label>{t("dialMs")}</label><input name="dial_timeout_ms" type="number" min="100" max="60000" defaultValue={s.dial_timeout_ms} /></div>
            <div><label>{t("idleMs")}</label><input name="idle_timeout_ms" type="number" min="1000" max="600000" defaultValue={s.idle_timeout_ms} /></div>
            <div><label>{t("dnsRate")}</label><input name="dns_rate_qps" type="number" min="0" max="100000" defaultValue={s.dns_rate_qps} /></div>
            <div><label>{t("auditRetention")}</label><input name="audit_retention_days" type="number" min="7" max="3650" defaultValue={s.audit_retention_days} /></div>
            <div><label>{t("agentImage")}</label><input name="agent_image" defaultValue={s.agent_image} /></div>
          </div>
          <p><button type="submit">{t("save")}</button></p>
        </form>
      </section>
      <section className="mod">
        <h2>Upstream</h2>
        <p className="empty">{t("upstreamHint")}</p>
        {!data.upstreams?.length && <p className="banner note">{t("upstreamEmpty")}</p>}
        <form onSubmit={addUpstream}>
          <div className="grid"><div><label>{t("address")}</label><input name="addr" placeholder="1.1.1.1:53" required /></div></div>
          <p><button type="submit">{t("add")}</button></p>
        </form>
        {data.upstreams.map((u) => (
          <form key={u.id} onSubmit={(e) => saveUpstream(u, e)}>
            <div className="grid">
              <div><label>{t("address")}</label><input name="addr" defaultValue={u.addr} /></div>
              <div><label>{t("order")}</label><input name="ordinal" type="number" defaultValue={u.ordinal} /></div>
            </div>
            <div className="row-actions">
              <button type="submit">{t("save")}</button>
              <button className="alarm" type="button" onClick={() => removeUpstream(u)}>{t("delete")}</button>
            </div>
          </form>
        ))}
      </section>
    </>
  );
}
