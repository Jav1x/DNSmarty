import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { LogOut, Menu } from "lucide-react";
import { api } from "../api";
import { LangSwitch, useI18n } from "../i18n";
import { ROUTES } from "../routes";

/* Сайдбар — спец §4: три группы (Мониторинг / Управление / Панель).
   Разметка и глифы 1:1 из утверждённого макета .design-lab/lab16.html:
   mono-глифы в .ic, активный пункт .on с inset-полосой.
   Активность считает сам Shell (не NavLink). */
const groups = [
  ["groupMonitoring", [
    ["/", "overview", "◉"],
    ["/logs", "logs", "▤"],
    ["/stats", "stats", "▥"],
  ]],
  ["groupManagement", [
    ["/nodes", "nodes", "◈"],
    [ROUTES.services, "services", "❧"],
    [ROUTES.access, "access", "⛨"],
  ]],
  ["groupPanel", [
    ["/settings", "settings", "⚙"],
    ["/audit", "audit", "⏱"],
    ["/account", "account", "◉"],
  ]],
];

export function Shell({ user, onOut }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const { pathname } = useLocation();
  // Close the drawer on navigation.
  useEffect(() => () => setOpen(false), [navigate]);
  async function logout() {
    try {
      await api("/api/logout", { method: "POST" });
    } finally {
      onOut();
      navigate("/login");
    }
  }
  return (
    <div className="app">
      <a className="skip" href="#content">{t("skipToContent")}</a>
      <button
        type="button"
        className="ghost tiny burger"
        style={{ position: "fixed", top: 10, left: 10, zIndex: 55 }}
        onClick={() => setOpen(true)}
        aria-expanded={open}
        aria-label={t("menu")}
      ><Menu size={16} /></button>
      {open && <button type="button" className="backdrop" aria-label={t("menu")} onClick={() => setOpen(false)} />}
      <aside className={`side${open ? " open" : ""}`}>
        <div className="brand"><b>DNS</b>marty</div>
        <nav aria-label={t("menu")}>
          {groups.map(([gkey, items]) => (
            <div className="grp" key={gkey}>
              <div className="gh">{t(gkey)}</div>
              {items.map(([to, key, glyph, activePaths]) => {
                const active = (activePaths ?? [to]).includes(pathname);
                return (
                  <NavLink
                    key={to}
                    to={to}
                    end={to === "/"}
                    className={active ? "on" : ""}
                    aria-current={active ? "page" : undefined}
                    onClick={() => setOpen(false)}
                  >
                    <span className="ic" aria-hidden="true">{glyph}</span>{t(key)}
                  </NavLink>
                );
              })}
            </div>
          ))}
        </nav>
        <div className="who">
          <LangSwitch />
          <span>{user}</span>
          <button className="ghost" onClick={logout}><LogOut size={14} style={{ marginRight: 6, verticalAlign: "-2px" }} />{t("logout")}</button>
        </div>
      </aside>
      <main className="main" id="content"><Outlet /></main>
    </div>
  );
}
