import { useEffect, useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import {
  Activity, Globe2, FolderTree, ShieldCheck, ScrollText, Settings2,
  History, UserRound, LogOut, Menu, Monitor, Sun, Moon, BarChart3,
} from "lucide-react";
import { api } from "../api";
import { LangSwitch, useI18n } from "../i18n";
import { useTheme } from "../hooks/useLoad";

const nav = [
  ["/", "overview", Activity],
  ["/nodes", "nodes", Globe2],
  ["/domains", "domains", FolderTree],
  ["/clients", "clients", ShieldCheck],
  ["/logs", "logs", ScrollText],
  ["/stats", "stats", BarChart3],
  ["/settings", "settings", Settings2],
  ["/audit", "audit", History],
  ["/account", "account", UserRound],
];

function ThemeSwitch() {
  const { t } = useI18n();
  const [theme, setTheme] = useTheme();
  const opts = [
    ["system", Monitor, t("themeSystem")],
    ["light", Sun, t("themeLight")],
    ["dark", Moon, t("themeDark")],
  ];
  return (
    <span className="lang" role="group" aria-label={t("theme")}>
      {opts.map(([value, Icon, label]) => (
        <button
          key={value}
          type="button"
          className={theme === value ? "on" : ""}
          onClick={() => setTheme(value)}
          aria-pressed={theme === value}
          aria-label={label}
          title={label}
        ><Icon size={14} /></button>
      ))}
    </span>
  );
}

export function Shell({ user, onOut }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
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
        <div className="brand"><b>DNSmarty</b><span>DNS</span></div>
        <nav aria-label={t("menu")}>
          {nav.map(([to, key, Icon]) => (
            <NavLink
              key={to}
              to={to}
              end={to === "/"}
              className={({ isActive }) => (isActive ? "on" : "")}
              onClick={() => setOpen(false)}
            >
              <Icon size={16} />{t(key)}
            </NavLink>
          ))}
        </nav>
        <div className="who">
          <ThemeSwitch />
          <LangSwitch />
          <span>{user}</span>
          <button className="ghost" onClick={logout}><LogOut size={14} style={{ marginRight: 6, verticalAlign: "-2px" }} />{t("logout")}</button>
        </div>
      </aside>
      <main className="main" id="content"><Outlet /></main>
    </div>
  );
}
