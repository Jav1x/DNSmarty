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
    if (vars) {
      for (const [name, value] of Object.entries(vars)) {
        text = text.replaceAll(`{${name}}`, value);
      }
    }
    return text;
  }
  function err(message) {
    if (!message) return "";
    const exact = dictPhrases[lang][message];
    if (exact) return exact;
    const reach = message.match(/^(?:нет связи с|Cannot reach) (.+)$/);
    if (reach) return lang === "ru" ? `Нет связи с ${reach[1]}` : `Cannot reach ${reach[1]}`;
    const field = message.match(/^(?:Проверьте поле|Check the field): (.+)$/);
    if (field) {
      const name = dictFields[lang][field[1]] || field[1];
      return lang === "ru" ? `Проверьте поле: ${name}` : `Check the field: ${name}`;
    }
    const boot = message.match(/^Проверьте поле: bootstrap (.+)$/);
    if (boot) return lang === "ru" ? message : `Check the field: bootstrap ${boot[1]}`;
    const agent = message.match(/^агент ответил (.+)$/);
    if (agent) return lang === "ru" ? message : `Agent replied ${agent[1]}`;
    const cfg = message.match(/^конфиг не принят: (.+)$/);
    if (cfg) return lang === "ru" ? message : `Config was rejected: ${cfg[1]}`;
    return message;
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
