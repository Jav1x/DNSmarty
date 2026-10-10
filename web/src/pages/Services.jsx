import { useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { useLoad } from "../hooks/useLoad";
import { Err, SkeletonRows, StatusLamp } from "../components/Bits";
import { useConfirm } from "../components/Toast";
import { parseDomainsInput, templateSummary } from "../lib/services";
import { fmtBytes, shares } from "../lib/util";
import { BUILTIN_TEMPLATES } from "../data/templates";

const FQDN_REASON = { badLabel: "fqdnBadLabel", emptyLabel: "fqdnEmptyLabel", tooLong: "fqdnTooLong" };

function strategyLabel(s, t) {
  if (s === "weighted") return t("weighted");
  if (s === "sticky24") return t("sticky");
  return t("rr");
}

function proxyLinks(form, nodes) {
  const links = [];
  for (const n of nodes) {
    if (form.get(`on_${n.id}`) === "on") {
      links.push({ proxy_id: n.id, weight: Number(form.get(`w_${n.id}`) || 1) });
    }
  }
  return links;
}

function serviceBody(form, nodes) {
  return {
    name: form.get("name"),
    strategy: form.get("strategy"),
    comment: form.get("comment") || "",
    enabled: form.get("enabled") === "on",
    proxies: proxyLinks(form, nodes),
  };
}

function ProxyFields({ nodes, weights }) {
  const { t } = useI18n();
  return (
    <div className="row-actions">
      {nodes.map((n) => {
        const w = weights?.[n.id];
        return (
          <label className="weight check" key={n.id}>
            <input type="checkbox" name={`on_${n.id}`} defaultChecked={!!w?.on} />
            {n.name}
            <input name={`w_${n.id}`} type="number" defaultValue={w?.weight || 1} min="1" max="1000" style={{ width: "4.5rem" }} aria-label={`${n.name} ${t("weight")}`} />
          </label>
        );
      })}
    </div>
  );
}

function ServiceFields({ service, nodes }) {
  const { t } = useI18n();
  const weights = Object.fromEntries((service?.proxies || []).map((p) => [p.proxy_id, p]));
  return (
    <>
      <div className="grid">
        <div><label>{t("name")}</label><input name="name" defaultValue={service?.name} required /></div>
        <div><label>{t("strategy")}</label><select name="strategy" defaultValue={service?.strategy || "round_robin"}><option value="round_robin">{t("rr")}</option><option value="weighted">{t("weighted")}</option><option value="sticky24">{t("sticky")}</option></select></div>
        <div><label>{t("comment")}</label><input name="comment" defaultValue={service?.comment} /></div>
      </div>
      <p><label className="check"><input type="checkbox" name="enabled" defaultChecked={service ? service.enabled : true} />{t("enabled")}</label></p>
      <ProxyFields nodes={nodes} weights={weights} />
    </>
  );
}

function ServiceCard({ svc, nodes, open, onToggle, reload, ask, setError }) {
  const { t } = useI18n();
  const onProxies = svc.proxies.filter((p) => p.on);
  const pct = shares(onProxies.map((p) => p.weight));
  async function save(event) {
    event.preventDefault();
    try {
      await api(`/api/services/${svc.id}`, { method: "POST", body: JSON.stringify(serviceBody(new FormData(event.target), nodes)) });
      reload();
    } catch (err) { setError(err); }
  }
  function remove() {
    ask(t("confirmDelete", { name: svc.name }), async () => {
      try {
        await api(`/api/services/${svc.id}`, { method: "DELETE" });
        reload();
      } catch (err) { setError(err); }
    });
  }
  return (
    <section className="mod service-card">
      <header className="row-actions">
        <button type="button" className="ghost" aria-expanded={open} onClick={onToggle}>
          <StatusLamp on={svc.enabled} />
          <strong>{svc.name}</strong>
        </button>
        <span>{strategyLabel(svc.strategy, t)}</span>
        <span>{t("domainCount", { n: svc.members.length })}</span>
        <span>{t("serviceQueries", { n: svc.queries_24h })}</span>
        <span>{fmtBytes(svc.bytes_24h)}</span>
      </header>
      {open && (
        <div className="service-body">
          <form onSubmit={save}>
            <ServiceFields service={svc} nodes={nodes} />
            <div className="row-actions">
              <button type="submit">{t("save")}</button>
              <button type="button" className="alarm" onClick={remove}>{t("delete")}</button>
            </div>
          </form>
          {onProxies.length > 0 && (
            <p className="mono">{onProxies.map((p, i) => `${p.proxy_name} ${pct[i]}%`).join(" · ")}</p>
          )}
          <MembersPanel svc={svc} reload={reload} ask={ask} setError={setError} />
        </div>
      )}
    </section>
  );
}

function MembersPanel({ svc, reload, ask, setError }) {
  const { t } = useI18n();
  const [text, setText] = useState("");
  const [match, setMatch] = useState("suffix");
  const [bad, setBad] = useState([]);
  async function add(event) {
    event.preventDefault();
    const parsed = parseDomainsInput(text);
    setBad(parsed.bad);
    if (!parsed.ok.length) return;
    try {
      await api(`/api/services/${svc.id}/members`, {
        method: "POST",
        body: JSON.stringify({ members: parsed.ok.map((name) => ({ name, match, enabled: true, comment: "" })) }),
      });
      setText("");
      setBad([]);
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
    <div className="members">
      <form onSubmit={add}>
        <textarea value={text} onChange={(e) => setText(e.target.value)} placeholder={t("pasteDomains")} aria-label={t("pasteDomains")} rows={3} />
        <div className="row-actions">
          <select value={match} onChange={(e) => setMatch(e.target.value)} aria-label={t("match")}>
            <option value="suffix">{t("suffix")}</option>
            <option value="fqdn">{t("fqdn")}</option>
          </select>
          <button type="submit">{t("addDomains")}</button>
        </div>
      </form>
      {bad.map((b) => (
        <p className="alarm" key={b.raw}>{b.raw}: {t(FQDN_REASON[b.reason] || "fqdnBadLabel", { s: b.raw })}</p>
      ))}
      <table>
        <thead>
          <tr><th>{t("name")}</th><th>{t("match")}</th><th>{t("queries")}</th><th></th></tr>
        </thead>
        <tbody>
          {svc.members.map((m) => (
            <tr key={m.id}>
              <td className="mono">{m.name}</td>
              <td>{m.match === "fqdn" ? t("fqdn") : t("suffix")}</td>
              <td>{m.queries_24h}</td>
              <td className="row-actions">
                <button type="button" className="ghost tiny" onClick={() => toggle(m)}>{m.enabled ? t("disable") : t("enable")}</button>
                <button type="button" className="ghost tiny" onClick={() => remove(m)}>{t("delete")}</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {!svc.members.length && <p className="empty">{t("noMembers")}</p>}
    </div>
  );
}

function NewService({ nodes, templates, reload, setError, onDone }) {
  const { t } = useI18n();
  const [tplId, setTplId] = useState("");
  const [draft, setDraft] = useState(null);
  const [matches, setMatches] = useState({});
  const [text, setText] = useState("");
  const tpl = templates.find((x) => x.id === tplId);

  function pick(id) {
    setTplId(id);
    const found = templates.find((x) => x.id === id);
    if (!found) {
      setDraft(null);
      setMatches({});
      setText("");
      return;
    }
    const p = found.payload;
    setDraft({
      name: found.name,
      strategy: p.strategy,
      weights: Object.fromEntries((p.proxies || []).map((w) => [w.proxy_id, { on: true, weight: w.weight }])),
    });
    setMatches(Object.fromEntries((p.domains || []).map((d) => [d.name, d.match])));
    setText((p.domains || []).map((d) => d.name).join("\n"));
  }

  async function create(event) {
    event.preventDefault();
    const form = new FormData(event.target);
    const parsed = parseDomainsInput(text);
    if (!parsed.ok.length) {
      setError(new Error(t("noMembers")));
      return;
    }
    const body = {
      ...serviceBody(form, nodes),
      members: parsed.ok.map((name) => ({ name, match: matches[name] || "suffix", enabled: true, comment: "" })),
    };
    try {
      await api("/api/services", { method: "POST", body: JSON.stringify(body) });
      onDone();
      reload();
    } catch (err) { setError(err); }
  }

  return (
    <form className="mod new-service" key={tplId} onSubmit={create}>
      <div className="grid">
        <div>
          <label>{t("fromTemplate")}</label>
          <select value={tplId} onChange={(e) => pick(e.target.value)}>
            <option value="">{t("blankService")}</option>
            {templates.map((x) => <option key={x.id} value={x.id}>{templateSummary({ name: x.name, domains: x.payload.domains || [] }, t)}</option>)}
          </select>
        </div>
      </div>
      <ServiceFields service={draft ? { name: draft.name, strategy: draft.strategy, comment: "", enabled: true, proxies: Object.entries(draft.weights).map(([id, w]) => ({ proxy_id: id, ...w })) } : null} nodes={nodes} />
      <textarea name="domains" value={text} onChange={(e) => setText(e.target.value)} placeholder={t("pasteDomains")} aria-label={t("pasteDomains")} rows={4} />
      {tpl && <p className="mono">{templateSummary({ name: tpl.name, domains: tpl.payload.domains || [] }, t)}</p>}
      <p><button type="submit">{t("add")}</button></p>
    </form>
  );
}

export function Services() {
  const { t } = useI18n();
  const { data, error, setError, reload } = useLoad("/api/services");
  const [open, setOpen] = useState(null);
  const [creating, setCreating] = useState(false);
  const [ask, confirmRow] = useConfirm();
  if (!data) return <div className="mod"><Err text={error} />{!error && <SkeletonRows cols={4} />}</div>;
  const services = data.services || [];
  const nodes = data.proxies || [];
  const templates = data.templates || [];
  const allTemplates = [
    ...BUILTIN_TEMPLATES,
    ...templates.map((x) => ({ id: x.id, name: x.name, payload: JSON.parse(x.payload) })),
  ];
  return (
    <>
      {confirmRow}
      <Err text={error} />
      <div className="toolbar">
        <button type="button" onClick={() => setCreating((c) => !c)}>{t("newService")}</button>
      </div>
      {creating && (
        <NewService nodes={nodes} templates={allTemplates} reload={reload} setError={setError} onDone={() => setCreating(false)} />
      )}
      {services.map((svc) => (
        <ServiceCard
          key={svc.id}
          svc={svc}
          nodes={nodes}
          open={open === svc.id}
          onToggle={() => setOpen(open === svc.id ? null : svc.id)}
          reload={reload}
          ask={ask}
          setError={setError}
        />
      ))}
      {!services.length && <p className="empty">{t("noServices")}</p>}
    </>
  );
}
