import { useEffect, useState } from "react";
import { api } from "../api";
import { useI18n } from "../i18n";
import { Err, EmptyRow, SkeletonRows } from "../components/Bits";
import { useConfirm, useToast } from "../components/Toast";
import Lamp from "../ui/Lamp";
import Modal from "../ui/Modal";
import Pill from "../ui/Pill";
import Switch from "../ui/Switch";
import TrashButton from "../ui/TrashButton";
import { parseCidr } from "../lib/util";

/* «Доступ» (.design-lab/lab12.html): сводка allow/deny, единая таблица правил
   с колонкой «заблокировано 24ч» (GET /api/stats/acl; сбой эндпоинта — «—»),
   модалка правила: live-валидация CIDR (parseCidr), сегмент allow/deny,
   превью приоритета, свитч, корзина 🗑 в футере. */

// Число по-русски: 1 правило / 2–4 правила / 5+ правил; en — обычное множественное.
function ruleWord(n, lang) {
  if (lang === "ru") {
    if (n % 10 === 1 && n % 100 !== 11) return "правило";
    if ([2, 3, 4].includes(n % 10) && !(n % 100 >= 12 && n % 100 <= 14)) return "правила";
    return "правил";
  }
  return n === 1 ? "rule" : "rules";
}

function ipToInt(ip) {
  const o = ip.split(".").map(Number);
  return BigInt(((o[0] * 256 + o[1]) * 256 + o[2]) * 256 + o[3]);
}

// Адрес сразу за подсетью — «мимо правила» в превью лабы 12 (/32 → ip+1).
function nextAddr(ip, addrs) {
  const n = ipToInt(ip) + BigInt(addrs);
  return [n >> 24n & 255n, n >> 16n & 255n, n >> 8n & 255n, n & 255n].join(".");
}

export function Access() {
  const { t, lang } = useI18n();
  const [data, setData] = useState(null);
  const [error, setError] = useState("");
  const [allowOn, setAllowOn] = useState(true);
  const [denyOn, setDenyOn] = useState(true);
  // hits: {id: n} из /api/stats/acl; null — не ответили или сбой («—» в колонке).
  const [hits, setHits] = useState(null);
  const [query, setQuery] = useState("");
  const [kindFilter, setKindFilter] = useState("all");
  // Черновик модалки + снимок для счётчика изменённых полей.
  const [draft, setDraft] = useState(null);
  const [base, setBase] = useState(null);
  const [ask, confirmRow] = useConfirm();
  const toast = useToast();

  async function load() {
    try {
      const out = await api("/api/clients");
      setData(out.rows);
      setAllowOn(out.allow_enabled);
      setDenyOn(out.deny_enabled);
      setError("");
    } catch (e) { setError(e); }
  }
  // «Заблокировано 24ч» по правилам. Сбой — колонка «—», страница работает дальше.
  function loadHits() {
    api("/api/stats/acl")
      .then((out) => {
        const m = {};
        for (const r of out.rules) m[r.id] = r.hits;
        setHits(m);
      })
      .catch(() => setHits(null));
  }
  useEffect(() => { load(); loadHits(); }, []);

  // Toggle sends both flags: the endpoint sets the pair in one transaction.
  async function toggleKinds(allowEnabled, denyEnabled) {
    const prev = { allowOn, denyOn };
    setAllowOn(allowEnabled);
    setDenyOn(denyEnabled);
    try {
      await api("/api/clients/kinds", { method: "POST", body: JSON.stringify({ allow_enabled: allowEnabled, deny_enabled: denyEnabled }) });
      toast(t("saved"));
    } catch (e) {
      setAllowOn(prev.allowOn);
      setDenyOn(prev.denyOn);
      setError(e);
    }
  }

  const rows = data || [];
  const q = query.trim().toLowerCase();
  const filtered = rows
    .filter((c) => kindFilter === "all" || (c.list_kind || "allow") === kindFilter)
    .filter((c) => !q || c.cidr.toLowerCase().includes(q) || (c.label || "").toLowerCase().includes(q));
  const kindCount = (kind) => rows.filter((c) => (c.list_kind || "allow") === kind).length;

  function openEditor(c) {
    const d = { id: c?.id ?? null, cidr: c?.cidr ?? "", label: c?.label ?? "", kind: c?.list_kind || "allow", enabled: c?.enabled ?? true };
    setDraft(d);
    setBase(d);
  }
  const dirty = draft && base
    ? [draft.cidr !== base.cidr, draft.label !== base.label, draft.kind !== base.kind, draft.enabled !== base.enabled].filter(Boolean).length
    : 0;
  const verdict = draft ? parseCidr(draft.cidr.trim()) : null;
  const canSave = Boolean(draft && draft.cidr.trim() && verdict.ok);
  const sampleIP = verdict?.ok && draft.cidr.trim() ? draft.cidr.trim().split("/")[0] : "";

  function closeEditor() { setDraft(null); setBase(null); }
  async function save(event) {
    event?.preventDefault?.();
    const body = JSON.stringify({ cidr: draft.cidr.trim(), label: draft.label, list_kind: draft.kind, enabled: draft.enabled });
    try {
      await api(draft.id ? `/api/clients/${draft.id}` : "/api/clients", { method: "POST", body });
      closeEditor();
      toast(t("saved"));
      load();
      loadHits();
    } catch (e) { setError(e); }
  }
  function removeDraft() {
    ask(t("confirmDelete", { name: draft.cidr }), async () => {
      try {
        await api(`/api/clients/${draft.id}`, { method: "DELETE" });
        closeEditor();
        load();
        loadHits();
      } catch (e) { setError(e); }
    });
  }

  return (
    <>
      {confirmRow}
      <div className="hrow">
        <h1>{t("access")}</h1>
        <span className="sub">{t("accessSub")}</span>
        <span className="toolbar">
          <input className="finput" style={{ width: 190 }} placeholder={t("searchRules")} value={query} onChange={(e) => setQuery(e.target.value)} />
          <Pill on={kindFilter === "all"} onClick={() => setKindFilter("all")}>{t("filterAll")}</Pill>
          <Pill on={kindFilter === "allow"} onClick={() => setKindFilter("allow")}>{`allow · ${kindCount("allow")}`}</Pill>
          <Pill on={kindFilter === "deny"} onClick={() => setKindFilter("deny")}>{`deny · ${kindCount("deny")}`}</Pill>
          <button type="button" className="btn" onClick={() => openEditor(null)}>+ {t("ruleCol")}</button>
        </span>
      </div>
      <Err text={error} />

      {/* сводка: два списка и их состояние (лаба 12) */}
      <div className="kinds">
        {[
          ["allow", t("allowServe"), t("allowServeSub"), allowOn, (v) => toggleKinds(v, denyOn), kindCount("allow")],
          ["deny", t("denyRefuse"), t("denyRefuseSub"), denyOn, (v) => toggleKinds(allowOn, v), kindCount("deny")],
        ].map(([kind, title, sub, on, toggle, n]) => (
          <div className={`kind ${kind}`} key={kind}>
            <span className="kt"><b>{title}</b><span>{sub}</span></span>
            <span className="kstat">
              {n} {ruleWord(n, lang)} · <b>{on ? t("enabled") : t("listDisabled")}</b>
              {" "}
              <Switch on={on} onChange={toggle} label={`${title}: ${on ? t("enabled") : t("listDisabled")}`} />
            </span>
          </div>
        ))}
      </div>

      <div className="tablecard">
        <table>
          <thead><tr>
            <th>{t("ruleCol")} <span className="ar">▾</span></th>
            <th>{t("actSect")}</th>
            <th>{t("label")}</th>
            <th style={{ textAlign: "right" }}>{t("hits24")}</th>
            <th></th>
          </tr></thead>
          <tbody>
            {data === null && <SkeletonRows cols={5} rows={3} />}
            {data !== null && filtered.map((c) => {
              const kind = c.list_kind || "allow";
              const hit = hits && c.id in hits ? hits[c.id] : null;
              return (
                <tr key={c.id} className="click" onClick={(e) => { if (!e.target.closest("button")) openEditor(c); }}>
                  <td>
                    <span className="cidr">
                      <Lamp state={c.enabled ? "on" : "dis"} tip={c.enabled ? t("ruleOnTip") : t("ruleOffTip")} />
                      {c.cidr}
                    </span>
                  </td>
                  <td><span className={`ktag ${kind}`}>{kind}</span></td>
                  <td className={c.label ? "lbl" : "lbl none"}>{c.label || "—"}</td>
                  <td className="traf">{hit === null ? "—" : <b>{hit.toLocaleString(lang === "ru" ? "ru-RU" : "en-US")}</b>}</td>
                  <td>
                    <span className="acts">
                      <button type="button" className="abtn" onClick={() => openEditor(c)} title={t("edit")}>✎</button>
                    </span>
                  </td>
                </tr>
              );
            })}
            {data !== null && filtered.length === 0 && <EmptyRow colSpan={5}>{t("noRules")}</EmptyRow>}
          </tbody>
        </table>
      </div>
      <div className="hint">{t("prioHint")}</div>

      <Modal
        open={Boolean(draft)}
        onClose={closeEditor}
        title={draft?.id
          ? <>{t("ruleCol")} · <span style={{ color: "var(--accent)" }}>{base.cidr}</span></>
          : t("newRule")}
        note={dirty ? <span className="dirty">● {t("unsaved", { n: dirty })}</span> : null}
        footer={(
          <>
            {draft?.id && <TrashButton tip={t("trashRuleTip")} onClick={removeDraft} />}
            <button type="button" className="btn ghost" onClick={closeEditor}>{t("cancel")}</button>
            <button type="button" className="btn" onClick={save} disabled={!canSave}>{t("save")}</button>
          </>
        )}
      >
        {draft && (
          <form onSubmit={save}>
            {/* Сеть: кого касается правило — CIDR с живой валидацией (лаба 12) */}
            <section className="sect">
              <h3>{t("netSect")}<small>{t("netSectSub")}</small></h3>
              <div className="sbody f">
                <label htmlFor="rule-cidr">CIDR</label>
                <input id="rule-cidr" spellCheck={false} placeholder="198.51.100.10/32" value={draft.cidr}
                  onChange={(e) => setDraft({ ...draft, cidr: e.target.value })} />
                {draft.cidr.trim() !== "" && <div className={`hint ${verdict.ok ? "ok" : "bad"}`}>{verdict.msg}</div>}
              </div>
            </section>

            {/* Действие: сегмент allow/deny и превью приоритета (лаба 12) */}
            <section className="sect">
              <h3>{t("actSect")}<small>{t("actSectSub")}</small></h3>
              <div className="sbody">
                <span className="seg">
                  <button type="button" className={draft.kind === "allow" ? "on allow" : ""} onClick={() => setDraft({ ...draft, kind: "allow" })}>
                    <span className="dotb g"></span>{t("allowServe")}
                  </button>
                  <button type="button" className={draft.kind === "deny" ? "on deny" : ""} onClick={() => setDraft({ ...draft, kind: "deny" })}>
                    <span className="dotb r"></span>{t("denyRefuse")}
                  </button>
                </span>
                {verdict?.ok && draft.cidr.trim() !== "" && (
                  <div className="preview">
                    {t("client")} <b>{sampleIP}</b> <span style={{ opacity: 0.6 }}>→ {t("clientHits")} →</span>{" "}
                    <b className={draft.kind === "deny" ? "r" : ""}>{draft.kind === "deny" ? t("refused") : t("served")}</b>
                    {" "}({t("denyWins")})<br />
                    {t("client")} <b>{nextAddr(sampleIP, verdict.addrs)}</b> <span style={{ opacity: 0.6 }}>→ {t("clientMiss")}</span>
                  </div>
                )}
              </div>
            </section>

            {/* Прочее: метка и включённость */}
            <section className="sect">
              <h3>{t("otherSect")}</h3>
              <div className="sbody f">
                <label htmlFor="rule-label">{t("label")}</label>
                <input id="rule-label" placeholder={t("ruleWhy")} value={draft.label}
                  onChange={(e) => setDraft({ ...draft, label: e.target.value })} />
                <div>
                  <div className="swrow">
                    <span className="t"><b>{t("ruleOn")}</b><span>{t("ruleOnSub")}</span></span>
                    <Switch on={draft.enabled} onChange={(v) => setDraft({ ...draft, enabled: v })} label={t("ruleOn")} />
                  </div>
                </div>
              </div>
            </section>
          </form>
        )}
      </Modal>
    </>
  );
}
