import { useMemo, useState } from "react";
import { api } from "../api";
import { LangSwitch, useI18n } from "../i18n";
import { Err } from "../components/Bits";

/* Орбитальная сцена входа — разметка/CSS/анимации 1:1 из утверждённого макета
   .design-lab/lab8.html («Орбиты · развитие»: ядро + 3 орбиты с бейджами
   доменов + искры + стеклянная форма; финальный вариант логина из итераций
   lab2–8). Стили — секция «Логин» в styles/pages.css.
   Сеть прежняя: POST /api/login → onIn(out.user, out.csrf) — App.jsx не тронут. */

// Искры-запросы: те же углы/радиусы/тайминги, что у генератора в lab8 <script>.
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

export function Login({ onIn }) {
  const { t, err } = useI18n();
  const [error, setError] = useState("");
  // 2FA-заготовка: ветка включается только когда ответ /api/login содержит
  // {totp_required:true, ticket} — бэкенд начнёт так отвечать в фазе 2,
  // до тех пор ветка не срабатывает. Отправка кода — тоже фаза 2.
  const [totpTicket, setTotpTicket] = useState(null);
  const [totpCode, setTotpCode] = useState("");
  // OAuth-заготовка: фаза 1 не запрашивает GET /api/auth/providers (эндпоинта нет),
  // кнопки не рисуются. Фаза 2 вернёт загрузку списка и переход на provider.url.
  const providers = [];

  async function submit(event) {
    event.preventDefault();
    const data = new FormData(event.target);
    try {
      const out = await api("/api/login", { method: "POST", body: JSON.stringify({ username: data.get("username"), password: data.get("password") }) });
      if (out && out.totp_required) {
        setTotpTicket(out.ticket ?? "");
        setError("");
        return;
      }
      onIn(out.user, out.csrf);
    } catch (e) {
      setError(e);
    }
  }

  const sparks = useMemo(sceneSparks, []);

  return (
    <main className="login-scene">
      {/* направляющие орбиты */}
      <div className="orbit o1" /><div className="orbit o2" /><div className="orbit o3" />
      {/* риплы от ядра */}
      <div className="ring" /><div className="ring" /><div className="ring" />
      {/* пульсирующее ядро */}
      <div className="core" />
      {/* искры: летят с орбит к ядру (4 из лабы + 6 от её генератора) */}
      <div className="query" style={{ "--fx": "310px", "--fy": "0px" }} />
      <div className="query q2" style={{ "--fx": "-220px", "--fy": "-160px" }} />
      <div className="query q3" style={{ "--fx": "0px", "--fy": "310px" }} />
      <div className="query q4" style={{ "--fx": "-155px", "--fy": "270px" }} />
      {sparks.map((s) => (
        <div key={s.key} className={s.cls} style={{ "--fx": `${s.fx}px`, "--fy": `${s.fy}px`, animationDelay: `${s.delay}s`, animationDuration: `${s.duration}s` }} />
      ))}

      {/* спутники: 1-я орбита (два бейджа) */}
      <div className="satwrap w1">
        <div className="satpos" style={{ "--a": "0deg" }}><div className="sat">www.example.com</div></div>
        <div className="satpos" style={{ "--a": "180deg" }}><div className="sat">api.example.com</div></div>
      </div>
      {/* 2-я орбита (warn, задний ход) */}
      <div className="satwrap w2">
        <div className="satpos" style={{ "--a": "0deg" }}><div className="sat">mx1.mail.example.net</div></div>
      </div>
      {/* 3-я орбита, приглушённые */}
      <div className="satwrap w3">
        <div className="satpos" style={{ "--a": "0deg" }}><div className="sat">cdn.edges.example.org</div></div>
      </div>

      {/* стеклянная карточка */}
      <div className="login-card">
        <div className="logo">DNS<b>marty</b></div>
        <p className="lead">{t("loginLead")}</p>
        <Err text={error} />
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
                  {/* Фаза 2: клик ведёт на URL провайдера — сегодня только отрисовка. */}
                  {providers.map((p) => (
                    <button key={p.id ?? p.name} type="button" className="oauth-btn">{p.name}</button>
                  ))}
                </div>
              </div>
            )}
            <div className="langrow"><LangSwitch /></div>
          </form>
        ) : (
          /* 2FA-шаг: поле кода; отправка кода подключается в фазе 2. */
          <form className="f" onSubmit={(e) => e.preventDefault()}>
            <p className="lead">{t("twoFactorTitle")}</p>
            <label htmlFor="l-totp">{t("twoFactorCode")}</label>
            <input id="l-totp" name="totp" inputMode="numeric" autoComplete="one-time-code" value={totpCode} onChange={(e) => setTotpCode(e.target.value)} />
            <button className="btn" type="submit" disabled>{t("signIn")}</button>
            <p className="hint">{t("twoFactorHint")}</p>
          </form>
        )}
      </div>
    </main>
  );
}
