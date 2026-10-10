// Аудит (лаба 16): поиск по действию/актёру/деталям, пилюли типов, цветные
// чипы действий (login — янтарь, login.fail — красный с подсветкой строки),
// человекочитаемые детали. Keyset-пагинация — как сейчас на бэкенде.
//
// Контракт (проверен по internal/panel/api.go s.auditLog и admin.go):
//   GET /api/audit?before=&before_id= → { rows, next }  (без параметров фильтра)
//   row: { id, at, actor, action, detail } — detail::text, JSON.
//   Действия фазы 1: login {ip}, login.fail {ip}, logout {}, node.check {id, ok}.
//   Поиск и пилюли типов фильтруются на клиенте (сервер фильтров не принимает);
//   колонки «Источник» (IP) в контракте нет — IP приходит в детали login*.
import { useEffect, useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { EmptyRow, Err, SkeletonRows } from "../components/Bits";
import { auditDetail, auditGroup } from "../lib/logsfilters";

const GROUPS = [
  ["login", "groupLogin"],
  ["node", "groupNode"],
  ["session", "groupSession"],
  ["service", "groupService"],
  ["settings", "groupSettings"],
  ["acl", "groupAcl"],
  ["other", "groupOther"],
];

// Класс чипа действия (лаба 16): login/login.fail/node/session/domain, прочие —
// нейтральная рамка.
function actClass(action) {
  if (action === "login.fail") return "act login fail";
  const prefix = action.split(".")[0];
  if (["login", "node", "session"].includes(prefix)) return `act ${prefix}`;
  if (prefix === "service" || prefix === "domain") return "act domain";
  return "act";
}

// Детали human-readable: пары {k, v} из auditDetail переводятся ключами
// detIp/detId/detNodeOk/detNodeFail; незнакомые ключи и raw — как есть.
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
  const { t, err } = useI18n();
  const [search, setSearch] = useState("");
  const [group, setGroup] = useState("");
  const [rows, setRows] = useState(null);
  const [next, setNext] = useState(null);
  const [error, setError] = useState("");

  // Первая страница: только при монтировании и после «Загрузить ещё» —
  // фильтров на сервере нет, перезапрашивать нечего. live-флаг гасит ответ
  // размонтированного компонента; таймеров здесь нет.
  useEffect(() => {
    let live = true;
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
    return () => { live = false; };
  }, []);

  async function loadMore() {
    const p = new URLSearchParams();
    if (next) {
      p.set("before", next.at);
      p.set("before_id", next.id);
    }
    try {
      const out = await api(`/api/audit?${p}`);
      setRows((r) => [...(r || []), ...out.rows]);
      setNext(out.next || null);
      setError("");
    } catch (e) {
      setError(e);
    }
  }

  const needle = search.trim().toLowerCase();
  // Пилюли типов — только реально присутствующие в загруженных строках группы.
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
              {rows !== null && !visible.length && <EmptyRow colSpan={4}>{t("noData")}</EmptyRow>}
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
          <button type="button" className="btn ghost sm" onClick={loadMore}>{t("loadMore")}</button>
        </div>
      )}
    </>
  );
}
