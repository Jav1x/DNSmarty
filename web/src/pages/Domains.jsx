import { useEffect, useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { Err, StatusLamp, SkeletonRows, Pager } from "../components/Bits";
import { useConfirm } from "../components/Toast";

const pageSize = 40;

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

function balanceLabel(s, t) {
  if (s === "weighted") return t("weighted");
  if (s === "sticky24") return t("sticky");
  return t("rr");
}

export function Domains() {
  const { t } = useI18n();
  const [data, setData] = useState(null);
  const [error, setError] = useState("");
  const [group, setGroup] = useState("all");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(0);
  const [editing, setEditing] = useState(null);
  const [ask, confirmRow] = useConfirm();
  async function reload() {
    try {
      setData(await api("/api/domains"));
      setError("");
    } catch (e) { setError(e); }
  }
  useEffect(() => { reload(); }, []);
  if (!data) return <div className="mod"><Err text={error} />{!error && <SkeletonRows cols={5} />}</div>;
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
    } catch (err) { setError(err); }
  }
  async function assignGroup(domain, groupID) {
    try {
      await api(`/api/domains/${domain.id}`, { method: "POST", body: JSON.stringify(domainPayload(domain, groupID)) });
      reload();
    } catch (err) { setError(err); }
  }
  async function create(event) {
    event.preventDefault();
    try {
      await api("/api/domains", { method: "POST", body: JSON.stringify(domainBody(new FormData(event.target), data.proxies)) });
      event.target.reset();
      setEditing(null);
      reload();
    } catch (err) { setError(err); }
  }
  async function removeGroup(g) {
    ask(t("confirmDelete", { name: g.name }), async () => {
      try {
        await api(`/api/groups/${g.id}`, { method: "DELETE" });
        if (group === g.id) setGroup("all");
        reload();
      } catch (err) { setError(err); }
    });
  }
  return (
    <>
      {confirmRow}
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
            <input name="name" placeholder={t("groupName")} required aria-label={t("groupName")} />
            <button type="submit" className="tiny">{t("add")}</button>
          </form>
        </aside>
        <section className="sheet">
          <div className="toolbar">
            <input value={q} onChange={(e) => { setQ(e.target.value); setPage(0); }} placeholder={t("searchDomains")} aria-label={t("searchDomains")} />
            <button type="button" onClick={() => setEditing(editing === "new" ? null : "new")}>{t("newDomain")}</button>
          </div>
          {editing === "new" && (
            <form onSubmit={create}>
              <DomainFields proxies={data.proxies} groups={groups} groupID={group !== "all" && group !== "none" ? group : ""} />
              <p><button type="submit">{t("add")}</button></p>
            </form>
          )}
          {editing && editing !== "new" && (
            <DomainCard key={editing.id} domain={editing} groups={groups} ask={ask} onChange={() => { setEditing(null); reload(); }} setError={setError} />
          )}
          <div className="table-wrap">
            <table>
              <thead>
                <tr><th>{t("name")}</th><th>{t("groups")}</th><th>{t("match")}</th><th>{t("strategy")}</th><th></th></tr>
              </thead>
              <tbody>
                {slice.map((d) => (
                  <tr key={d.id}>
                    <td className="mono"><StatusLamp on={d.enabled} />{d.name}</td>
                    <td>
                      <select value={d.group_id || ""} onChange={(e) => assignGroup(d, e.target.value)} aria-label={t("groups")}>
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
          </div>
          {!filtered.length && <p className="empty">{t("noDomains")}</p>}
          <Pager page={current} pages={pages} onPage={setPage} label={t("domainCount", { n: filtered.length })} />
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
            <input name={`w_${w.proxy_id}`} type="number" defaultValue={w.weight} min="1" max="1000" style={{ width: "4.5rem" }} aria-label={`${w.proxy_name} weight`} />
          </label>
        ))}
      </div>
    </>
  );
}

function DomainCard({ domain, groups, ask, onChange, setError }) {
  const { t } = useI18n();
  async function save(event) {
    event.preventDefault();
    try {
      await api(`/api/domains/${domain.id}`, { method: "POST", body: JSON.stringify(domainBody(new FormData(event.target), domain.weights)) });
      onChange();
    } catch (err) { setError(err); }
  }
  async function remove() {
    ask(t("confirmDelete", { name: domain.name }), async () => {
      try {
        await api(`/api/domains/${domain.id}`, { method: "DELETE" });
        onChange();
      } catch (err) { setError(err); }
    });
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
