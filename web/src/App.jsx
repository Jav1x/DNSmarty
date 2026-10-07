import { useEffect, useState } from "react";
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
    <div className="rack">
      <header className="mast">
        <div className="brand"><b>DNSmarty</b><span>control plane</span></div>
        <nav>
          {nav.map(([to, key]) => (
            <NavLink key={to} to={to} end={to === "/"} className={({ isActive }) => (isActive ? "on" : "")}>{t(key)}</NavLink>
          ))}
        </nav>
        <div className="who"><LangSwitch /><span>{user}</span><button className="ghost" onClick={logout}>{t("logout")}</button></div>
      </header>
      <Outlet />
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
  const cols = o.bay_proxies?.length || 0;
  return (
    <>
      <section className="meters">
        <div className="meter"><i>{t("qps")}</i><b>{o.qps}</b></div>
        <div className="meter"><i>{t("sessions60")}</i><b>{o.sessions}</b></div>
        <div className="meter"><i>{t("bytes24")}</i><b>{formatBytes(o.bytes)}</b></div>
        <div className="meter"><i>{t("acl60")}</i><b>{o.refused}</b></div>
        <div className="meter"><i>Stale</i><b>{o.stale}</b></div>
      </section>
      <section className="bay" style={{ "--n": cols }}>
        <h2>{t("patch")}</h2>
        {o.rows?.length ? (
          <>
            <div className="bay-grid">
              <div className="bay-head"><span>{t("domain")}</span>{o.bay_proxies.map((p) => <span key={p.id}>{p.name}</span>)}</div>
              {o.rows.map((row) => (
                <div className="bay-row" key={row.name}>
                  <div className="dname">{row.name} <span className="empty">{balanceLabel(row.balance, t)}</span></div>
                  {(row.cells || []).map((cell, i) => <div className="cell" key={i}><span className={`jack ${cell.state}`} /></div>)}
                </div>
              ))}
            </div>
            <div className="legend"><span><i className="live" />{t("legendLive")}</span><span><i className="dead" />{t("legendDead")}</span><span><i className="empty" />{t("legendEmpty")}</span></div>
          </>
        ) : <p className="empty">{t("noDomains")}</p>}
      </section>
      <section className="mod">
        <h2>{t("nodeCol")}</h2>
        <NodeTable nodes={data.nodes} />
      </section>
      <p className="banner note">{t("noUdp")}</p>
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
  const [step, setStep] = useState(0);
  const [created, setCreated] = useState(null);
  const [connect, setConnect] = useState(null);
  async function create(event) {
    event.preventDefault();
    const form = new FormData(event.target);
    try {
      const out = await api("/api/nodes", { method: "POST", body: JSON.stringify(nodeBody(form)) });
      setCreated(out);
      setStep(2);
      setConnect(null);
      reload();
    } catch (err) {
      setError(err.message);
    }
  }
  async function next() {
    const out = await api(`/api/nodes/${created.node.id}/connect`, { method: "POST" });
    setConnect(out);
    setStep(3);
    reload();
  }
  return (
    <>
      <Err text={error} />
      <section className="mod">
        <h2>{t("newNode")}</h2>
        <div className="steps"><span className={step < 2 ? "on" : ""}>{t("stepAddr")}</span><span className={step === 2 ? "on" : ""}>{t("stepKey")}</span><span className={step === 3 ? "on" : ""}>{t("stepLink")}</span></div>
        {step < 2 && (
          <form onSubmit={create}>
            <div className="grid">
              <div><label>{t("name")}</label><input name="name" required /></div>
              <div><label>{t("role")}</label><select name="role"><option value="dns">dns</option><option value="proxy">proxy</option></select></div>
              <div><label>{t("ipv4")}</label><input name="public_ipv4" placeholder="203.0.113.10" /></div>
              <div><label>{t("ipv6")}</label><input name="public_ipv6" /></div>
              <div><label>{t("region")}</label><input name="region" /></div>
              <div><label>{t("agentHost")}</label><input name="agent_host" placeholder="203.0.113.10" required /></div>
              <div><label>{t("agentPort")}</label><input name="agent_port" defaultValue="9443" required /></div>
              <div><label>{t("inRotation")}</label><label className="check"><input type="checkbox" name="enabled" defaultChecked />{t("enabled")}</label></div>
            </div>
            <p className="empty">{t("nodeHint")}</p>
            <p><button type="submit">{t("issueKey")}</button></p>
          </form>
        )}
        {step === 2 && created && (
          <div>
            <p>{t("keyOnce", { name: created.node.name })}</p>
            <p className="token">{created.key}</p>
            <p className="empty">{t("scriptHint", { port: created.node.agent_port })}</p>
            <pre className="banner note">{`DNSMARTY_IMAGE=${created.image || "dnsmarty:local"} sh install-node.sh ${created.node.role}`}</pre>
            <p><a href="/install/node.sh">{t("downloadScript")}</a></p>
            <div className="row-actions">
              <button onClick={next}>{t("next")}</button>
              <button className="ghost" onClick={() => setStep(0)}>{t("anotherNode")}</button>
            </div>
          </div>
        )}
        {step === 3 && connect && (
          <div>
            {connect.ok
              ? <div className="banner ok">{t("linked")}</div>
              : <div className="banner err">{t("notLinked", { error: err(connect.error) })}</div>}
            <button onClick={() => { setStep(0); setCreated(null); }}>{t("close")}</button>
          </div>
        )}
      </section>
      {(data || []).map((n) => <NodeCard key={n.id} node={n} onChange={reload} setError={setError} />)}
    </>
  );
}

function NodeCard({ node, onChange, setError }) {
  const { t, err } = useI18n();
  const [msg, setMsg] = useState("");
  async function save(event) {
    event.preventDefault();
    try {
      await api(`/api/nodes/${node.id}`, { method: "POST", body: JSON.stringify(nodeBody(new FormData(event.target))) });
      setMsg(t("saved"));
      onChange();
    } catch (e) { setError(e.message); }
  }
  async function connect() {
    const out = await api(`/api/nodes/${node.id}/connect`, { method: "POST" });
    setMsg(out.ok ? t("linkedShort") : err(out.error));
    onChange();
  }
  async function rotate() {
    const out = await api(`/api/nodes/${node.id}/key`, { method: "POST" });
    setMsg(t("newKeyOnce", { key: out.key }));
  }
  async function remove() {
    await api(`/api/nodes/${node.id}`, { method: "DELETE" });
    onChange();
  }
  return (
    <section className="mod">
      <h2>{node.name}</h2>
      {msg && <div className="banner note">{msg}</div>}
      <form onSubmit={save}>
        <div className="grid">
          <div><label>{t("name")}</label><input name="name" defaultValue={node.name} required /></div>
          <div><label>{t("role")}</label><select name="role" defaultValue={node.role}><option value="dns">dns</option><option value="proxy">proxy</option></select></div>
          <div><label>{t("ipv4")}</label><input name="public_ipv4" defaultValue={node.public_ipv4} /></div>
          <div><label>{t("ipv6")}</label><input name="public_ipv6" defaultValue={node.public_ipv6} /></div>
          <div><label>{t("region")}</label><input name="region" defaultValue={node.region} /></div>
          <div><label>{t("agentHost")}</label><input name="agent_host" defaultValue={node.agent_host} required /></div>
          <div><label>{t("agentPort")}</label><input name="agent_port" defaultValue={node.agent_port} required /></div>
          <div><label>{t("inRotation")}</label><label className="check"><input type="checkbox" name="enabled" defaultChecked={node.enabled} />{t("enabled")}</label></div>
        </div>
        <p className="empty"><span className={`lamp ${node.fresh ? "on" : "off"}`} />{node.fresh ? t("online") : t("offline")}{node.last_error ? ` · ${err(node.last_error)}` : ""}</p>
        <div className="row-actions"><button type="submit">{t("save")}</button></div>
      </form>
      <div className="row-actions">
        <button className="ghost" onClick={connect}>{t("checkLink")}</button>
        <button className="ghost" onClick={rotate}>{t("newKey")}</button>
        <button className="alarm" onClick={remove}>{t("deleteName", { name: node.name })}</button>
      </div>
    </section>
  );
}

function Domains() {
  const { t } = useI18n();
  const { data, error, setError, reload } = useLoad("/api/domains");
  if (!data) return error ? <Err text={error} /> : null;
  async function create(event) {
    event.preventDefault();
    try {
      await api("/api/domains", { method: "POST", body: JSON.stringify(domainBody(new FormData(event.target), data.proxies)) });
      event.target.reset();
      reload();
    } catch (err) { setError(err.message); }
  }
  return (
    <>
      <Err text={error} />
      <section className="mod">
        <h2>{t("newDomain")}</h2>
        <p className="empty">{t("seedHint")}</p>
        <form onSubmit={create}>
          <DomainFields proxies={data.proxies} />
          <p><button type="submit">{t("add")}</button></p>
        </form>
      </section>
      {data.domains.map((d) => <DomainCard key={d.id} domain={d} onChange={reload} setError={setError} />)}
    </>
  );
}

function DomainFields({ proxies, domain }) {
  const { t } = useI18n();
  const weights = domain?.weights || proxies.map((p) => ({ proxy_id: p.id, proxy_name: p.name, weight: 1, on: false }));
  return (
    <>
      <div className="grid">
        <div><label>{t("name")}</label><input name="name" defaultValue={domain?.name} placeholder="example.com" required /></div>
        <div><label>{t("match")}</label><select name="match" defaultValue={domain?.match || "suffix"}><option value="suffix">{t("suffix")}</option><option value="fqdn">{t("fqdn")}</option></select></div>
        <div><label>{t("strategy")}</label><select name="balance" defaultValue={domain?.balance || "round_robin"}><option value="round_robin">{t("rr")}</option><option value="weighted">{t("weighted")}</option><option value="sticky24">{t("sticky")}</option></select></div>
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

function DomainCard({ domain, onChange, setError }) {
  const { t } = useI18n();
  async function save(event) {
    event.preventDefault();
    try {
      await api(`/api/domains/${domain.id}`, { method: "POST", body: JSON.stringify(domainBody(new FormData(event.target), domain.weights)) });
      onChange();
    } catch (err) { setError(err.message); }
  }
  return (
    <section className="mod">
      <h2>{domain.name}</h2>
      <form onSubmit={save}>
        <DomainFields domain={domain} proxies={[]} />
        <p><button type="submit">{t("save")}</button></p>
      </form>
      <button className="alarm" onClick={async () => { await api(`/api/domains/${domain.id}`, { method: "DELETE" }); onChange(); }}>{t("deleteName", { name: domain.name })}</button>
    </section>
  );
}

function Clients() {
  const { t } = useI18n();
  const { data, error, setError, reload } = useLoad("/api/clients");
  async function create(event) {
    event.preventDefault();
    const form = new FormData(event.target);
    try {
      await api("/api/clients", { method: "POST", body: JSON.stringify({ cidr: form.get("cidr"), label: form.get("label"), enabled: form.get("enabled") === "on" }) });
      event.target.reset();
      reload();
    } catch (err) { setError(err.message); }
  }
  return (
    <>
      <Err text={error} />
      <section className="mod">
        <h2>{t("allowNet")}</h2>
        <p className="empty">{t("allowHint")}</p>
        <form onSubmit={create}>
          <div className="grid">
            <div><label>CIDR</label><input name="cidr" placeholder="198.51.100.10/32" required /></div>
            <div><label>{t("label")}</label><input name="label" /></div>
            <div><label>{t("state")}</label><label className="check"><input type="checkbox" name="enabled" defaultChecked />{t("enabledFem")}</label></div>
          </div>
          <p><button type="submit">{t("add")}</button></p>
        </form>
      </section>
      {(data || []).map((c) => (
        <section className="mod" key={c.id}>
          <form onSubmit={async (event) => {
            event.preventDefault();
            const form = new FormData(event.target);
            try {
              await api(`/api/clients/${c.id}`, { method: "POST", body: JSON.stringify({ cidr: form.get("cidr"), label: form.get("label"), enabled: form.get("enabled") === "on" }) });
              reload();
            } catch (err) { setError(err.message); }
          }}>
            <div className="grid">
              <div><label>CIDR</label><input name="cidr" defaultValue={c.cidr} required /></div>
              <div><label>{t("label")}</label><input name="label" defaultValue={c.label} /></div>
              <div><label>{t("state")}</label><label className="check"><input type="checkbox" name="enabled" defaultChecked={c.enabled} />{t("enabledFem")}</label></div>
            </div>
            <div className="row-actions"><button type="submit">{t("save")}</button></div>
          </form>
          <button className="alarm" onClick={async () => { await api(`/api/clients/${c.id}`, { method: "DELETE" }); reload(); }}>{t("delete")}</button>
        </section>
      ))}
    </>
  );
}

function Logs() {
  const { t } = useI18n();
  const [q, setQ] = useState("");
  const [ip, setIp] = useState("");
  const { data, error, reload } = useLoad(`/api/logs?q=${encodeURIComponent(q)}&ip=${encodeURIComponent(ip)}`);
  return (
    <>
      <Err text={error} />
      <section className="mod">
        <h2>{t("filter")}</h2>
        <form onSubmit={(e) => { e.preventDefault(); reload(); }}>
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
              <button className="alarm" type="button" onClick={async () => { await api(`/api/upstreams/${u.id}`, { method: "DELETE" }); reload(); }}>{t("delete")}</button>
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
