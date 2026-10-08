import { useState } from "react";
import { useLoad } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { Err, SkeletonRows } from "../components/Bits";
import { useConfirm, useToast } from "../components/Toast";

export function Account() {
  const { t } = useI18n();
  const { data, loading, error, setError, reload } = useLoad("/api/sessions");
  const [msg, setMsg] = useState("");
  const [pwdError, setPwdError] = useState("");
  const [ask, confirmRow] = useConfirm();
  const toast = useToast();
  async function changePassword(event) {
    event.preventDefault();
    const form = new FormData(event.target);
    setPwdError("");
    setMsg("");
    if (form.get("next") !== form.get("confirm")) {
      setPwdError(t("passwordMismatch"));
      return;
    }
    try {
      await api("/api/password", { method: "POST", body: JSON.stringify({ current: form.get("current"), next: form.get("next") }) });
      event.target.reset();
      setMsg(t("passwordChanged"));
      reload();
    } catch (e) { setPwdError(e); }
  }
  async function revoke(s) {
    ask(t("confirmRevoke"), async () => {
      try {
        await api(`/api/sessions/${s.id}`, { method: "DELETE" });
        reload();
      } catch (e) { setError(e); }
    });
  }
  async function revokeOthers() {
    ask(t("confirmRevokeOthers"), async () => {
      try {
        const out = await api("/api/sessions/revoke-others", { method: "POST" });
        toast(t("revokedN", { n: out.revoked }));
        reload();
      } catch (e) { setError(e); }
    });
  }
  const rows = data || [];
  return (
    <>
      {confirmRow}
      {msg && <div className="banner ok">{msg}</div>}
      <section className="mod">
        <h2>{t("changePassword")}</h2>
        <Err text={pwdError} />
        <form onSubmit={changePassword}>
          <div className="grid">
            <div><label>{t("currentPassword")}</label><input name="current" type="password" autoComplete="current-password" required /></div>
            <div><label>{t("newPassword")}</label><input name="next" type="password" autoComplete="new-password" minLength="12" maxLength="72" required /></div>
            <div><label>{t("confirmPassword")}</label><input name="confirm" type="password" autoComplete="new-password" minLength="12" maxLength="72" required /></div>
          </div>
          <p className="empty">{t("passwordHint")}</p>
          <p><button type="submit">{t("save")}</button></p>
        </form>
      </section>
      <section className="mod">
        <div className="toolbar">
          <h2>{t("activeSessions")}</h2>
          {rows.length > 1 && <button type="button" className="ghost" onClick={revokeOthers}>{t("revokeOthers")}</button>}
        </div>
        <Err text={error} />
        <div className="table-wrap">
          <table>
            <thead><tr><th>{t("lastSeen")}</th><th>{t("signedIn")}</th><th>IP</th><th>{t("browser")}</th><th></th></tr></thead>
            <tbody>
              {loading && !data && <SkeletonRows cols={5} rows={3} />}
              {rows.map((s) => (
                <tr key={s.id}>
                  <td>{new Date(s.last_seen_at).toLocaleString()}</td>
                  <td>{new Date(s.created_at).toLocaleString()}</td>
                  <td className="mono">{s.ip || "—"}</td>
                  <td title={s.user_agent}>{s.user_agent.slice(0, 60) || "—"}</td>
                  <td>{s.current
                    ? <span className="empty">{t("thisDevice")}</span>
                    : <button type="button" className="ghost tiny" onClick={() => revoke(s)}>{t("revoke")}</button>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}
