import { useEffect, useState } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { api, setCSRF } from "./api";
import { ToastProvider } from "./components/Toast";
import { I18nProvider } from "./i18n";
import { Login } from "./pages/Login";
import { Shell } from "./pages/Shell";
import { Overview } from "./pages/Overview";
import { Nodes } from "./pages/Nodes";
import { Domains } from "./pages/Domains";
import { Clients } from "./pages/Clients";
import { Logs } from "./pages/Logs";
import { Stats } from "./pages/Stats";
import { Account } from "./pages/Account";
import { Settings } from "./pages/Settings";
import { Audit } from "./pages/Audit";

export function App() {
  const [user, setUser] = useState(undefined);
  useEffect(() => {
    api("/api/me").then((d) => {
      setUser(d.user);
      setCSRF(d.csrf);
    }).catch(() => setUser(null));
  }, []);
  if (user === undefined) return null;
  return (
    <I18nProvider>
      <ToastProvider>
        <Routes>
          <Route path="/login" element={user ? <Navigate to="/" /> : <Login onIn={(u, csrf) => { setUser(u); setCSRF(csrf); }} />} />
          <Route element={user ? <Shell user={user} onOut={() => setUser(null)} /> : <Navigate to="/login" />}>
            <Route path="/" element={<Overview />} />
            <Route path="/nodes" element={<Nodes />} />
            <Route path="/domains" element={<Domains />} />
            <Route path="/clients" element={<Clients />} />
            <Route path="/logs" element={<Logs />} />
            <Route path="/stats" element={<Stats />} />
            <Route path="/account" element={<Account />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="/audit" element={<Audit />} />
          </Route>
        </Routes>
      </ToastProvider>
    </I18nProvider>
  );
}
