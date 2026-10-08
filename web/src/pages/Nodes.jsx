import { Fragment, useEffect, useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { Err, StatusLamp, SkeletonRows, EmptyRow } from "../components/Bits";
import { useConfirm } from "../components/Toast";

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

export function Nodes() {
  const { t, err } = useI18n();
  const [data, setData] = useState(null);
  const [error, setError] = useState("");
  const [open, setOpen] = useState(null);
  const [ask, confirmNode] = useConfirm();
  async function reload() {
    try {
      setData(await api("/api/nodes"));
      setError("");
    } catch (e) { setError(e.message); }
  }
  useEffect(() => { reload(); }, []);
  const nodes = data || [];
  const editing = open?.mode === "edit" ? nodes.find((n) => n.id === open.node.id) || open.node : null;
  return (
    <>
      {confirmNode}
      <Err text={error} />
      <section className="mod">
        <div className="toolbar">
          <h2>{t("nodes")}</h2>
          <button type="button" onClick={() => setOpen({ mode: "new" })}>{t("newNode")}</button>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t("name")}</th><th>{t("role")}</th><th>{t("publicIp")}</th><th>{t("agent")}</th><th>{t("state")}</th><th></th>
              </tr>
            </thead>
            <tbody>
              {data === null && <SkeletonRows cols={6} />}
              {data !== null && (nodes.length ? nodes.map((n) => (
                <tr key={n.id}>
                  <td><StatusLamp on={n.fresh} />{n.name}</td>
                  <td>{n.role}</td>
                  <td className="mono">{ipOf(n)}</td>
                  <td className="mono">{n.agent_host}:{n.agent_port}</td>
                  <td>
                    {n.last_error ? err(n.last_error) : (n.fresh ? t("online") : t("offline"))}
                    {n.needs_update && <span className="banner note" style={{ display: "inline", marginLeft: 8, padding: "1px 6px" }}>{t("needsUpdate")}</span>}
                  </td>
                  <td><button type="button" className="ghost tiny" onClick={() => setOpen({ mode: "edit", node: n })}>{t("edit")}</button></td>
                </tr>
              )) : <EmptyRow colSpan={6}>{t("noNodes")}</EmptyRow>)}
            </tbody>
          </table>
        </div>
      </section>
      {open && (
        <NodeModal
          mode={open.mode}
          node={editing}
          onClose={() => setOpen(null)}
          onChange={reload}
          setError={setError}
          ask={ask}
        />
      )}
    </>
  );
}

function NodeModal({ mode, node, onClose, onChange, setError, ask }) {
  const { t, err } = useI18n();
  const [step, setStep] = useState(0);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState(null);
  const [connect, setConnect] = useState(null);
  const [copied, setCopied] = useState("");
  const [msg, setMsg] = useState("");
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
    ask(t("confirmRotate", { name: node.name }), async () => {
      try {
        const out = await api(`/api/nodes/${node.id}/key`, { method: "POST" });
        setMsg(t("newKeyOnce", { key: out.key }));
      } catch (e) { setError(e.message); }
    });
  }
  async function remove() {
    ask(t("confirmDelete", { name: node.name }), async () => {
      try {
        await api(`/api/nodes/${node.id}`, { method: "DELETE" });
        onChange();
        onClose();
      } catch (e) { setError(e.message); }
    });
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
              <p className="empty"><StatusLamp on={node.fresh} />{node.fresh ? t("online") : t("offline")}{node.last_error ? ` · ${err(node.last_error)}` : ""}</p>
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

function ipOf(n) {
  if (n.public_ipv4 && n.public_ipv6) return `${n.public_ipv4} / ${n.public_ipv6}`;
  return n.public_ipv4 || n.public_ipv6 || "—";
}
