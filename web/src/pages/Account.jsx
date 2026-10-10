// Аккаунт: профиль, 01 пароль, 02 двухфакторная (TOTP + резервные коды),
// 03 сессии, 04 недавняя активность. OAuth появится отдельным шагом и сдвинет номера.
//
// Контракт (проверен по internal/panel/api.go и store/admin.go):
//   GET  /api/me                       → { user, csrf, session_id, version } — роли в ответе нет;
//   POST /api/password                 → {current, next}; бэкенд завершает прочие
//                                        сессии (store.ChangePassword) — перечитываем список;
//   GET  /api/sessions                 → [{id, created_at, last_seen_at, expires_at,
//                                          ip, user_agent, current}];
//   POST /api/sessions/revoke-others   → {ok, revoked};
//   DELETE /api/sessions/{id}          → {ok};
//   GET  /api/audit                    → {rows:[{id, at, actor, action, detail}], next} —
//                                        фильтра по пользователю на сервере нет, отбираем
//                                        на клиенте (actor === user из /api/me).
import { useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import { useLoad } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { EmptyRow, Err, SkeletonRows } from "../components/Bits";
import { useConfirm, useToast } from "../components/Toast";
import { auditDetail } from "../lib/logsfilters";
import { ago, deviceIcon, pwStrength } from "../lib/util";

// Слова надёжности по сегментам (лаба 14): 1 слабый · 2 посредственный ·
// 3 хороший · 4 отличный; пустое поле — «минимум 12 символов».
const PW_WORDS = ["", "pwWeak", "pwFair", "pwGood", "pwGreat"];
const SHORT_DATE = { dateStyle: "short", timeStyle: "short" };

// Человеческое имя действия аудита; незнакомые действия — как есть (фаза 1
// знает login/login.fail/logout/node.check, см. logsfilters.auditGroup).
const ACT_KEYS = {
  "login": "actLogin",
  "login.fail": "actLoginFail",
  "logout": "actLogout",
  "node.check": "actNodeCheck",
  "session.revoke": "actSessionRevoke",
  "password.change": "actPasswordChange",
  "totp.enable": "actTotpEnable",
  "totp.disable": "actTotpDisable",
};

// Лампа-точка строки активности: вход — зелёная, отказ входа — красная,
// прочее — синяя (лаба 14).
function actDot(action) {
  if (action === "login") return "ok";
  if (action === "login.fail") return "bad";
  return "info";
}

export function Account() {
  const { t } = useI18n();
  const me = useLoad("/api/me");
  const { data, loading, error, setError, reload } = useLoad("/api/sessions");
  const audit = useLoad("/api/audit");
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [msg, setMsg] = useState("");
  const [pwdError, setPwdError] = useState("");
  const [ask, confirmRow] = useConfirm();
  const toast = useToast();
  const totp = useLoad("/api/account/2fa/codes");
  const [setup, setSetup] = useState(null);
  const [totpCode, setTotpCode] = useState("");
  const [totpError, setTotpError] = useState("");
  const [totpPass, setTotpPass] = useState("");

  const user = me.data?.user;
  const rows = data || [];
  // Аудит «по этому пользователю»: сервер фильтров не принимает — отбираем
  // по актёру на клиенте; последние 8 строк достаточно для лабовой секции.
  const activity = (audit.data?.rows || [])
    .filter((r) => user && r.actor === user)
    .slice(0, 8);

  async function changePassword(event) {
    event.preventDefault();
    setPwdError("");
    setMsg("");
    if (next !== confirm) {
      setPwdError(t("passwordMismatch"));
      return;
    }
    try {
      await api("/api/password", { method: "POST", body: JSON.stringify({ current, next }) });
      setCurrent("");
      setNext("");
      setConfirm("");
      setMsg(t("passwordChanged"));
      reload();
      audit.reload();
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

  async function startTotp() {
    setTotpError("");
    try {
      setSetup(await api("/api/account/2fa/setup", { method: "POST", body: "{}" }));
      setTotpCode("");
    } catch (e) { setTotpError(e); }
  }

  async function confirmTotp(event) {
    event.preventDefault();
    setTotpError("");
    try {
      await api("/api/account/2fa/enable", { method: "POST", body: JSON.stringify({ code: totpCode }) });
      setSetup(null);
      setTotpCode("");
      totp.reload();
      audit.reload();
    } catch (e) { setTotpError(e); }
  }

  async function disableTotp(event) {
    event.preventDefault();
    setTotpError("");
    try {
      await api("/api/account/2fa/disable", { method: "POST", body: JSON.stringify({ password: totpPass }) });
      setTotpPass("");
      totp.reload();
      audit.reload();
    } catch (e) { setTotpError(e); }
  }

  async function markCode(id) {
    setTotpError("");
    try {
      await api(`/api/account/2fa/codes/${id}`, { method: "POST", body: "{}" });
      totp.reload();
    } catch (e) { setTotpError(e); }
  }

  const score = pwStrength(next);
  const match = next === confirm;
  const totpOn = !!totp.data?.enabled;
  const backup = totp.data?.codes || [];

  return (
    <>
      {confirmRow}
      <div className="hrow">
        <h1>{t("account")}</h1>
        <span className="crumb">{t("accountSub")}</span>
      </div>

      {me.data && (
        <div className="hero acc">
          <span className="avatar" aria-hidden="true">{(me.data.user || "?").slice(0, 2).toUpperCase()}</span>
          <span className="who">
            <b>{me.data.user}</b>
            <span>{t("panelVer", { v: me.data.version || "—" })}</span>
          </span>
          <span className="hstats">
            <span className="hstat"><b>{rows.length}</b><span>{t("sessionsLive")}</span></span>
          </span>
        </div>
      )}

      {msg && <div className="banner ok">{msg}</div>}

      <section className="sect">
        <h2>
          <span className="wrapl"><span className="snum">01</span>{t("changePassword")}</span>
          <small>{t("passwordHint")}</small>
        </h2>
        <div className="sbody">
          <Err text={pwdError} />
          <form onSubmit={changePassword}>
            <div className="grid3 f">
              <div>
                <label htmlFor="pw-current">{t("currentPassword")}</label>
                <input
                  id="pw-current"
                  name="current"
                  type="password"
                  autoComplete="current-password"
                  required
                  value={current}
                  onChange={(e) => setCurrent(e.target.value)}
                />
              </div>
              <div>
                <label htmlFor="pw-next">{t("newPassword")}</label>
                <input
                  id="pw-next"
                  name="next"
                  type="password"
                  autoComplete="new-password"
                  minLength="12"
                  maxLength="72"
                  required
                  value={next}
                  onChange={(e) => setNext(e.target.value)}
                />
                <div className={`strength${next ? ` s${Math.max(1, score)}` : ""}`} aria-hidden="true"><i /><i /><i /><i /></div>
                <div className="stlbl">
                  {next
                    ? <>{t("pwStrengthLbl")} <b>{t(PW_WORDS[Math.max(1, score)])}</b></>
                    : t("pwMinLength")}
                </div>
              </div>
              <div>
                <label htmlFor="pw-confirm">{t("confirmPassword")}</label>
                <input
                  id="pw-confirm"
                  name="confirm"
                  type="password"
                  autoComplete="new-password"
                  minLength="12"
                  maxLength="72"
                  required
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                />
                <div className={`stlbl${confirm ? (match ? " ok" : " bad") : ""}`}>
                  {confirm ? (match ? t("pwMatch") : t("passwordMismatch")) : ""}
                </div>
              </div>
            </div>
            <p><button type="submit">{t("save")}</button></p>
          </form>
        </div>
      </section>

      <section className="sect">
        <h2>
          <span className="wrapl"><span className="snum">02</span>{t("totpSect")}</span>
          <small>{totpOn ? t("totpOn") : t("totpOff")}</small>
        </h2>
        <div className="sbody">
          <Err text={totp.error || totpError} />
          {!totpOn && !setup && (
            <p><button type="button" onClick={startTotp}>{t("totpEnable")}</button></p>
          )}
          {setup && (
            <form onSubmit={confirmTotp}>
              <img className="qr" alt="" src={`data:image/png;base64,${setup.qr_png}`} />
              <p className="mono">{t("totpSecret")}: {setup.secret}</p>
              <label htmlFor="totp-code">{t("twoFactorCode")}</label>
              <input id="totp-code" inputMode="numeric" autoComplete="one-time-code" required value={totpCode} onChange={(e) => setTotpCode(e.target.value)} />
              <p><button type="submit">{t("totpConfirm")}</button></p>
            </form>
          )}
          {totpOn && (
            <>
              <p className="hint">{t("totpCodesHint")}</p>
              <div className="codes">
                {backup.map((c) => (
                  <button
                    key={c.id}
                    type="button"
                    className={c.used ? "used" : ""}
                    disabled={c.used}
                    onClick={() => markCode(c.id)}
                  >{c.used ? t("totpUsed") : c.code}</button>
                ))}
              </div>
              <form onSubmit={disableTotp}>
                <label htmlFor="totp-pass">{t("totpPassword")}</label>
                <input id="totp-pass" type="password" autoComplete="current-password" required value={totpPass} onChange={(e) => setTotpPass(e.target.value)} />
                <p><button type="submit">{t("totpDisable")}</button></p>
              </form>
            </>
          )}
        </div>
      </section>

      <section className="sect">
        <h2>
          <span className="wrapl"><span className="snum">03</span>{t("activeSessions")}</span>
          {rows.length > 1 && (
            <button type="button" className="btn ghost sm" onClick={revokeOthers}>{t("revokeOthers")}</button>
          )}
        </h2>
        <div className="sbody">
          <Err text={error} />
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t("deviceCol")}</th><th>IP</th><th>{t("signedIn")}</th>
                <th>{t("lastSeen")}</th><th>{t("expiresCol")}</th><th></th>
              </tr>
            </thead>
            <tbody>
              {loading && !data && <SkeletonRows cols={6} rows={3} />}
              {!loading && !rows.length && !error && <EmptyRow colSpan={6}>{t("noData")}</EmptyRow>}
              {rows.map((s) => (
                <tr key={s.id} className={s.current ? "cur" : undefined}>
                  <td>
                    <span className="dev">
                      <span className="dic" aria-hidden="true">{deviceIcon(s.user_agent)}</span>
                      <span className="dn" title={s.user_agent}>{(s.user_agent || "").slice(0, 48) || "—"}</span>
                    </span>
                  </td>
                  <td className="mono">{s.ip || "—"}</td>
                  <td>{new Date(s.created_at).toLocaleString(undefined, SHORT_DATE)}</td>
                  <td className="rel">{s.last_seen_at ? ago(s.last_seen_at, t) : "—"}</td>
                  <td className="rel">{new Date(s.expires_at).toLocaleString(undefined, SHORT_DATE)}</td>
                  <td>
                    {s.current
                      ? <span className="curtag">{t("thisDevice")}</span>
                      : (
                        <span className="acts">
                          <button
                            type="button"
                            className="abtn del"
                            title={t("revoke")}
                            aria-label={t("revoke")}
                            onClick={() => revoke(s)}
                          >✕</button>
                        </span>
                      )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="sect">
        <h2>
          <span className="wrapl">
            <span className="snum">04</span>{t("recentActivity")}
            <small>{t("fromAudit")}</small>
          </span>
          <Link className="btn ghost sm" to="/audit">{t("allAudit")}</Link>
        </h2>
        <div className="sbody audit">
          <Err text={audit.error} />
          {/* Пока /api/audit грузится, строк не рисуем: «нет данных» показываем
              только после ответа — иначе первая секунда выглядит ошибкой. */}
          {!audit.loading && audit.data && !activity.length && <div className="hint">{t("noData")}</div>}
          {activity.map((r) => {
            const pairs = auditDetail(r.action, r.detail);
            return (
              <div className="arow" key={r.id}>
                <span className="at">{ago(r.at, t)}</span>
                <span className={`ai ${actDot(r.action)}`} aria-hidden="true" />
                <span className="aa">
                  <b>{ACT_KEYS[r.action] ? t(ACT_KEYS[r.action]) : r.action}</b>{" "}
                  <span>
                    {pairs.map((p, i) => {
                      let txt;
                      if (p.k === "ip") txt = t("detIp", { v: String(p.v) });
                      else if (p.k === "id") txt = t("detId", { v: String(p.v) });
                      else if (p.k === "ok") txt = p.v ? t("detNodeOk") : t("detNodeFail");
                      else txt = `${p.k}: ${String(p.v)}`;
                      return <span key={p.k}>{i > 0 && " · "}{txt}</span>;
                    })}
                  </span>
                </span>
              </div>
            );
          })}
        </div>
      </section>
    </>
  );
}
