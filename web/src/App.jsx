import { Fragment, useEffect, useState } from "react";
import { NavLink, Navigate, Outlet, Route, Routes, useNavigate } from "react-router-dom";
import { ApiError, api } from "./api";
import { LangSwitch, useI18n } from "./i18n";

export function App() {
  const [user, setUser] = useState(undefined);
  useEffect(() => {
    api("/api/me").then((d) => setUser(d.user)).catch(() => setUser(null));
  }, []);
  if (user === undefined) return null;
  return (
    <Routes>
      <Route path="/login" element={user ? <Navigate to="/" /> : <Login onIn={setUser} />} />
      <Route element={user ? <Shell user={user} onOut={() => setUser(null)} /> : <Navigate to="/login" />}>
        <Route path="/" element={<Overview />} />
        <Route path="/nodes" element={<Nodes />} />
        <Route path="/domains" element={<Domains />} />
        <Route path="/clients" element={<Clients />} />
        <Route path="/logs" element={<Logs />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="/audit" element={<Audit />} />
      </Route>
    </Routes>
  );
}

function Login({ onIn }) {
  const { t, err } = useI18n();
  const [error, setError] = useState("");
  async function submit(event) {
    event.preventDefault();
    const data = new FormData(event.target);
    try {
      const out = await api("/api/login", { method: "POST", body: JSON.stringify({ username: data.get("username"), password: data.get("password") }) });
      onIn(out.user);
    } catch (e) {
      setError(err(e.message));
    }
  }
  return (
    <main className="login">
      <div className="login-top"><h1>DNSmarty</h1><LangSwitch /></div>
      <p className="empty">{t("loginLead")}</p>
      <Err text={error} />
      <form className="stack" onSubmit={submit}>
        <div><label>{t("username")}</label><input name="username" autoComplete="username" required /></div>
        <div><label>{t("password")}</label><input name="password" type="password" autoComplete="current-password" required /></div>
        <button type="submit">{t("signIn")}</button>
      </form>
    </main>
  );
}

function Shell({ user, onOut }) {
  const { t } = useI18n();
  const nav = [
    ["/", "overview"],
    ["/nodes", "nodes"],
    ["/domains", "domains"],
    ["/clients", "clients"],
    ["/logs", "logs"],
    ["/settings", "settings"],
    ["/audit", "audit"],
  ];
  async function logout() {
    await api("/api/logout", { method: "POST" });
    onOut();
  }
  return (
    <div className="app">
      <aside className="side">
        <div className="brand"><b>DNSmarty</b><span>DNS</span></div>
        <nav>
          {nav.map(([to, key]) => (
            <NavLink key={to} to={to} end={to === "/"} className={({ isActive }) => (isActive ? "on" : "")}>{t(key)}</NavLink>
          ))}
        </nav>
        <div className="who"><LangSwitch /><span>{user}</span><button className="ghost" onClick={logout}>{t("logout")}</button></div>
      </aside>
      <main className="main"><Outlet /></main>
    </div>
  );
}

function Err({ text }) {
  const { err } = useI18n();
  if (!text) return null;
  return <div className="banner err">{err(text)}</div>;
}

function useLoad(path) {
  const [data, setData] = useState(null);
  const [error, setError] = useState("");
  const navigate = useNavigate();
  async function reload() {
    try {
      setData(await api(path));
      setError("");
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) navigate("/login");
      else setError(e.message);
    }
  }
  useEffect(() => { reload(); }, [path]);
  return { data, error, setError, reload };
}

function Overview() {
  const { t } = useI18n();
  const { data, error } = useLoad("/api/overview");
  if (!data) return error ? <Err text={error} /> : null;
  const o = data.overview;
  return (
    <>
      <section className="meters">
        <div className="meter"><i>{t("qps")}</i><b>{o.qps}</b></div>
        <div className="meter"><i>{t("sessions60")}</i><b>{o.sessions}</b></div>
        <div className="meter"><i>{t("bytes24")}</i><b>{formatBytes(o.bytes)}</b></div>
        <div className="meter"><i>{t("acl60")}</i><b>{o.refused}</b></div>
        <div className="meter"><i>{t("domains")}</i><b>{o.rows?.length || 0}</b></div>
      </section>
      <section className="mod">
        <h2>{t("nodeCol")}</h2>
        <NodeTable nodes={data.nodes} />
      </section>
      <Err text={error} />
    </>
  );
}

function NodeTable({ nodes }) {
  const { t, err } = useI18n();
  return (
    <table>
      <thead><tr><th>{t("name")}</th><th>{t("role")}</th><th>{t("publicIp")}</th><th>{t("agent")}</th><th>{t("error")}</th></tr></thead>
      <tbody>
        {nodes?.length ? nodes.map((n) => (
          <tr key={n.id}>
            <td><span className={`lamp ${n.fresh ? "on" : "off"}`} />{n.name}</td>
            <td>{n.role}</td>
            <td>{ipOf(n)}</td>
            <td>{n.agent_host}:{n.agent_port}</td>
            <td>{n.last_error ? err(n.last_error) : "—"}</td>
          </tr>
        )) : <tr><td colSpan="5" className="empty">{t("noNodes")}</td></tr>}
      </tbody>
    </table>
  );
}

function Nodes() {
  const { t, err } = useI18n();
  const { data, error, setError, reload } = useLoad("/api/nodes");
  const [open, setOpen] = useState(null);
  const nodes = data || [];
  const editing = open?.mode === "edit" ? nodes.find((n) => n.id === open.node.id) || open.node : null;
  return (
    <>
      <Err text={error} />
      <section className="sheet">
        <div className="toolbar">
          <h2>{t("nodes")}</h2>
          <button type="button" onClick={() => setOpen({ mode: "new" })}>{t("newNode")}</button>
        </div>
        <table>
          <thead>
            <tr>
              <th>{t("name")}</th>
              <th>{t("role")}</th>
              <th>{t("publicIp")}</th>
              <th>{t("agent")}</th>
              <th>{t("state")}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {nodes.length ? nodes.map((n) => (
              <tr key={n.id}>
                <td><span className={`lamp ${n.fresh ? "on" : "off"}`} />{n.name}</td>
                <td>{n.role}</td>
                <td className="mono">{ipOf(n)}</td>
                <td className="mono">{n.agent_host}:{n.agent_port}</td>
                <td>{n.last_error ? err(n.last_error) : (n.fresh ? t("online") : t("offline"))}</td>
                <td><button type="button" className="ghost tiny" onClick={() => setOpen({ mode: "edit", node: n })}>{t("edit")}</button></td>
              </tr>
            )) : <tr><td colSpan="6" className="empty">{t("noNodes")}</td></tr>}
          </tbody>
        </table>
      </section>
      {open && (
        <NodeModal
          mode={open.mode}
          node={editing}
          onClose={() => setOpen(null)}
          onChange={reload}
          setError={setError}
        />
      )}
    </>
  );
}

function NodeModal({ mode, node, onClose, onChange, setError }) {
  const { t, err } = useI18n();
  const [step, setStep] = useState(0);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState(null);
  const [connect, setConnect] = useState(null);
  const [copied, setCopied] = useState("");
  const [msg, setMsg] = useState("");
  useEffect(() => {
    function onKey(event) {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  async function copyText(label, text) {
    await navigator.clipboard.writeText(text);
    setCopied(label);
  }
  async function create(event) {
    event.preventDefault();
    if (busy) return;
    setBusy(true);
    try {
      const out = await api("/api/nodes", { method: "POST", body: JSON.stringify(nodeBody(new FormData(event.target))) });
      setCreated(out);
      setConnect(null);
      setStep(1);
      onChange();
    } catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  async function checkNew() {
    if (busy || !created) return;
    setBusy(true);
    try {
      const out = await api(`/api/nodes/${created.node.id}/connect`, { method: "POST" });
      setConnect(out);
      setStep(2);
      onChange();
    } catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  async function save(event) {
    event.preventDefault();
    try {
      await api(`/api/nodes/${node.id}`, { method: "POST", body: JSON.stringify(nodeBody(new FormData(event.target))) });
      setMsg(t("saved"));
      onChange();
    } catch (e) { setError(e.message); }
  }
  async function check() {
    try {
      const out = await api(`/api/nodes/${node.id}/connect`, { method: "POST" });
      setMsg(out.ok ? t("linkedShort") : err(out.error));
      onChange();
    } catch (e) { setError(e.message); }
  }
  async function rotate() {
    if (!window.confirm(t("confirmRotate", { name: node.name }))) return;
    try {
      const out = await api(`/api/nodes/${node.id}/key`, { method: "POST" });
      setMsg(t("newKeyOnce", { key: out.key }));
    } catch (e) { setError(e.message); }
  }
  async function remove() {
    if (!window.confirm(t("confirmDelete", { name: node.name }))) return;
    try {
      await api(`/api/nodes/${node.id}`, { method: "DELETE" });
      onChange();
      onClose();
    } catch (e) { setError(e.message); }
  }
  const title = mode === "new" ? t("newNode") : node?.name;
  const install = created
    ? `dnsmarty-node install --role ${created.node.role} --key ${created.key} --port ${created.node.agent_port}`
    : "";
  return (
    <div className="modal-back" onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <div className="modal" role="dialog" aria-modal="true" aria-label={title}>
        <div className="modal-head">
          <h2>{title}</h2>
          <button type="button" className="ghost tiny" onClick={onClose}>{t("close")}</button>
        </div>
        {mode === "new" && (
          <div className="stepper">
            {[t("stepAddr"), t("stepKey"), t("stepLink")].map((label, i) => (
              <Fragment key={label}>
                {i > 0 && <span className={i <= step ? "step-line on" : "step-line"} />}
                <span className={i === step ? "step on" : i < step ? "step done" : "step"}><b>{i < step ? "✓" : i + 1}</b>{label}</span>
              </Fragment>
            ))}
          </div>
        )}
        {mode === "new" && step === 0 && (
          <form onSubmit={create}>
            <NodeFields />
            <p className="empty">{t("nodeHint")}</p>
            <div className="modal-foot">
              <button type="button" className="ghost" onClick={onClose}>{t("back")}</button>
              <button type="submit" disabled={busy}>{t("next")}</button>
            </div>
          </form>
        )}
        {mode === "new" && step === 1 && created && (
          <div>
            <p>{t("keyOnce", { name: created.node.name })}</p>
            <div className="copy-box">
              <p className="token">{created.key}</p>
              <button type="button" className="ghost tiny" onClick={() => copyText("key", created.key)}>{copied === "key" ? t("copied") : t("copy")}</button>
            </div>
            <p className="empty">{t("scriptHint", { port: created.node.agent_port })}</p>
            <div className="copy-box">
              <pre className="banner note">{install}</pre>
              <button type="button" className="ghost tiny" onClick={() => copyText("cmd", install)}>{copied === "cmd" ? t("copied") : t("copy")}</button>
            </div>
            <p><a href="/install/node.sh">{t("downloadScript")}</a></p>
            <div className="modal-foot">
              <button type="button" onClick={checkNew} disabled={busy}>{t("next")}</button>
            </div>
          </div>
        )}
        {mode === "new" && step === 2 && connect && (
          <div>
            {connect.ok
              ? <div className="banner ok">{t("linked")}</div>
              : <div className="banner err">{t("notLinked", { error: err(connect.error) })}</div>}
            <div className="modal-foot">
              {!connect.ok && <button type="button" className="ghost" onClick={checkNew} disabled={busy}>{t("checkLink")}</button>}
              <button type="button" onClick={onClose}>{t("close")}</button>
            </div>
          </div>
        )}
        {mode === "edit" && node && (
          <>
            {msg && <div className="banner note">{msg}</div>}
            <form onSubmit={save}>
              <NodeFields node={node} />
              <p className="empty"><span className={`lamp ${node.fresh ? "on" : "off"}`} />{node.fresh ? t("online") : t("offline")}{node.last_error ? ` · ${err(node.last_error)}` : ""}</p>
              <div className="row-actions">
                <button type="submit">{t("save")}</button>
                <button type="button" className="ghost" onClick={check}>{t("checkLink")}</button>
                <button type="button" className="ghost" onClick={rotate}>{t("newKey")}</button>
                <button type="button" className="alarm" onClick={remove}>{t("delete")}</button>
              </div>
            </form>
          </>
        )}
      </div>
    </div>
  );
}

function NodeFields({ node }) {
  const { t } = useI18n();
  return (
    <div className="grid">
      <div><label>{t("name")}</label><input name="name" defaultValue={node?.name} required /></div>
      <div><label>{t("role")}</label><select name="role" defaultValue={node?.role || "dns"}><option value="dns">dns</option><option value="proxy">proxy</option></select></div>
      <div><label>{t("ipv4")}</label><input name="public_ipv4" defaultValue={node?.public_ipv4} placeholder="203.0.113.10" /></div>
      <div><label>{t("ipv6")}</label><input name="public_ipv6" defaultValue={node?.public_ipv6} /></div>
      <div><label>{t("region")}</label><input name="region" defaultValue={node?.region} /></div>
      <div><label>{t("agentHost")}</label><input name="agent_host" defaultValue={node?.agent_host} placeholder="203.0.113.10" required /></div>
      <div><label>{t("agentPort")}</label><input name="agent_port" defaultValue={node?.agent_port || (node?.role === "proxy" ? "9444" : "9443")} required /></div>
      <div><label>{t("inRotation")}</label><label className="check"><input type="checkbox" name="enabled" defaultChecked={node ? node.enabled : true} />{t("enabled")}</label></div>
    </div>
  );
}

const pageSize = 40;

function Domains() {
  const { t } = useI18n();
  const { data, error, setError, reload } = useLoad("/api/domains");
  const [group, setGroup] = useState("all");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(0);
  const [editing, setEditing] = useState(null);
  if (!data) return error ? <Err text={error} /> : null;
  const groups = data.groups || [];
  const domains = data.domains || [];
  const query = q.trim().toLowerCase();
  const filtered = domains.filter((d) => {
    if (group === "none" && d.group_id) return false;
    if (group !== "all" && group !== "none" && d.group_id !== group) return false;
    if (!query) return true;
    return d.name.toLowerCase().includes(query) || (d.comment || "").toLowerCase().includes(query);
  });
  const pages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const current = Math.min(page, pages - 1);
  const slice = filtered.slice(current * pageSize, current * pageSize + pageSize);
  const ungrouped = domains.filter((d) => !d.group_id).length;
  async function createGroup(event) {
    event.preventDefault();
    const form = new FormData(event.target);
    try {
      const out = await api("/api/groups", { method: "POST", body: JSON.stringify({ name: form.get("name") }) });
      event.target.reset();
      setGroup(out.id);
      setPage(0);
      reload();
    } catch (err) { setError(err.message); }
  }
  async function assignGroup(domain, groupID) {
    try {
      await api(`/api/domains/${domain.id}`, { method: "POST", body: JSON.stringify(domainPayload(domain, groupID)) });
      reload();
    } catch (err) { setError(err.message); }
  }
  async function create(event) {
    event.preventDefault();
    try {
      await api("/api/domains", { method: "POST", body: JSON.stringify(domainBody(new FormData(event.target), data.proxies)) });
      event.target.reset();
      setEditing(null);
      reload();
    } catch (err) { setError(err.message); }
  }
  async function removeGroup(g) {
    if (!window.confirm(t("confirmDelete", { name: g.name }))) return;
    try {
      await api(`/api/groups/${g.id}`, { method: "DELETE" });
      if (group === g.id) setGroup("all");
      reload();
    } catch (err) { setError(err.message); }
  }
  return (
    <>
      <Err text={error} />
      <div className="domains">
        <aside className="groups">
          <button type="button" className={group === "all" ? "group-item on" : "group-item"} onClick={() => { setGroup("all"); setPage(0); }}>
            {t("allGroups")}<span>{domains.length}</span>
          </button>
          <button type="button" className={group === "none" ? "group-item on" : "group-item"} onClick={() => { setGroup("none"); setPage(0); }}>
            {t("ungrouped")}<span>{ungrouped}</span>
          </button>
          {groups.map((g) => (
            <div className="group-head" key={g.id}>
              <button type="button" className={group === g.id ? "group-item on" : "group-item"} onClick={() => { setGroup(g.id); setPage(0); }}>
                {g.name}<span>{g.count}</span>
              </button>
              <button type="button" className="ghost tiny" onClick={() => removeGroup(g)}>{t("delete")}</button>
            </div>
          ))}
          <form className="group-add" onSubmit={createGroup}>
            <input name="name" placeholder={t("groupName")} required />
            <button type="submit" className="tiny">{t("add")}</button>
          </form>
        </aside>
        <section className="sheet">
          <div className="toolbar">
            <input value={q} onChange={(e) => { setQ(e.target.value); setPage(0); }} placeholder={t("searchDomains")} />
            <button type="button" onClick={() => setEditing(editing === "new" ? null : "new")}>{t("newDomain")}</button>
          </div>
          {editing === "new" && (
            <form onSubmit={create}>
              <DomainFields proxies={data.proxies} groups={groups} groupID={group !== "all" && group !== "none" ? group : ""} />
              <p><button type="submit">{t("add")}</button></p>
            </form>
          )}
          {editing && editing !== "new" && (
            <DomainCard key={editing.id} domain={editing} groups={groups} onChange={() => { setEditing(null); reload(); }} setError={setError} />
          )}
          <table>
            <thead>
              <tr><th>{t("name")}</th><th>{t("groups")}</th><th>{t("match")}</th><th>{t("strategy")}</th><th></th></tr>
            </thead>
            <tbody>
              {slice.map((d) => (
                <tr key={d.id}>
                  <td className="mono"><span className={d.enabled ? "lamp on" : "lamp off"} />{d.name}</td>
                  <td>
                    <select value={d.group_id || ""} onChange={(e) => assignGroup(d, e.target.value)}>
                      <option value="">{t("ungrouped")}</option>
                      {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
                    </select>
                  </td>
                  <td>{d.match === "fqdn" ? t("fqdn") : t("suffix")}</td>
                  <td>{balanceLabel(d.balance, t)}</td>
                  <td>
                    <button type="button" className="ghost tiny" onClick={() => setEditing(d)}>{t("edit")}</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!filtered.length && <p className="empty">{t("noDomains")}</p>}
          <div className="pager">
            <span>{t("domainCount", { n: filtered.length })}</span>
            <button type="button" className="ghost tiny" disabled={current === 0} onClick={() => setPage(current - 1)}>{t("prev")}</button>
            <span>{current + 1} {t("of")} {pages}</span>
            <button type="button" className="ghost tiny" disabled={current + 1 >= pages} onClick={() => setPage(current + 1)}>{t("nextPage")}</button>
          </div>
        </section>
      </div>
    </>
  );
}

function DomainFields({ proxies, domain, groups, groupID }) {
  const { t } = useI18n();
  const weights = domain?.weights || proxies.map((p) => ({ proxy_id: p.id, proxy_name: p.name, weight: 1, on: false }));
  return (
    <>
      <div className="grid">
        <div><label>{t("name")}</label><input name="name" defaultValue={domain?.name} placeholder="example.com" required /></div>
        <div><label>{t("match")}</label><select name="match" defaultValue={domain?.match || "suffix"}><option value="suffix">{t("suffix")}</option><option value="fqdn">{t("fqdn")}</option></select></div>
        <div><label>{t("strategy")}</label><select name="balance" defaultValue={domain?.balance || "round_robin"}><option value="round_robin">{t("rr")}</option><option value="weighted">{t("weighted")}</option><option value="sticky24">{t("sticky")}</option></select></div>
        <div><label>{t("groups")}</label><select name="group_id" defaultValue={domain?.group_id || groupID || ""}><option value="">{t("ungrouped")}</option>{(groups || []).map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}</select></div>
      </div>
      <div className="grid">
        <div><label>{t("comment")}</label><input name="comment" defaultValue={domain?.comment} /></div>
      </div>
      <p><label className="check"><input type="checkbox" name="enabled" defaultChecked={domain ? domain.enabled : true} />{t("enabled")}</label></p>
      <div className="row-actions">
        {weights.map((w) => (
          <label className="weight check" key={w.proxy_id}>
            <input type="checkbox" name={`on_${w.proxy_id}`} defaultChecked={w.on} />
            {w.proxy_name}
            <input name={`w_${w.proxy_id}`} type="number" defaultValue={w.weight} min="1" max="1000" style={{ width: "4.5rem" }} />
          </label>
        ))}
      </div>
    </>
  );
}

function DomainCard({ domain, groups, onChange, setError }) {
  const { t } = useI18n();
  async function save(event) {
    event.preventDefault();
    try {
      await api(`/api/domains/${domain.id}`, { method: "POST", body: JSON.stringify(domainBody(new FormData(event.target), domain.weights)) });
      onChange();
    } catch (err) { setError(err.message); }
  }
  async function remove() {
    if (!window.confirm(t("confirmDelete", { name: domain.name }))) return;
    try {
      await api(`/api/domains/${domain.id}`, { method: "DELETE" });
      onChange();
    } catch (err) { setError(err.message); }
  }
  return (
    <form onSubmit={save}>
      <DomainFields domain={domain} proxies={[]} groups={groups} />
      <div className="row-actions">
        <button type="submit">{t("save")}</button>
        <button type="button" className="alarm" onClick={remove}>{t("delete")}</button>
      </div>
    </form>
  );
}

function Clients() {
  const { t } = useI18n();
  const { data, error, setError, reload } = useLoad("/api/clients");
  async function create(kind, event) {
    event.preventDefault();
    const form = new FormData(event.target);
    try {
      await api("/api/clients", { method: "POST", body: JSON.stringify({ cidr: form.get("cidr"), label: form.get("label"), list_kind: kind, enabled: form.get("enabled") === "on" }) });
      event.target.reset();
      reload();
    } catch (err) { setError(err.message); }
  }
  async function remove(c) {
    if (!window.confirm(t("confirmDelete", { name: c.cidr }))) return;
    try {
      await api(`/api/clients/${c.id}`, { method: "DELETE" });
      reload();
    } catch (err) { setError(err.message); }
  }
  const rows = data || [];
  return (
    <>
      <Err text={error} />
      <p className="empty">{t("allowHint")}</p>
      <div className="clients">
        {["allow", "deny"].map((kind) => (
          <section className="mod" key={kind}>
            <h2>{kind === "allow" ? t("whitelist") : t("blacklist")}</h2>
            <form onSubmit={(event) => create(kind, event)}>
              <div className="grid">
                <div><label>CIDR</label><input name="cidr" placeholder="198.51.100.10/32" required /></div>
                <div><label>{t("label")}</label><input name="label" /></div>
              </div>
              <p><label className="check"><input type="checkbox" name="enabled" defaultChecked />{t("enabledFem")}</label></p>
              <p><button type="submit">{t("add")}</button></p>
            </form>
            <table>
              <tbody>
                {rows.filter((c) => (c.list_kind || "allow") === kind).map((c) => (
                  <tr key={c.id}>
                    <td className="mono">{c.cidr}</td>
                    <td>{c.label}</td>
                    <td><span className={c.enabled ? "lamp on" : "lamp off"} /></td>
                    <td><button type="button" className="ghost tiny" onClick={() => remove(c)}>{t("delete")}</button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        ))}
      </div>
    </>
  );
}

function Logs() {
  const { t } = useI18n();
  const [q, setQ] = useState("");
  const [ip, setIp] = useState("");
  const [applied, setApplied] = useState({ q: "", ip: "" });
  const { data, error, reload } = useLoad(`/api/logs?q=${encodeURIComponent(applied.q)}&ip=${encodeURIComponent(applied.ip)}`);
  function submit(event) {
    event.preventDefault();
    const next = { q: q.trim(), ip: ip.trim() };
    if (next.q === applied.q && next.ip === applied.ip) reload();
    else setApplied(next);
  }
  return (
    <>
      <Err text={error} />
      <section className="mod">
        <h2>{t("filter")}</h2>
        <form onSubmit={submit}>
          <div className="grid">
            <div><label>{t("name")}</label><input value={q} onChange={(e) => setQ(e.target.value)} /></div>
            <div><label>{t("clientIp")}</label><input value={ip} onChange={(e) => setIp(e.target.value)} placeholder={t("ipPlaceholder")} /></div>
          </div>
          <p><button type="submit">{t("show")}</button></p>
        </form>
      </section>
      <section className="mod">
        <h2>DNS</h2>
        <table>
          <thead><tr><th>{t("time")}</th><th>{t("clientIp")}</th><th>{t("name")}</th><th>{t("type")}</th><th>{t("code")}</th><th>{t("decision")}</th></tr></thead>
          <tbody>{data?.dns?.map((row, i) => <tr key={i}><td>{row.at}</td><td>{row.client_ip}</td><td>{row.name}</td><td>{row.qtype}</td><td>{row.rcode}</td><td>{row.decision}</td></tr>)}</tbody>
        </table>
      </section>
      <section className="mod">
        <h2>{t("proxyTitle")}</h2>
        <p className="empty">{t("noBody")}</p>
        <table>
          <thead><tr><th>{t("time")}</th><th>{t("clientIp")}</th><th>SNI</th><th>{t("bytes")}</th><th>{t("status")}</th><th>Dial</th></tr></thead>
          <tbody>{data?.proxy?.map((row, i) => <tr key={i}><td>{row.at}</td><td>{row.client_ip}</td><td>{row.sni}</td><td>{row.bytes_up} / {row.bytes_down}</td><td>{row.status}</td><td>{row.dial_error}</td></tr>)}</tbody>
        </table>
      </section>
    </>
  );
}

function Settings() {
  const { t } = useI18n();
  const { data, error, setError, reload } = useLoad("/api/settings");
  if (!data) return error ? <Err text={error} /> : null;
  const s = data.settings;
  async function save(event) {
    event.preventDefault();
    const form = new FormData(event.target);
    const body = {};
    for (const [k, v] of form.entries()) body[k] = Number.isNaN(Number(v)) || v === "" || k === "bootstrap_cidr" || k === "agent_image" ? v : Number(v);
    try {
      await api("/api/settings", { method: "POST", body: JSON.stringify(body) });
      reload();
    } catch (err) { setError(err.message); }
  }
  return (
    <>
      <Err text={error} />
      <section className="mod">
        <h2>{t("params")}</h2>
        <form onSubmit={save}>
          <div className="grid">
            <div><label>{t("ttl")}</label><input name="ttl" defaultValue={s.ttl} /></div>
            <div><label>{t("pushInterval")}</label><input name="pull_interval_sec" defaultValue={s.pull_interval_sec} /></div>
            <div><label>{t("retention")}</label><input name="retention_days" defaultValue={s.retention_days} /></div>
            <div><label>Bootstrap CIDR</label><input name="bootstrap_cidr" defaultValue={s.bootstrap_cidr} /></div>
            <div><label>{t("sessionsPerIp")}</label><input name="session_limit" defaultValue={s.session_limit} /></div>
            <div><label>{t("dialMs")}</label><input name="dial_timeout_ms" defaultValue={s.dial_timeout_ms} /></div>
            <div><label>{t("idleMs")}</label><input name="idle_timeout_ms" defaultValue={s.idle_timeout_ms} /></div>
            <div><label>{t("dnsRate")}</label><input name="dns_rate_qps" type="number" min="0" max="100000" defaultValue={s.dns_rate_qps} /></div>
            <div><label>{t("agentImage")}</label><input name="agent_image" defaultValue={s.agent_image} /></div>
          </div>
          <p><button type="submit">{t("save")}</button></p>
        </form>
      </section>
      <section className="mod">
        <h2>Upstream</h2>
        <p className="empty">{t("upstreamHint")}</p>
        {!data.upstreams?.length && <p className="banner note">{t("upstreamEmpty")}</p>}
        <form onSubmit={async (event) => {
          event.preventDefault();
          try {
            await api("/api/upstreams", { method: "POST", body: JSON.stringify({ addr: new FormData(event.target).get("addr") }) });
            event.target.reset();
            reload();
          } catch (err) { setError(err.message); }
        }}>
          <div className="grid"><div><label>{t("address")}</label><input name="addr" placeholder="1.1.1.1:53" required /></div></div>
          <p><button type="submit">{t("add")}</button></p>
        </form>
        {data.upstreams.map((u) => (
          <form key={u.id} onSubmit={async (event) => {
            event.preventDefault();
            const form = new FormData(event.target);
            try {
              await api(`/api/upstreams/${u.id}`, { method: "POST", body: JSON.stringify({ addr: form.get("addr"), ordinal: Number(form.get("ordinal")) }) });
              reload();
            } catch (err) { setError(err.message); }
          }}>
            <div className="grid">
              <div><label>{t("address")}</label><input name="addr" defaultValue={u.addr} /></div>
              <div><label>{t("order")}</label><input name="ordinal" defaultValue={u.ordinal} /></div>
            </div>
            <div className="row-actions">
              <button type="submit">{t("save")}</button>
              <button className="alarm" type="button" onClick={async () => {
                if (!window.confirm(t("confirmDelete", { name: u.addr }))) return;
                try {
                  await api(`/api/upstreams/${u.id}`, { method: "DELETE" });
                  reload();
                } catch (err) { setError(err.message); }
              }}>{t("delete")}</button>
            </div>
          </form>
        ))}
      </section>
    </>
  );
}

function Audit() {
  const { t } = useI18n();
  const { data, error } = useLoad("/api/audit");
  return (
    <section className="mod">
      <h2>{t("auditTitle")}</h2>
      <Err text={error} />
      <table>
        <thead><tr><th>{t("time")}</th><th>{t("who")}</th><th>{t("action")}</th><th>{t("detail")}</th></tr></thead>
        <tbody>{(data || []).map((row, i) => <tr key={i}><td>{row.at}</td><td>{row.actor}</td><td>{row.action}</td><td>{row.detail}</td></tr>)}</tbody>
      </table>
    </section>
  );
}

function nodeBody(form) {
  return {
    name: form.get("name"),
    role: form.get("role"),
    public_ipv4: form.get("public_ipv4") || "",
    public_ipv6: form.get("public_ipv6") || "",
    region: form.get("region") || "",
    agent_host: form.get("agent_host"),
    agent_port: Number(form.get("agent_port")),
    enabled: form.get("enabled") === "on",
  };
}

function domainPayload(domain, groupID) {
  return {
    name: domain.name,
    match: domain.match,
    balance: domain.balance,
    comment: domain.comment || "",
    enabled: domain.enabled,
    group_id: groupID,
    links: (domain.weights || []).filter((w) => w.on).map((w) => ({ proxy_id: w.proxy_id, weight: w.weight })),
  };
}

function domainBody(form, proxies) {
  const links = [];
  for (const p of proxies) {
    const id = p.proxy_id || p.id;
    if (form.get(`on_${id}`) === "on") {
      links.push({ proxy_id: id, weight: Number(form.get(`w_${id}`) || 1) });
    }
  }
  return {
    name: form.get("name"),
    match: form.get("match"),
    balance: form.get("balance"),
    comment: form.get("comment") || "",
    enabled: form.get("enabled") === "on",
    group_id: form.get("group_id") || "",
    links,
  };
}

function ipOf(n) {
  if (n.public_ipv4 && n.public_ipv6) return `${n.public_ipv4} / ${n.public_ipv6}`;
  return n.public_ipv4 || n.public_ipv6 || "—";
}

function balanceLabel(s, t) {
  if (s === "weighted") return t("weighted");
  if (s === "sticky24") return t("sticky");
  return t("rr");
}

function formatBytes(n) {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB"];
  let v = n;
  for (const u of units) {
    v /= 1024;
    if (v < 1024) return `${v.toFixed(1)} ${u}`;
  }
  return `${(v / 1024).toFixed(1)} TB`;
}
