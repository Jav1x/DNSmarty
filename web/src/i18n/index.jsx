import { createContext, useContext, useEffect, useState } from "react";
import { fields as dictFields, phrases as dictPhrases, dict as dicts } from "./dicts.js";

const I18n = createContext(null);

function initialLang() {
  const saved = localStorage.getItem("dnsmarty-lang");
  if (saved === "en" || saved === "ru") return saved;
  return navigator.language?.toLowerCase().startsWith("ru") ? "ru" : "en";
}

export function I18nProvider({ children }) {
  const [lang, setLangState] = useState(initialLang);
  useEffect(() => {
    document.documentElement.lang = lang;
  }, [lang]);
  function setLang(next) {
    localStorage.setItem("dnsmarty-lang", next);
    setLangState(next);
  }
  function t(key, vars) {
    let text = dicts[lang][key] ?? dicts.en[key] ?? key;
    // A few strings need live agreement (e.g. ru plurals) — dicts may hold a
    // function of the vars instead of a ready-made template.
    if (typeof text === "function") return text(vars ?? {}, lang);
    if (vars) {
      for (const [name, value] of Object.entries(vars)) {
        text = text.replaceAll(`{${name}}`, value);
      }
    }
    return text;
  }
  function err(message) {
    if (!message) return "";
    if (typeof message === "object" && message !== null) {
      return errFromBody(message);
    }
    const exact = dictPhrases[lang][message];
    if (exact) return exact;
    const reach = message.match(/^cannot reach (.+)$/);
    if (reach) return t("errReach", { host: reach[1] });
    const field = message.match(/^Check the field: (.+)$/);
    if (field) {
      const name = dictFields[lang][field[1]] || field[1];
      return t("errInvalid", { field: name });
    }
    const agent = message.match(/^agent replied (.+)$/);
    if (agent) return t("errAgentReplied", { detail: agent[1] });
    const cfg = message.match(/^config rejected: (.+)$/);
    if (cfg) return t("errConfigRejected", { detail: cfg[1] });
    const stale = message.match(/^agent has a newer snapshot: (.+)$/);
    if (stale) return t("errStaleSnap", { n: stale[1] });
    const stats = message.match(/^stats: (.+)$/);
    if (stats) return t("errStats", { detail: stats[1] });
    return message;
  }

  // errFromBody translates a structured {error, code, field} API error by its code.
  function errFromBody(body) {
    const key = {
      unauthorized: "errUnauthorized",
      csrf: "errCsrf",
      rate_limited: "errRateLimited",
      bad_credentials: "errBadCredentials",
      bad_code: "errBadCode",
      wrong_password: "errWrongPassword",
      weak_password: "errWeakPassword",
      conflict: "errConflict",
      not_found: "errNotFound",
      bad_json: "errBadJson",
      content_type: "errContentType",
      invalid: "errInvalid",
      internal: "errInternal",
    }[body.code];
    if (key) {
      return t(key, { field: (dictFields[lang][body.field] || body.field || "") });
    }
    return body.error || String(body);
  }
  return <I18n.Provider value={{ lang, setLang, t, err }}>{children}</I18n.Provider>;
}

export function useI18n() {
  return useContext(I18n);
}

export function LangSwitch() {
  const { lang, setLang } = useI18n();
  return (
    <span className="lang" role="group" aria-label="Language">
      <button type="button" className={lang === "en" ? "on" : ""} onClick={() => setLang("en")} aria-pressed={lang === "en"}>EN</button>
      <button type="button" className={lang === "ru" ? "on" : ""} onClick={() => setLang("ru")} aria-pressed={lang === "ru"}>RU</button>
    </span>
  );
}
