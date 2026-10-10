import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../api";
import { useLoad } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { EmptyRow, Err, SkeletonRows } from "../components/Bits";
import { useConfirm, useToast } from "../components/Toast";
import { Modal } from "../ui/Modal";
import { auditDetail } from "../lib/logsfilters";
import Lamp from "../ui/Lamp";
import { ago, deviceIcon, pwStrength } from "../lib/util";

const PW_WORDS = ["", "pwWeak", "pwFair", "pwGood", "pwGreat"];
const OAUTH_ERR = {
  unlinked: "oauthErrUnlinked",
  denied: "oauthErrDenied",
  bad_state: "oauthErrState",
  failed: "oauthErrFailed",
};
const OAUTH_NAME = {
  google: "oauthNameGoogle",
  github: "oauthNameGitHub",
  yandex: "oauthNameYandex",
};
const SHORT_DATE = { dateStyle: "short", timeStyle: "short" };

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

function actDot(action) {
  if (action === "login") return "ok";
  if (action === "login.fail") return "bad";
  return "info";
}

function TotpModal({
  enroll, step, setStep, setup, code, setCode, password, setPassword,
  codes, copied, error, onCopy, onCopyCodes, onConfirm, onDisable, onClose, t,
}) {
  const labels = [t("totpStepKey"), t("totpStepCode"), t("totpStepCodes")];
  const showKey = enroll && step === 0;
  const showCode = enroll && step === 1;
  const showTickets = enroll && step === 2;
  const showOff = !enroll;
  return (
    <Modal
      open
      onClose={onClose}
      width={560}
      title={t("totpSect")}
      note={<span className="dirty">{enroll ? labels[step] : t("totpStepOff")}</span>}
      footer={(
        <>
          {!showTickets && <button type="button" className="btn ghost" onClick={onClose}>{t("cancel")}</button>}
          {showTickets && <button type="button" className="btn" onClick={onClose}>{t("totpDone")}</button>}
          {showKey && <button type="button" className="btn" onClick={() => setStep(1)}>{t("next")} →</button>}
          {showCode && <button type="submit" form="totp-confirm" className="btn">{t("totpConfirm")}</button>}
          {showOff && <button type="submit" form="totp-off" className="btn danger">{t("totpDisable")}</button>}
        </>
      )}
    >
      {enroll && (
        <div className="stepper">
          {labels.map((label, i) => (
            <span className="stepwrap" key={label}>
              {i > 0 && <span className="starrow" aria-hidden="true">→</span>}
              <span className={`step${i === step ? " on" : i < step ? " done" : ""}`}>
                <span className="n">{i < step ? "✓" : i + 1}</span>{label}
              </span>
            </span>
          ))}
        </div>
      )}
      {showKey && setup && (
        <div className="qrplate">
          <img alt="" src={`data:image/png;base64,${setup.qr_png}`} />
          <p className="hint">{t("totpScanHint")}</p>
          <div className="tok nn-tok">
            <span>{setup.secret}</span>
            <button type="button" className="copybtn" onClick={onCopy}>{copied ? t("copied") : t("copy")}</button>
          </div>
        </div>
      )}
      {showCode && (
        <form id="totp-confirm" className="code-step" onSubmit={onConfirm}>
          <Err text={error} />
          <label htmlFor="totp-code">{t("twoFactorCode")}</label>
          <input
            id="totp-code"
            className="codewell"
            inputMode="numeric"
            autoComplete="one-time-code"
            autoFocus
            maxLength={8}
            required
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
          <p className="hint">{t("twoFactorHint")}</p>
        </form>
      )}
      {showTickets && (
        <div className="ticket-step">
          <p className="hint">{t("totpCodesHint")}</p>
          <div className="tickets">
            {(codes || []).map((c, i) => (
              <div key={i} className="ticket">{c}</div>
            ))}
          </div>
          <button type="button" className="copybtn" onClick={onCopyCodes}>{copied ? t("copied") : t("copy")}</button>
        </div>
      )}
      {showOff && (
        <form id="totp-off" className="code-step" onSubmit={onDisable}>
          <Err text={error} />
          <div className="f">
            <label htmlFor="totp-pass">{t("totpPassword")}</label>
            <input id="totp-pass" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
          </div>
          <p className="hint">{t("totpOffHint")}</p>
        </form>
      )}
    </Modal>
  );
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
  const oauth = useLoad("/api/account/oauth");
  const [params] = useSearchParams();
  const [setup, setSetup] = useState(null);
  const [totpOpen, setTotpOpen] = useState(false);
  const [totpStep, setTotpStep] = useState(0);
  const [freshCodes, setFreshCodes] = useState(null);
  const [copied, setCopied] = useState(false);
  const [totpCode, setTotpCode] = useState("");
  const [totpError, setTotpError] = useState("");
  const [totpPass, setTotpPass] = useState("");

  const user = me.data?.user;
  const rows = data || [];

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

  function closeTotp() {
    setTotpOpen(false);
    setSetup(null);
    setFreshCodes(null);
    setTotpStep(0);
    setTotpCode("");
    setTotpPass("");
    setCopied(false);
    setTotpError("");
  }

  async function startTotp() {
    setTotpError("");
    try {
      setSetup(await api("/api/account/2fa/setup", { method: "POST", body: "{}" }));
      setTotpCode("");
      setFreshCodes(null);
      setTotpStep(0);
      setTotpOpen(true);
    } catch (e) { setTotpError(e); }
  }

  function openTotp() {
    setSetup(null);
    setFreshCodes(null);
    setTotpStep(0);
    setTotpError("");
    setTotpOpen(true);
  }

  async function confirmTotp(event) {
    event.preventDefault();
    setTotpError("");
    try {
      const out = await api("/api/account/2fa/enable", { method: "POST", body: JSON.stringify({ code: totpCode }) });
      setCopied(false);
      setFreshCodes(out.codes || []);
      setTotpStep(2);
      totp.reload();
      audit.reload();
    } catch (e) { setTotpError(e); }
  }

  async function copySecret() {
    try {
      await navigator.clipboard.writeText(setup.secret);
      setCopied(true);
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
      closeTotp();
    } catch (e) { setTotpError(e); }
  }

  async function copyCodes() {
    try {
      await navigator.clipboard.writeText((freshCodes || []).join("\n"));
      setCopied(true);
    } catch (e) { setTotpError(e); }
  }

  function unlinkOAuth(id) {
    ask(t("oauthUnlinkAsk"), async () => {
      try {
        await api(`/api/account/oauth/${id}`, { method: "DELETE" });
        oauth.reload();
      } catch (e) { oauth.setError(e); }
    });
  }

  const score = pwStrength(next);
  const match = next === confirm;
  const totpOn = !!totp.data?.enabled;

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
            <span>{t("roleAdmin")} · {t("panelVer", { v: me.data.version || "—" })}</span>
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
            <p><button type="submit" className="btn">{t("save")}</button></p>
          </form>
        </div>
      </section>

      <section className="sect">
        <h2>
          <span className="wrapl"><span className="snum">02</span>{t("totpSect")}</span>
          <small>{totpOn ? t("totpOn") : t("totpOff")}</small>
        </h2>
        <div className="sbody totp-status">
          <Err text={totp.error || (!totpOpen && totpError)} />
          <Lamp state={totpOn ? "on" : "dis"} tip={totpOn ? t("totpOn") : t("totpOff")} />
          <p>{totpOn ? t("totpOn") : t("totpOff")}</p>
          {totpOn
            ? <button type="button" className="btn danger" onClick={openTotp}>{t("totpDisable")}</button>
            : <button type="button" className="btn" onClick={startTotp}>{t("totpEnable")}</button>}
        </div>
      </section>
      {totpOpen && (
        <TotpModal
          enroll={!!setup}
          step={totpStep}
          setStep={setTotpStep}
          setup={setup}
          code={totpCode}
          setCode={setTotpCode}
          password={totpPass}
          setPassword={setTotpPass}
          codes={freshCodes || []}
          copied={copied}
          error={totpError}
          onCopy={copySecret}
          onCopyCodes={copyCodes}
          onConfirm={confirmTotp}
          onDisable={disableTotp}
          onClose={closeTotp}
          t={t}
        />
      )}

      <section className="sect">
        <h2>
          <span className="wrapl"><span className="snum">03</span>{t("oauthAccount")}</span>
          <small>{t("oauthAccountSub")}</small>
        </h2>
        <div className="sbody">
          <Err text={oauth.error} />
          {OAUTH_ERR[params.get("oauth")] && <div className="banner err">{t(OAUTH_ERR[params.get("oauth")])}</div>}
          {(oauth.data?.providers || []).map((p) => (
            <div className="oauth-acc" key={p.id}>
              <b>{t(OAUTH_NAME[p.id] || p.name)}</b>
              <span className={p.linked && p.display_name ? "nm" : undefined}>
                {p.linked ? (p.display_name || t("oauthLinked")) : t("oauthNotLinked")}
              </span>
              {p.linked
                ? <button type="button" className="btn ghost sm" onClick={() => unlinkOAuth(p.id)}>{t("oauthUnlink")}</button>
                : p.ready
                  ? <a className="btn sm" href={`/api/account/oauth/${p.id}/link`}>{t("oauthLink")}</a>
                  : <span className="hint">{t("oauthNeedSettings")}</span>}
            </div>
          ))}
        </div>
      </section>

      <section className="sect">
        <h2>
          <span className="wrapl"><span className="snum">04</span>{t("activeSessions")}</span>
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
            <span className="snum">05</span>{t("recentActivity")}
            <small>{t("fromAudit")}</small>
          </span>
          <Link className="btn ghost sm" to="/audit">{t("allAudit")}</Link>
        </h2>
        <div className="sbody audit">
          <Err text={audit.error} />

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
