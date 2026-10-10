import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api";
import { LangSwitch, useI18n } from "../i18n";
import { Err } from "../components/Bits";

const SPARK_ANGLES = [20, 80, 150, 210, 265, 320];
const SPARK_RADII = [310, 340, 470];

function sceneSparks() {
  return SPARK_ANGLES.map((a, i) => ({
    key: `g${i}`,
    cls: i % 3 === 0 ? "query q2" : i % 3 === 1 ? "query q3" : "query",
    fx: Math.cos((a * Math.PI) / 180) * SPARK_RADII[i % 3],
    fy: Math.sin((a * Math.PI) / 180) * SPARK_RADII[i % 3],
    delay: i * 0.55,
    duration: 2.2 + (i % 4) * 0.5,
  }));
}

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

export function Login({ onIn }) {
  const { t, err } = useI18n();
  const [params] = useSearchParams();
  const [error, setError] = useState("");
  const [totpTicket, setTotpTicket] = useState(null);
  const [totpCode, setTotpCode] = useState("");
  const [providers, setProviders] = useState([]);
  useEffect(() => {
    let live = true;
    api("/api/auth/providers").then((d) => {
      if (live) setProviders(d.providers || []);
    }).catch(() => {});
    return () => { live = false; };
  }, []);
  const oauthNote = OAUTH_ERR[params.get("oauth")];

  async function submit(event) {
    event.preventDefault();
    const data = new FormData(event.target);
    try {
      const out = await api("/api/login", { method: "POST", body: JSON.stringify({ username: data.get("username"), password: data.get("password") }) });
      if (out && out.totp_required) {
        setTotpTicket(out.ticket ?? "");
        setTotpCode("");
        setError("");
        return;
      }
      onIn(out.user, out.csrf);
    } catch (e) {
      setError(e);
    }
  }

  async function submitTotp(event) {
    event.preventDefault();
    try {
      const out = await api("/api/login/totp", { method: "POST", body: JSON.stringify({ ticket: totpTicket, code: totpCode }) });
      onIn(out.user, out.csrf);
    } catch (e) {
      setError(e);
    }
  }

  const sparks = useMemo(sceneSparks, []);

  return (
    <main className="login-scene">

      <div className="orbit o1" /><div className="orbit o2" /><div className="orbit o3" />

      <div className="ring" /><div className="ring" /><div className="ring" />

      <div className="core" />

      <div className="query" style={{ "--fx": "310px", "--fy": "0px" }} />
      <div className="query q2" style={{ "--fx": "-220px", "--fy": "-160px" }} />
      <div className="query q3" style={{ "--fx": "0px", "--fy": "310px" }} />
      <div className="query q4" style={{ "--fx": "-155px", "--fy": "270px" }} />
      {sparks.map((s) => (
        <div key={s.key} className={s.cls} style={{ "--fx": `${s.fx}px`, "--fy": `${s.fy}px`, animationDelay: `${s.delay}s`, animationDuration: `${s.duration}s` }} />
      ))}

      <div className="satwrap w1">
        <div className="satpos" style={{ "--a": "0deg" }}><div className="sat">www.example.com</div></div>
        <div className="satpos" style={{ "--a": "180deg" }}><div className="sat">api.example.com</div></div>
      </div>

      <div className="satwrap w2">
        <div className="satpos" style={{ "--a": "0deg" }}><div className="sat">mx1.mail.example.net</div></div>
      </div>

      <div className="satwrap w3">
        <div className="satpos" style={{ "--a": "0deg" }}><div className="sat">cdn.edges.example.org</div></div>
      </div>

      <div className="login-card">
        <div className="logo">DNS<b>marty</b></div>
        <Err text={error} />
        {oauthNote && <div className="banner err">{t(oauthNote)}</div>}
        {totpTicket === null ? (
          <form className="f" onSubmit={submit}>
            <label htmlFor="l-user">{t("username")}</label>
            <input id="l-user" name="username" autoComplete="username" required />
            <label htmlFor="l-pass">{t("password")}</label>
            <input id="l-pass" name="password" type="password" autoComplete="current-password" required />
            <button className="btn" type="submit">{t("signIn")}</button>
            {providers.length > 0 && (
              <div className="oauth">
                <span className="oauth-lead">{t("orContinueWith")}</span>
                <div className="oauth-row">
                  {providers.map((p) => (
                    <a key={p.id} className="oauth-btn" href={`/api/auth/oauth/${p.id}/login`}>{t(OAUTH_NAME[p.id] || p.name)}</a>
                  ))}
                </div>
              </div>
            )}
            <div className="langrow"><LangSwitch /></div>
          </form>
        ) : (

          <form className="f" onSubmit={submitTotp}>
            <p className="lead">{t("twoFactorTitle")}</p>
            <label htmlFor="l-totp">{t("twoFactorCode")}</label>
            <input id="l-totp" name="totp" inputMode="numeric" autoComplete="one-time-code" value={totpCode} onChange={(e) => setTotpCode(e.target.value)} required />
            <button className="btn" type="submit">{t("signIn")}</button>
            <p className="hint">{t("twoFactorHint")}</p>
          </form>
        )}
      </div>
    </main>
  );
}
