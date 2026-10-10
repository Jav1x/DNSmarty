import { useEffect, useMemo, useState } from "react";
import { api } from "../api";
import { useLoad } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { Err, EmptyRow, SkeletonRows } from "../components/Bits";
import { useConfirm, useToast } from "../components/Toast";
import Lamp from "../ui/Lamp";
import Modal from "../ui/Modal";
import Switch from "../ui/Switch";
import Tip from "../ui/Tip";
import TrashButton from "../ui/TrashButton";
import { moveId } from "../lib/nodes";
import { ago, fmtBytes, fmtMbps, fmtUptime, hwRows } from "../lib/util";
import NewNodeModal from "./nodes/NewNodeModal.jsx";

function nodeInput(n, enabled) {
  return {
    name: String(n.name || "").trim(),
    role: n.role,
    public_ipv4: n.public_ipv4 || "",
    public_ipv6: n.public_ipv6 || "",
    region: n.region || "",
    agent_host: String(n.agent_host || "").trim(),
    agent_port: Number(n.agent_port),
    enabled: Boolean(enabled),
  };
}

function nodeConnect(id) {
  return api(`/api/nodes/${id}/connect`, { method: "POST" });
}

export function Nodes() {
  const { t, err } = useI18n();
  const { data, loading, error, setError, reload } = useLoad("/api/nodes");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(() => new Set());
  const [dragId, setDragId] = useState(null);
  const [overId, setOverId] = useState(null);
  const [order, setOrder] = useState(null);
  const [open, setOpen] = useState(null);
  const [ask, confirmRow] = useConfirm();
  const toast = useToast();

  const nodes = data || [];
  useEffect(() => { setOrder(null); }, [data]);

  const placed = useMemo(() => {
    if (!order) return nodes;
    const ids = [...order.filter((id) => nodes.some((n) => n.id === id)),
      ...nodes.filter((n) => !order.includes(n.id)).map((n) => n.id)];
    return ids.map((id) => nodes.find((n) => n.id === id));
  }, [nodes, order]);

  const q = query.trim().toLowerCase();
  const list = q
    ? placed.filter((n) =>
        n.name.toLowerCase().includes(q) ||
        (n.region || "").toLowerCase().includes(q) ||
        `${n.agent_host}:${n.agent_port}`.toLowerCase().includes(q) ||
        `${n.public_ipv4}${n.public_ipv6}`.toLowerCase().includes(q))
    : placed;
  const editing = open?.kind === "edit" ? nodes.find((n) => n.id === open.id) : null;

  function toggle(id) {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id); else next.add(id);
    setSelected(next);
  }
  const selNodes = nodes.filter((n) => selected.has(n.id));
  const allSelected = list.length > 0 && list.every((n) => selected.has(n.id));

  async function withMutation(run, done = t("saved")) {
    try {
      await run();
      setSelected(new Set());
      reload();
      toast(done);
    } catch (e) { setError(e); reload(); }
  }
  function bulkEnabled(next) {
    const targets = selNodes.filter((n) => n.enabled !== next);
    if (!targets.length) return;
    withMutation(async () => {
      for (const n of targets) {
        await api(`/api/nodes/${n.id}`, { method: "POST", body: JSON.stringify(nodeInput(n, next)) });
      }
    });
  }
  function bulkDelete() {
    const ids = selNodes.map((n) => n.id);
    ask(t("bulkDeleteConfirm", { n: ids.length }), () => {
      withMutation(async () => {
        for (const id of ids) await api(`/api/nodes/${id}`, { method: "DELETE" });
      }, t("deleted"));
    });
  }
  function removeRow(n) {
    ask(t("confirmDelete", { name: n.name }), () => {
      withMutation(async () => {
        await api(`/api/nodes/${n.id}`, { method: "DELETE" });
      }, t("deleted"));
    });
  }
  async function linkRow(n) {
    try {
      const out = await nodeConnect(n.id);
      toast(out.ok ? t("linkedShort") : t("notLinked", { error: err(out.error) }));
    } catch (e) { setError(e); }
  }
  function reorder(dropId) {
    const ids0 = order || nodes.map((n) => n.id);
    const ids = moveId(ids0, dragId, dropId);
    if (ids === ids0) return;
    setOrder(ids);
    api("/api/nodes/order", { method: "PUT", body: JSON.stringify({ ids }) }).catch((e) => {
      setError(e);
      setOrder(null);
      reload();
    });
  }

  return (
    <>
      {confirmRow}
      <div className="hrow">
        <h1>{t("nodes")}</h1>
        <span className="toolbar">
          <input className="finput search" placeholder={t("searchNodesPh")}
            aria-label={t("searchNodes")} value={query} onChange={(e) => setQuery(e.target.value)} />
          <button type="button" className="btn" onClick={() => setOpen({ kind: "new" })}>+ {t("newNode")}</button>
        </span>
      </div>
      <Err text={error} />

      {selNodes.length > 0 && (
        <div className="bulkbar">
          {t("bulkSel", { n: selNodes.length })}
          <button type="button" className="btn ghost sm" onClick={() => bulkEnabled(true)}>{t("toRot")}</button>
          <button type="button" className="btn ghost sm" onClick={() => bulkEnabled(false)}>{t("fromRot")}</button>
          <button type="button" className="btn ghost sm bulkdel" onClick={bulkDelete}>{t("delete")}</button>
        </div>
      )}

      <div className="tablecard">
        <div className="tscroll">
          <table>
            <thead><tr>
              <th className="grab">⠿</th>
              <th style={{ width: 26 }}>
                <input type="checkbox" checked={allSelected} aria-label={t("filterAll")}
                  onChange={() => setSelected(allSelected ? new Set() : new Set(list.map((n) => n.id)))} />
              </th>
              <th>{t("nCol")}</th>
              <th>{t("statusCol")}</th>
              <th>{t("contactCol")}</th>
              <th>{t("publicIp")}</th>
              <th>{t("agentHost")}</th>
              <th>{t("role")}</th>
              <th>{t("region")}</th>
              <th>{t("agent")}</th>
              <th>{t("rotCol")}</th>
              <th>{t("q24")}</th>
              <th></th>
            </tr></thead>
            <tbody>
              {loading && !data && <SkeletonRows cols={13} />}
              {data !== null && list.map((n) => {
                const st = statusOf(n, t, err);
                const seen = n.last_seen_at ? ago(n.last_seen_at, t) : "—";
                return (
                  <tr
                    key={n.id}
                    draggable
                    onDragStart={() => setDragId(n.id)}
                    onDragEnd={() => { setDragId(null); setOverId(null); }}
                    onDragOver={(e) => { e.preventDefault(); if (n.id !== dragId) setOverId(n.id); }}
                    onDrop={(e) => {
                      e.preventDefault();
                      if (dragId && dragId !== n.id) reorder(n.id);
                      setDragId(null);
                      setOverId(null);
                    }}
                    className={[
                      dragId === n.id ? "drag" : "",
                      overId === n.id && dragId !== n.id ? "drop" : "",
                      !n.enabled ? "disrow" : "",
                      selected.has(n.id) ? "sel" : "",
                    ].filter(Boolean).join(" ")}
                    onDoubleClick={(e) => {
                      if (!e.target.closest("button, input, select, td.drag")) setOpen({ kind: "edit", id: n.id });
                    }}
                  >
                    <td className="drag">⠿</td>
                    <td>
                      <input type="checkbox" checked={selected.has(n.id)} onChange={() => toggle(n.id)} aria-label={n.name} />
                    </td>
                    <td className="c-name">
                      {n.region ? <span className="flag" aria-hidden="true">{monoFlag(n.region)}</span> : null}
                      <b>{n.name}</b>
                      {!n.enabled && <span className="dismark">{t("rotDis")}</span>}
                    </td>
                    <td><Lamp state={st.lamp} tip={st.tip} /></td>
                    <td>{seen}</td>
                    <td>{ipOf(n)}</td>
                    <td className="addr"><b>{n.agent_host}</b>:{n.agent_port}</td>
                    <td>{n.role}</td>
                    <td>{n.region || "—"}</td>
                    <td>
                      {n.agent_version
                        ? (n.needs_update
                          ? <Tip label={t("needsUpdate")}><span className="ver old">{n.agent_version}</span></Tip>
                          : <span className="ver"><b>{n.agent_version}</b></span>)
                        : <span className="ver">—</span>}
                    </td>
                    <td>
                      <span className={`rot ${n.enabled && n.fresh ? "on" : ""}`}>
                        {!n.enabled ? t("rotDis") : n.fresh ? t("rotIn") : t("rotOut")}
                      </span>
                    </td>
                    <td className="num">{Number(n.queries_24h || 0).toLocaleString()}</td>
                    <td>
                      <span className="acts">
                        <button type="button" className="abtn" title={t("edit")} onClick={() => setOpen({ kind: "edit", id: n.id })}>✎</button>
                        <button type="button" className="abtn" title={t("checkLink")} onClick={() => linkRow(n)}>⟳</button>
                        <button type="button" className="abtn del" title={t("delete")} onClick={() => removeRow(n)}>✕</button>
                      </span>
                    </td>
                  </tr>
                );
              })}
              {data !== null && list.length === 0 && <EmptyRow colSpan={13}>{t("noNodes")}</EmptyRow>}
            </tbody>
          </table>
        </div>
      </div>
      <div className="hint">{t("hintDrag")}</div>

      {open?.kind === "edit" && editing && (
        <EditNodeModal
          node={editing}
          onClose={() => setOpen(null)}
          onSaved={() => { setOpen(null); reload(); }}
          setError={setError}
          ask={ask}
          toast={toast}
        />
      )}
      {open?.kind === "new" && (
        <NewNodeModal nodes={nodes} onClose={() => setOpen(null)} reload={reload} setError={setError} />
      )}
    </>
  );
}

function statusOf(n, t, err) {
  if (!n.enabled) return { lamp: "dis", tip: t("disLampTip") };
  const seen = n.last_seen_at ? ago(n.last_seen_at, t) : "—";
  if (n.fresh) {
    return {
      lamp: n.needs_update ? "warn" : "on",
      tip: n.needs_update ? t("warnLampTip", { ago: seen }) : t("onlineTip", { ago: seen }),
    };
  }
  const bad = err(n.last_error);
  return { lamp: "off", tip: t("offlineTip", { ago: seen }) + (bad ? `\n${bad}` : "") };
}

function monoFlag(region) {
  return region.slice(0, 2).toUpperCase();
}

function ipOf(n) {
  if (n.public_ipv4 && n.public_ipv6) {
    return <>{n.public_ipv4}<br /><span className="ipsmall">{n.public_ipv6}</span></>;
  }
  return n.public_ipv4 || n.public_ipv6 || "—";
}

function EditNodeModal({ node, onClose, onSaved, setError, ask, toast }) {
  const { t, err } = useI18n();
  const [draft, setDraft] = useState(() => nodeInput(node, node.enabled));
  const [base] = useState(() => nodeInput(node, node.enabled));
  const [key, setKey] = useState("");
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  const set = (k) => (e) => setDraft({ ...draft, [k]: e.target.value });

  const CMP = ["name", "role", "public_ipv4", "public_ipv6", "region", "agent_host", "enabled"];
  const dirty = CMP.filter((k) => String(draft[k] ?? "") !== String(base[k] ?? "")).length
    + (Number(draft.agent_port) !== Number(base.agent_port) ? 1 : 0);
  const port = Number(draft.agent_port);
  const canSave = dirty > 0 && Number.isInteger(port) && port >= 1 && port <= 65535
    && draft.name.trim() && draft.agent_host.trim() && !busy;

  async function save() {
    if (!canSave) return;
    setBusy(true);
    try {
      await api(`/api/nodes/${node.id}`, { method: "POST", body: JSON.stringify(nodeInput(draft, draft.enabled)) });
      toast(t("saved"));
      onSaved();
    } catch (e) { setError(e); }
    finally { setBusy(false); }
  }
  function remove() {
    ask(t("confirmDelete", { name: node.name }), async () => {
      try {
        await api(`/api/nodes/${node.id}`, { method: "DELETE" });
        onSaved();
      } catch (e) { setError(e); }
    });
  }
  async function link() {
    try {
      const out = await nodeConnect(node.id);
      toast(out.ok ? t("linkedShort") : t("notLinked", { error: err(out.error) }));
    } catch (e) { setError(e); }
  }
  function rotate() {
    ask(t("confirmRotate", { name: node.name }), async () => {
      try {
        const out = await api(`/api/nodes/${node.id}/key`, { method: "POST" });
        setKey(out.key);
      } catch (e) { setError(e); }
    }, false);
  }
  async function copyKey() {
    try {
      await navigator.clipboard.writeText(key);
      setCopied(true);
      setTimeout(() => setCopied(false), 1400);
    } catch {}
  }

  const seen = node.last_seen_at ? ago(node.last_seen_at, t) : "—";
  const st = statusOf(node, t, err);
  const stateText = !node.enabled
    ? t("rotDis")
    : node.fresh ? `${t("online")} · ${seen}` : t("offlineTip", { ago: seen });
  const hw = node.last_hw && typeof node.last_hw === "object" ? node.last_hw : {};
  const iface = hw.iface;

  return (
    <Modal
      open
      onClose={onClose}
      width={860}
      title={<>{t("editing")} · <span style={{ color: "var(--accent)" }}>{node.name}</span></>}
      note={dirty ? <span className="dirty">● {t("unsaved", { n: dirty })}</span> : null}
      footer={(
        <>
          <TrashButton tip={t("trashNodeTip")} onClick={remove} disabled={busy} />
          <button type="button" className="btn ghost" onClick={onClose}>{t("cancel")}</button>
          <button type="button" className="btn" onClick={save} disabled={!canSave}>{t("save")}</button>
        </>
      )}
    >

      <section className="sect">
        <h3>{t("stateSect")}<small>{hw.uptime_sec != null ? fmtUptime(hw.uptime_sec) : t("stateSectSub")}</small></h3>
        <div className="sbody">
          <div className="scol">
            <div className="col">
              <div className="kv"><span>{t("statusCol")}</span><b><Lamp state={st.lamp} /> {stateText}</b></div>
              <div className="kv"><span>{t("agent")}</span>{node.agent_version
                ? <b className={node.needs_update ? "ver old" : "ver"}>{node.agent_version}</b>
                : <b>—</b>}</div>
              <div className="kv"><span>{t("q24")}</span><b>{Number(node.queries_24h || 0).toLocaleString()}</b></div>
              <div className="kv"><span>{t("proxySessKv")}</span><b>{Number(node.sessions_24h || 0).toLocaleString()}</b></div>
              <div className="kv"><span>{t("bytes24")}</span><b>{fmtBytes(node.bytes_24h || 0)}</b></div>
            </div>
            <div className="col">
              {hwRows(hw).map(([k, v]) => (
                <div className="kv" key={k}><span>{t(k)}</span><b>{v}</b></div>
              ))}
            </div>
          </div>
          {iface && (
            <div className="mainif">
              <span className="mic"><b>{iface.name || "—"}</b></span>
              <span className="pill on">{t("ifMain")}</span>
              <span className="ipv">{iface.ip || "—"}</span>
              <span className="ifload">↓ {fmtMbps(iface.rx_mbps)} <span className="sep">/</span> ↑ {fmtMbps(iface.tx_mbps)}</span>
              <span className="ifok">{t("ifUp")}</span>
            </div>
          )}
          {!node.last_hw && <div className="hint">{t("stateHwWait")}</div>}
        </div>
      </section>

      <section className="sect">
        <h3>{t("vitalsSect")}<small>{t("vitalsSectSub")}</small></h3>
        <div className="sbody f">
          <div className="frow">
            <div>
              <label htmlFor="nd-name">{t("name")}</label>
              <input id="nd-name" value={draft.name} onChange={set("name")} spellCheck={false} />
              <span className="hint">{t("nameHint")}</span>
            </div>
            <div>
              <label htmlFor="nd-region">{t("region")}</label>
              <input id="nd-region" value={draft.region} onChange={set("region")} spellCheck={false} />
              <span className="hint">{t("regionHint")}</span>
            </div>
          </div>
          <div className="frow">
            <div>
              <label htmlFor="nd-v4">{t("ipv4")}</label>
              <input id="nd-v4" value={draft.public_ipv4} onChange={set("public_ipv4")} placeholder="198.51.100.10" spellCheck={false} />
              <span className="hint">{t("ipv4Hint")}</span>
            </div>
            <div>
              <label htmlFor="nd-v6">{t("ipv6")}</label>
              <input id="nd-v6" value={draft.public_ipv6} onChange={set("public_ipv6")} placeholder="2001:db8::10" spellCheck={false} />
              <span className="hint">{t("ipv6Hint")}</span>
            </div>
          </div>
          <div className="frow3">
            <div>
              <label htmlFor="nd-host">{t("agentHost")}</label>
              <input id="nd-host" value={draft.agent_host} onChange={set("agent_host")} required />
            </div>
            <div>
              <label htmlFor="nd-port">{t("agentPort")}</label>
              <input id="nd-port" type="number" min="1" max="65535" value={draft.agent_port} onChange={set("agent_port")} required />
            </div>
            <div>
              <label htmlFor="nd-role">{t("role")}</label>
              <select id="nd-role" value={draft.role} onChange={set("role")}>
                <option value="dns">dns</option>
                <option value="proxy">proxy</option>
              </select>
            </div>
          </div>
          <div className="swrow">
            <span className="t"><b>{t("inRotation")}</b><span>{t("inRotSub")}</span></span>
            <Switch on={draft.enabled} onChange={(v) => setDraft({ ...draft, enabled: v })} label={t("inRotation")} />
          </div>
          <div className="offbox">
            <div className="swrow">
              <span className="t"><b>{t("offNode")}</b><span>{t("offNodeSub")}</span></span>
              <Switch on={!draft.enabled} onChange={(v) => setDraft({ ...draft, enabled: !v })} label={t("offNode")} />
            </div>
            <div className="hint">{t("offNodeHint")}</div>
          </div>
        </div>
      </section>

      <section className="sect">
        <h3>{t("keySect")}<small>{t("keySectSub")}</small></h3>
        <div className="sbody f">
          <div className="tok">{key || t("keyIssued")}</div>
          <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
            <button type="button" className="copybtn" onClick={rotate}>⟳ {t("newKey")}</button>
            {key && <button type="button" className="copybtn" onClick={copyKey}>{copied ? t("copied") : t("copy")}</button>}
          </div>
          <div className="hint">{t("keySwapHint")}</div>
        </div>
      </section>
    </Modal>
  );
}

