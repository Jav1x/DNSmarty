import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { EmptyRow, Err, SkeletonRows } from "../components/Bits";
import { auditDetail, auditGroup, mergeById } from "../lib/logsfilters";

const GROUPS = [
  ["login", "groupLogin"],
  ["node", "groupNode"],
  ["session", "groupSession"],
  ["service", "groupService"],
  ["settings", "groupSettings"],
  ["acl", "groupAcl"],
  ["oauth", "groupOAuth"],
  ["other", "groupOther"],
];

function actClass(action) {
  if (action === "login.fail") return "act login fail";
  const prefix = action.split(".")[0];
  if (["login", "node", "session", "oauth"].includes(prefix)) return `act ${prefix}`;
  if (prefix === "service" || prefix === "domain" || prefix === "template") return "act domain";
  if (prefix === "totp" || prefix === "password") return "act login";
  return "act";
}

function Detail({ pairs }) {
  const { t } = useI18n();
  if (!pairs.length) return <span className="muted">{t("dash")}</span>;
  return (
    <>
      {pairs.map((p, i) => {
        let txt = null;
        if (p.k === "ip") txt = t("detIp", { v: String(p.v) });
        else if (p.k === "id") txt = t("detId", { v: String(p.v) });
        else if (p.k === "ok") txt = p.v ? t("detNodeOk") : t("detNodeFail");
        return (
          <span key={p.k}>
            {i > 0 && " · "}
            {txt === null ? <code>{`${p.k}: ${String(p.v)}`}</code> : txt}
          </span>
        );
      })}
    </>
  );
}

export function Audit() {
  const { t } = useI18n();
  const [search, setSearch] = useState("");
  const [group, setGroup] = useState("");
  const [rows, setRows] = useState(null);
  const [next, setNext] = useState(null);
  const [error, setError] = useState("");
  const [loadingMore, setLoadingMore] = useState(false);
  const moreRef = useRef(false);
  const mountedRef = useRef(false);

  useEffect(() => {
    let live = true;
    mountedRef.current = true;
    api("/api/audit")
      .then((out) => {
        if (!live) return;
        setRows(out.rows);
        setNext(out.next || null);
        setError("");
      })
      .catch((e) => {
        if (live) setError(e);
      });
    return () => {
      live = false;
      mountedRef.current = false;
    };
  }, []);

  async function loadMore() {
    if (moreRef.current || !next) return;
    moreRef.current = true;
    setLoadingMore(true);
    const p = new URLSearchParams({ before: next.at, before_id: next.id });
    try {
      const out = await api(`/api/audit?${p}`);
      if (!mountedRef.current) return;
      setRows((r) => mergeById(r, out.rows));
      setNext(out.next || null);
      setError("");
    } catch (e) {
      if (mountedRef.current) setError(e);
    } finally {
      moreRef.current = false;
      if (mountedRef.current) setLoadingMore(false);
    }
  }

  const needle = search.trim().toLowerCase();
  const present = GROUPS.filter(([g]) => (rows || []).some((r) => auditGroup(r.action) === g));
  const visible = (rows || []).filter((r) =>
    (!group || auditGroup(r.action) === group)
    && (!needle || `${r.actor} ${r.action} ${r.detail}`.toLowerCase().includes(needle)));

  return (
    <>
      <div className="hrow">
        <h1>{t("audit")}</h1>
        <span className="toolbar">
          <input
            className="finput"
            value={search}
            placeholder={t("searchAudit")}
            autoComplete="off"
            aria-label={t("searchAudit")}
            onChange={(e) => setSearch(e.target.value)}
          />
          <button type="button" className={`pill${group === "" ? " on" : ""}`} aria-pressed={group === ""} onClick={() => setGroup("")}>
            {t("filterAll")}
          </button>
          {present.map(([g, lbl]) => (
            <button key={g} type="button" className={`pill${group === g ? " on" : ""}`} aria-pressed={group === g} onClick={() => setGroup(g)}>
              {t(lbl)}
            </button>
          ))}
        </span>
      </div>
      <Err text={error} />

      <div className="card">
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t("time")}</th><th>{t("who")}</th><th>{t("action")}</th><th>{t("detail")}</th>
              </tr>
            </thead>
            <tbody>
              {rows === null && !error && <SkeletonRows cols={4} />}
              {rows !== null && !visible.length && (
                <EmptyRow colSpan={4}>{next ? t("noMatchLoaded") : t("noMatch")}</EmptyRow>
              )}
              {visible.map((row) => (
                <tr key={row.id} className={row.action === "login.fail" ? "err" : undefined}>
                  <td>{new Date(row.at).toLocaleString()}</td>
                  <td>{row.actor || t("dash")}</td>
                  <td><span className={actClass(row.action)}>{row.action}</span></td>
                  <td className="detail"><Detail pairs={auditDetail(row.action, row.detail)} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {next && (
        <div className="pager">
          <button type="button" className="btn ghost sm" disabled={loadingMore} onClick={loadMore}>{t("loadMore")}</button>
        </div>
      )}
    </>
  );
}
