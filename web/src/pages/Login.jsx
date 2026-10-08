import { useState } from "react";
import { api } from "../api";
import { LangSwitch, useI18n } from "../i18n";
import { Err } from "../components/Bits";

export function Login({ onIn }) {
  const { t, err } = useI18n();
  const [error, setError] = useState("");
  async function submit(event) {
    event.preventDefault();
    const data = new FormData(event.target);
    try {
      const out = await api("/api/login", { method: "POST", body: JSON.stringify({ username: data.get("username"), password: data.get("password") }) });
      onIn(out.user, out.csrf);
    } catch (e) {
      setError(e.message);
    }
  }
  return (
    <main className="login">
      <div className="login-top"><h1>DNSmarty</h1><LangSwitch /></div>
      <p className="empty">{t("loginLead")}</p>
      <Err text={error} />
      <form className="stack" onSubmit={submit}>
        <div><label htmlFor="l-user">{t("username")}</label><input id="l-user" name="username" autoComplete="username" required /></div>
        <div><label htmlFor="l-pass">{t("password")}</label><input id="l-pass" name="password" type="password" autoComplete="current-password" required /></div>
        <button type="submit">{t("signIn")}</button>
      </form>
    </main>
  );
}
