import { useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { useLoad } from "../hooks/useLoad";
import { Err, SkeletonRows } from "../components/Bits";
import { useConfirm, useToast } from "../components/Toast";
import { Modal } from "../ui/Modal";
import Lamp from "../ui/Lamp";
import Switch from "../ui/Switch";
import TrashButton from "../ui/TrashButton";
import { parseDomainsInput, templateSummary, isBuiltinTemplate, readTemplatePayload, templatePayloadFromService } from "../lib/services";
import { fmtBytes, shares } from "../lib/util";
import { BUILTIN_TEMPLATES } from "../data/templates";

const FQDN_REASON = { badLabel: "fqdnBadLabel", emptyLabel: "fqdnEmptyLabel", tooLong: "fqdnTooLong" };

function strategyLabel(s, t) {
  if (s === "weighted") return t("weighted");
  if (s === "sticky24") return t("sticky");
  return t("rr");
}

function weightsFrom(svc) {
  return Object.fromEntries((svc?.proxies || []).map((p) => [p.proxy_id, { on: p.on, weight: p.weight }]));
}

function proxiesFrom(nodes, weights) {
  return nodes.filter((n) => weights[n.id]?.on).map((n) => ({
    proxy_id: n.id,
    weight: Number(weights[n.id].weight) || 1,
  }));
}

function RouteFields({ idPrefix = "svc", nodes, weights, setWeight, strategy, setStrategy }) {
  const { t } = useI18n();
  const onNodes = nodes.filter((n) => weights[n.id]?.on);
  const pct = shares(onNodes.map((n) => Number(weights[n.id]?.weight) || 0));
  const pctById = Object.fromEntries(onNodes.map((n, i) => [n.id, pct[i]]));
  const strategyId = `${idPrefix}-strategy`;
  return (
    <section className="sect">
      <h3>{t("routeSect")}</h3>
      <div className="sbody f">
        <div>
          <label htmlFor={strategyId}>{t("strategy")}</label>
          <select id={strategyId} value={strategy} onChange={(e) => setStrategy(e.target.value)}>
            <option value="round_robin">{t("rr")}</option>
            <option value="weighted">{t("weighted")}</option>
            <option value="sticky24">{t("sticky")}</option>
          </select>
        </div>
        <div className="route">
          {nodes.map((n) => (
            <label className="line" key={n.id}>
              <input type="checkbox" checked={!!weights[n.id]?.on} onChange={(e) => setWeight(n.id, { on: e.target.checked })} />
              <span>{n.name}</span>
              <input
                type="number"
                min="1"
                max="1000"
                aria-label={`${n.name} ${t("weight")}`}
                value={weights[n.id]?.weight || 1}
                onChange={(e) => setWeight(n.id, { weight: Number(e.target.value) })}
              />
              <b>{weights[n.id]?.on ? `${pctById[n.id] ?? 0}%` : ""}</b>
            </label>
          ))}
          {!nodes.length && <p className="hint">{t("noLink")}</p>}
        </div>
      </div>
    </section>
  );
}

function ServiceCard({ svc, open, onToggle, onEdit, reload, ask, setError }) {
  const { t } = useI18n();
  const onProxies = (svc.proxies || []).filter((p) => p.on);
  const pct = shares(onProxies.map((p) => p.weight));
  function remove(event) {
    event.stopPropagation();
    ask(t("confirmDelete", { name: svc.name }), async () => {
      try {
        await api(`/api/services/${svc.id}`, { method: "DELETE" });
        reload();
      } catch (err) { setError(err); }
    });
  }
  function edit(event) {
    event.stopPropagation();
    onEdit(svc);
  }
  return (
    <article className={`svc${open ? " open" : ""}${svc.enabled ? "" : " lampoff"}`}>
      <div
        className="svc-head"
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={onToggle}
        onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onToggle(); } }}
      >
        <div className="sn">
          <Lamp state={svc.enabled ? "on" : "dis"} tip={svc.enabled ? t("enabled") : t("nodeOff")} />
          <b>{svc.name}</b>
          <span className="cnt">{t("domainCount", { n: (svc.members || []).length })}</span>
        </div>
        <div className="sroute">
          <span className="pill on">{strategyLabel(svc.strategy, t)}</span>
          {onProxies.length
            ? onProxies.map((p, i) => (
              <span className="pill" key={p.proxy_id}>{p.proxy_name} {pct[i]}%</span>
            ))
            : <span className="pill">{t("routeEmpty")}</span>}
        </div>
        <div className="traf">
          <b>{t("serviceQueries", { n: svc.queries_24h })}</b>
          <span>{fmtBytes(svc.bytes_24h)}</span>
        </div>
        <div className="sacts" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
          <button type="button" className="abtn" title={t("edit")} onClick={edit}>✎</button>
          <button type="button" className="abtn del" title={t("delete")} onClick={remove}>✕</button>
          <span className="chev" aria-hidden="true">▶</span>
        </div>
      </div>
      <div className="svc-dom">
        <MembersPanel svc={svc} reload={reload} ask={ask} setError={setError} />
      </div>
    </article>
  );
}

function MembersPanel({ svc, reload, ask, setError }) {
  const { t } = useI18n();
  const [text, setText] = useState("");
  const [match, setMatch] = useState("suffix");
  const members = svc.members || [];
  const parsed = parseDomainsInput(text);
  async function add(event) {
    event.preventDefault();
    if (!parsed.ok.length) return;
    try {
      await api(`/api/services/${svc.id}/members`, {
        method: "POST",
        body: JSON.stringify({ members: parsed.ok.map((name) => ({ name, match, enabled: true, comment: "" })) }),
      });
      setText("");
      reload();
    } catch (err) { setError(err); }
  }
  async function toggle(m) {
    try {
      await api(`/api/members/${m.id}`, {
        method: "POST",
        body: JSON.stringify({ name: m.name, match: m.match, enabled: !m.enabled, comment: m.comment || "" }),
      });
      reload();
    } catch (err) { setError(err); }
  }
  function remove(m) {
    ask(t("confirmDelete", { name: m.name }), async () => {
      try {
        await api(`/api/members/${m.id}`, { method: "DELETE" });
        reload();
      } catch (err) { setError(err); }
    });
  }
  return (
    <>
      {!!members.length && (
        <table className="dtable">
          <thead>
            <tr>
              <th>{t("name")}</th>
              <th>{t("match")}</th>
              <th>{t("enabled")}</th>
              <th>{t("queries")}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {members.map((m) => (
              <tr key={m.id} className={m.enabled ? undefined : "dim"}>
                <td className="dname">
                  <span className={`dot${m.enabled ? "" : " off"}`} />
                  {m.name}
                </td>
                <td>{m.match === "fqdn" ? t("fqdn") : t("suffix")}</td>
                <td><Switch on={m.enabled} onChange={() => toggle(m)} label={t("enabled")} /></td>
                <td className="num">{m.queries_24h}</td>
                <td>
                  <div className="acts2">
                    <button type="button" className="abtn del" title={t("delete")} onClick={() => remove(m)}>✕</button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {!members.length && <p className="empty">{t("noMembers")}</p>}
      <form className="dadd" onSubmit={add}>
        <div className="f grow">
          <textarea value={text} onChange={(e) => setText(e.target.value)} placeholder={t("pasteDomains")} aria-label={t("pasteDomains")} rows={2} />
        </div>
        <div className="f">
          <select value={match} onChange={(e) => setMatch(e.target.value)} aria-label={t("match")}>
            <option value="suffix">{t("suffix")}</option>
            <option value="fqdn">{t("fqdn")}</option>
          </select>
        </div>
        <button type="submit" className="btn sm" disabled={!parsed.ok.length}>{t("addDomains")}</button>
      </form>
      {!!parsed.bad.length && (
        <div className="dchips pad">
          {parsed.bad.map((b) => (
            <span key={b.raw} className="dchip bad">{b.raw}<i>{t(FQDN_REASON[b.reason] || "fqdnBadLabel")}</i></span>
          ))}
        </div>
      )}
    </>
  );
}

function SaveTemplateModal({ svc, onClose, reload }) {
  const { t } = useI18n();
  const toast = useToast();
  const [name, setName] = useState(svc.name || "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const payload = templatePayloadFromService(svc);

  async function save(event) {
    event.preventDefault();
    if (!payload.domains.length) {
      setError(t("tplNeedDomains"));
      return;
    }
    setBusy(true);
    try {
      await api("/api/templates", { method: "POST", body: JSON.stringify({ name: name.trim(), payload }) });
      toast(t("tplSaved"));
      reload();
      onClose();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={t("saveTemplateTitle")}
      note={<span className="dirty">{t("domainCount", { n: payload.domains.length })}</span>}
      footer={(
        <>
          <button type="button" className="btn ghost" onClick={onClose}>{t("cancel")}</button>
          <button type="submit" form="tpl-save" className="btn" disabled={busy || !name.trim() || !payload.domains.length}>{t("save")}</button>
        </>
      )}
    >
      <form id="tpl-save" className="code-step" onSubmit={save}>
        <Err text={error} />
        <p className="hint">{t("saveTemplateHint")}</p>
        <div className="f">
          <label htmlFor="tpl-name">{t("name")}</label>
          <input id="tpl-name" value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
        </div>
      </form>
    </Modal>
  );
}

function EditServiceModal({ svc, nodes, onClose, reload, ask, onSaveTpl }) {
  const { t } = useI18n();
  const toast = useToast();
  const [name, setName] = useState(svc.name);
  const [comment, setComment] = useState(svc.comment || "");
  const [strategy, setStrategy] = useState(svc.strategy || "round_robin");
  const [enabled, setEnabled] = useState(!!svc.enabled);
  const [weights, setWeights] = useState(() => weightsFrom(svc));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  function setWeight(id, patch) {
    setWeights((prev) => ({ ...prev, [id]: { on: false, weight: 1, ...prev[id], ...patch } }));
  }

  const orig = weightsFrom(svc);
  const dirty = [
    name !== svc.name,
    comment !== (svc.comment || ""),
    enabled !== !!svc.enabled,
    strategy !== (svc.strategy || "round_robin"),
    JSON.stringify(proxiesFrom(nodes, weights)) !== JSON.stringify(proxiesFrom(nodes, orig)),
  ].filter(Boolean).length;

  async function save(event) {
    event.preventDefault();
    setBusy(true);
    try {
      await api(`/api/services/${svc.id}`, {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          strategy,
          comment,
          enabled,
          proxies: proxiesFrom(nodes, weights),
        }),
      });
      toast(t("saved"));
      reload();
      onClose();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  function remove() {
    ask(t("confirmDelete", { name: svc.name }), async () => {
      try {
        await api(`/api/services/${svc.id}`, { method: "DELETE" });
        reload();
        onClose();
      } catch (err) { setError(err); }
    });
  }

  return (
    <Modal
      open
      onClose={onClose}
      width={820}
      title={t("editService")}
      note={dirty ? <span className="dirty">{t("unsaved", { n: dirty })}</span> : null}
      footer={(
        <>
          <TrashButton tip={t("trashServiceTip")} onClick={remove} />
          <button type="button" className="btn ghost" onClick={() => onSaveTpl(svc)} disabled={!svc.members.length}>{t("saveTemplate")}</button>
          <button type="button" className="btn ghost" onClick={onClose}>{t("cancel")}</button>
          <button type="submit" form="svc-edit" className="btn" disabled={busy || !name.trim() || !dirty}>{t("save")}</button>
        </>
      )}
    >
      <form id="svc-edit" onSubmit={save}>
        <Err text={error} />
        <section className="sect">
          <h3>{t("svcSect")}<small>{enabled ? t("enabled") : t("nodeOff")}</small></h3>
          <div className="sbody f">
            <div className="frow">
              <div>
                <label htmlFor="edit-svc-name">{t("name")}</label>
                <input id="edit-svc-name" value={name} onChange={(e) => setName(e.target.value)} required />
              </div>
              <div>
                <label htmlFor="edit-svc-comment">{t("comment")}</label>
                <input id="edit-svc-comment" value={comment} onChange={(e) => setComment(e.target.value)} />
              </div>
            </div>
            <div className="swrow">
              <span className="t"><b>{t("enabled")}</b><span>{t("svcEnabledSub")}</span></span>
              <Switch on={enabled} onChange={setEnabled} label={t("enabled")} />
            </div>
          </div>
        </section>
        <RouteFields idPrefix="edit-svc" nodes={nodes} weights={weights} setWeight={setWeight} strategy={strategy} setStrategy={setStrategy} />
      </form>
    </Modal>
  );
}

function NewServiceModal({ nodes, templates, reload, onClose, ask }) {
  const { t } = useI18n();
  const [mode, setMode] = useState(templates.length ? "tpl" : "blank");
  const [tplId, setTplId] = useState("");
  const [name, setName] = useState("");
  const [comment, setComment] = useState("");
  const [strategy, setStrategy] = useState("round_robin");
  const [enabled, setEnabled] = useState(true);
  const [weights, setWeights] = useState({});
  const [matches, setMatches] = useState({});
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const tpl = templates.find((x) => x.id === tplId);
  const parsed = parseDomainsInput(text);
  const onNodes = nodes.filter((n) => weights[n.id]?.on);

  function blank() {
    setMode("blank");
    setTplId("");
    setName("");
    setComment("");
    setStrategy("round_robin");
    setEnabled(true);
    setWeights({});
    setMatches({});
    setText("");
    setError("");
  }

  function pick(found) {
    const p = found.payload;
    setMode("tpl");
    setTplId(found.id);
    setName(found.name);
    setStrategy(p.strategy || "round_robin");
    setWeights(Object.fromEntries((p.proxies || []).map((w) => [w.proxy_id, { on: true, weight: w.weight }])));
    setMatches(Object.fromEntries((p.domains || []).map((d) => [d.name, d.match || "suffix"])));
    setText((p.domains || []).map((d) => d.name).join("\n"));
    setError("");
  }

  function setWeight(id, patch) {
    setWeights((prev) => ({ ...prev, [id]: { on: false, weight: 1, ...prev[id], ...patch } }));
  }

  async function create(event) {
    event.preventDefault();
    if (!parsed.ok.length) {
      setError(t("noMembers"));
      return;
    }
    try {
      await api("/api/services", { method: "POST", body: JSON.stringify({
        name,
        strategy,
        comment,
        enabled,
        proxies: onNodes.map((n) => ({ proxy_id: n.id, weight: Number(weights[n.id].weight) || 1 })),
        members: parsed.ok.map((domain) => ({ name: domain, match: matches[domain] || "suffix", enabled: true, comment: "" })),
      }) });
      onClose();
      reload();
    } catch (err) { setError(err); }
  }

  return (
    <Modal
      open
      onClose={onClose}
      width={820}
      title={t("newService")}
      note={<span className="dirty">{tpl ? templateSummary({ name: tpl.name, domains: tpl.payload.domains || [] }, t) : t("blankService")}</span>}
      footer={(
        <>
          <button type="button" className="btn ghost" onClick={onClose}>{t("cancel")}</button>
          <button type="submit" form="svc-new" className="btn" disabled={!name.trim() || !parsed.ok.length}>{t("add")}</button>
        </>
      )}
    >
      <form id="svc-new" onSubmit={create}>
        <div className="seg" role="tablist">
          <button type="button" role="tab" aria-selected={mode === "tpl"} className={mode === "tpl" ? "on" : ""} onClick={() => setMode("tpl")}>{t("fromTemplate")}</button>
          <button type="button" role="tab" aria-selected={mode === "blank"} className={mode === "blank" ? "on" : ""} onClick={blank}>{t("blankService")}</button>
        </div>
        {mode === "tpl" && (
          <div className="tplgrid">
            {templates.map((x) => (
              <div key={x.id} className={`tplcard hasx${x.id === tplId ? " on" : ""}`}>
                <button type="button" className="tplpick" onClick={() => pick(x)}>
                  <b>{x.name}</b>
                  <span>
                    {t("domainCount", { n: (x.payload.domains || []).length })}
                    {!isBuiltinTemplate(x.id) ? ` · ${t("tplCustom")}` : ""}
                  </span>
                </button>
                {!isBuiltinTemplate(x.id) && (
                  <button
                    type="button"
                    className="tplx"
                    aria-label={t("delete")}
                    onClick={() => ask(t("confirmDeleteTpl", { name: x.name }), async () => {
                      try {
                        await api(`/api/templates/${x.id}`, { method: "DELETE" });
                        if (tplId === x.id) blank();
                        reload();
                      } catch (err) { setError(err); }
                    })}
                  >✕</button>
                )}
              </div>
            ))}
          </div>
        )}
        <Err text={error} />
        <section className="sect">
          <h3>{t("svcSect")}<small>{enabled ? t("enabled") : t("disable")}</small></h3>
          <div className="sbody f">
            <div className="frow">
              <div>
                <label htmlFor="svc-name">{t("name")}</label>
                <input id="svc-name" value={name} onChange={(e) => setName(e.target.value)} required />
              </div>
              <div>
                <label htmlFor="svc-comment">{t("comment")}</label>
                <input id="svc-comment" value={comment} onChange={(e) => setComment(e.target.value)} />
              </div>
            </div>
            <div className="swrow">
              <span className="t"><b>{t("enabled")}</b><span>{t("svcEnabledSub")}</span></span>
              <Switch on={enabled} onChange={setEnabled} label={t("enabled")} />
            </div>
          </div>
        </section>
        <section className="sect">
          <h3>{t("domains")}<small>{parsed.ok.length ? t("domainCount", { n: parsed.ok.length }) : ""}</small></h3>
          <div className="sbody f">
            {!!parsed.ok.length && (
              <div className="dchips">
                {parsed.ok.map((domain) => (
                  <button
                    type="button"
                    key={domain}
                    className="dchip"
                    onClick={() => setMatches((m) => ({ ...m, [domain]: (m[domain] || "suffix") === "suffix" ? "fqdn" : "suffix" }))}
                  >
                    {domain}
                    <i>{(matches[domain] || "suffix") === "fqdn" ? t("fqdn") : t("suffix")}</i>
                  </button>
                ))}
              </div>
            )}
            {!!parsed.bad.length && (
              <div className="dchips">
                {parsed.bad.map((b) => (
                  <span key={b.raw} className="dchip bad">{b.raw}<i>{t(FQDN_REASON[b.reason] || "fqdnBadLabel")}</i></span>
                ))}
              </div>
            )}
            <textarea value={text} onChange={(e) => setText(e.target.value)} placeholder={t("pasteDomains")} aria-label={t("pasteDomains")} rows={4} />
          </div>
        </section>
        <RouteFields idPrefix="new-svc" nodes={nodes} weights={weights} setWeight={setWeight} strategy={strategy} setStrategy={setStrategy} />
      </form>
    </Modal>
  );
}

export function Services() {
  const { t } = useI18n();
  const { data, error, setError, reload } = useLoad("/api/services");
  const [open, setOpen] = useState(null);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState(null);
  const [savingTpl, setSavingTpl] = useState(null);
  const [ask, confirmRow] = useConfirm();
  if (!data) {
    return (
      <div className="mod">
        <Err text={error} />
        {!error && <table><tbody><SkeletonRows cols={4} /></tbody></table>}
      </div>
    );
  }
  const services = data.services || [];
  const nodes = data.proxies || [];
  const templates = data.templates || [];
  const allTemplates = [
    ...BUILTIN_TEMPLATES,
    ...templates.map((x) => ({ id: x.id, name: x.name, payload: readTemplatePayload(x.payload) })),
  ];
  return (
    <>
      {confirmRow}
      <Err text={error} />
      <div className="toolbar">
        <button type="button" className="btn" onClick={() => setCreating(true)}>{t("newService")}</button>
      </div>
      {creating && (
        <NewServiceModal nodes={nodes} templates={allTemplates} reload={reload} onClose={() => setCreating(false)} ask={ask} />
      )}
      {editing && (
        <EditServiceModal
          svc={editing}
          nodes={nodes}
          reload={reload}
          ask={ask}
          onClose={() => setEditing(null)}
          onSaveTpl={setSavingTpl}
        />
      )}
      {savingTpl && (
        <SaveTemplateModal svc={savingTpl} reload={reload} onClose={() => setSavingTpl(null)} />
      )}
      <div className="svc-list">
        {services.map((svc) => (
          <ServiceCard
            key={svc.id}
            svc={svc}
            open={open === svc.id}
            onToggle={() => setOpen(open === svc.id ? null : svc.id)}
            onEdit={setEditing}
            reload={reload}
            ask={ask}
            setError={setError}
          />
        ))}
      </div>
      {!services.length && <p className="empty">{t("noServices")}</p>}
    </>
  );
}
