// Ноды — порт lab10 (.design-lab/lab10.html): drag-таблица (порядок строк —
// локальный: сервер сортирует ноды по роли и имени, свой порядок ротации — фаза 2),
// bulkbar (В ротацию / Из ротации / Отключить / Удалить), модалка редактирования:
// Состояние (read-only, hw-поля «—» до фазы 2), Виталы (свитчи «В ротации» и
// «Нода отключена» — одно булево enabled бэкенда; роль ограничена dns/proxy),
// ключ связи (перевыпуск ⟳ — показывается один раз), 🗑 через ConfirmDialog.
// Сетевой контракт прежний: GET /api/nodes; POST /api/nodes; POST /api/nodes/{id}
// (обновление, принимает enabled:false; верб — POST, бриф говорил PUT, но маршрута
// PUT в серверной таблице нет — закреплено тестом internal/integration/node_disable_test.go);
// POST /api/nodes/{id}/connect (проверка связи, {ok, error} — сигнатура для
// визарда задачи 8); POST /api/nodes/{id}/key; DELETE /api/nodes/{id}.
// Визард «Новая нода» по лабе — задача 8; здесь рабочий краткий вариант.
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
import { ago } from "../lib/util";

// NodeInput из строки сервера (+ явная замена enabled для bulk-операций).
// Обновление принимает только полное тело — шлём все поля ноды.
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

// Проверка связи — сигнатура остаётся для визарда задачи 8 (ok/error из лабы).
export function nodeConnect(id) {
  return api(`/api/nodes/${id}/connect`, { method: "POST" });
}

export { nodeInput };

// FormData формы создания → NodeInput (из старого UI, контракт не менялся).
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
  const { data, loading, error, setError, reload } = useLoad("/api/nodes");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(() => new Set());
  // drag-перестановка строк (лаба 10): dragId — источник, overId — приёмник;
  // порядок строк локальный, reload возвращает серверную сортировку.
  const [dragId, setDragId] = useState(null);
  const [overId, setOverId] = useState(null);
  // order — id-шники после перетаскивания; null — серверный порядок.
  const [order, setOrder] = useState(null);
  // open: null | {kind:"edit", id} | {kind:"new"}
  const [open, setOpen] = useState(null);
  const [ask, confirmRow] = useConfirm();
  const toast = useToast();

  const nodes = data || [];
  useEffect(() => { setOrder(null); }, [data]);

  // Переставленный список: знакомые id в порядке drag'а, новые — в хвосте.
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
  const shownSelected = list.filter((n) => selected.has(n.id)).length;
  const allSelected = list.length > 0 && shownSelected === list.length;

  // Общий хвост мутаций: очистка выделения, перечитывание, toast; ошибки — в баннер.
  async function withMutation(run) {
    try {
      await run();
      setSelected(new Set());
      reload();
      toast(t("saved"));
    } catch (e) { setError(e); reload(); } // успехи до сбоя в bulk-цикле уже на сервере
  }
  function bulkEnabled(next) {
    const targets = nodes.filter((n) => selected.has(n.id) && n.enabled !== next);
    if (!targets.length) return;
    withMutation(async () => {
      for (const n of targets) {
        await api(`/api/nodes/${n.id}`, { method: "POST", body: JSON.stringify(nodeInput(n, next)) });
      }
    });
  }
  function bulkDelete() {
    const ids = [...selected];
    ask(t("bulkDeleteConfirm", { n: ids.length }), () => {
      withMutation(async () => {
        for (const id of ids) await api(`/api/nodes/${id}`, { method: "DELETE" });
      });
    });
  }
  function removeRow(n) {
    ask(t("confirmDelete", { name: n.name }), () => {
      withMutation(async () => {
        await api(`/api/nodes/${n.id}`, { method: "DELETE" });
      });
    });
  }
  async function linkRow(n) {
    try {
      const out = await nodeConnect(n.id);
      toast(out.ok ? t("linkedShort") : t("notLinked", { error: err(out.error) }));
    } catch (e) { setError(e); }
  }
  function reorder(dropId) {
    setOrder((cur) => {
      const ids0 = cur || nodes.map((n) => n.id);
      const from = ids0.indexOf(dragId);
      const to = ids0.indexOf(dropId);
      if (from < 0 || to < 0 || from === to) return cur;
      const ids = [...ids0];
      const [moved] = ids.splice(from, 1);
      ids.splice(to, 0, moved);
      return ids;
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

      {shownSelected > 0 && (
        <div className="bulkbar">
          {t("bulkSel", { n: shownSelected })}
          <button type="button" className="btn ghost sm" onClick={() => bulkEnabled(true)}>{t("toRot")}</button>
          <button type="button" className="btn ghost sm" onClick={() => bulkEnabled(false)}>{t("fromRot")}</button>
          <button type="button" className="btn ghost sm" onClick={() => bulkEnabled(false)}>{t("bulkDisable")}</button>
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
              <th></th>
            </tr></thead>
            <tbody>
              {loading && !data && <SkeletonRows cols={12} />}
              {data !== null && list.map((n) => {
                const st = statusOf(n, t, err);
                const seen = n.last_seen_at ? ago(n.last_seen_at) : "—";
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
              {data !== null && list.length === 0 && <EmptyRow colSpan={12}>{t("noNodes")}</EmptyRow>}
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
        <NewNodeModal onClose={() => setOpen(null)} reload={reload} setError={setError} />
      )}
    </>
  );
}

// Лампа + тултип по состоянию ноды: включённая и свежая — синяя пульс,
// уставший агент — янтарная, потерянная связь — красная, отключённая — серая.
function statusOf(n, t, err) {
  if (!n.enabled) return { lamp: "dis", tip: t("disLampTip") };
  const seen = n.last_seen_at ? ago(n.last_seen_at) : "—";
  if (n.fresh) {
    return {
      lamp: n.needs_update ? "warn" : "on",
      tip: n.needs_update ? t("warnLampTip", { ago: seen }) : t("onlineTip", { ago: seen }),
    };
  }
  const bad = err(n.last_error);
  return { lamp: "off", tip: t("offlineTip", { ago: seen }) + (bad ? `\n${bad}` : "") };
}

// Плашка-флаг региона (в лабе — эмодзи стран; регион в API — произвольная метка,
// поэтому код региона в лабовой плашке).
function monoFlag(region) {
  return region.slice(0, 2).toUpperCase();
}

function ipOf(n) {
  if (n.public_ipv4 && n.public_ipv6) {
    return <>{n.public_ipv4}<br /><span className="ipsmall">{n.public_ipv6}</span></>;
  }
  return n.public_ipv4 || n.public_ipv6 || "—";
}

/* Модалка редактирования (лаба 10): Состояние read-only, Виталы, Ключ; в футере
   🗑. Обновление шлёт полный NodeInput — грязь считает черновик против снимка. */
function EditNodeModal({ node, onClose, onSaved, setError, ask, toast }) {
  const { t, err } = useI18n();
  const [draft, setDraft] = useState(() => nodeInput(node, node.enabled));
  const [base] = useState(() => nodeInput(node, node.enabled));
  const [key, setKey] = useState(""); // разовый показ ключа после перевыпуска
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  const set = (k) => (e) => setDraft({ ...draft, [k]: e.target.value });

  // dirty: текстовые поля по строкам, порт — по числу («224» и 224 совпадают).
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
    } catch { /* нет clipboard — ключ остаётся виден в .tok */ }
  }

  const seen = node.last_seen_at ? ago(node.last_seen_at) : "—";
  const st = statusOf(node, t, err);
  const stateText = !node.enabled
    ? t("rotDis")
    : node.fresh ? `${t("online")} · ${seen}` : t("offlineTip", { ago: seen });

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
      {/* 1 · состояние: read-only снимок; hw-поля «—» до фазы 2 */}
      <section className="sect">
        <h3>{t("stateSect")}<small>{t("stateSectSub")}</small></h3>
        <div className="sbody">
          <div className="scol">
            <div className="col">
              <div className="kv"><span>{t("statusCol")}</span><b><Lamp state={st.lamp} /> {stateText}</b></div>
              <div className="kv"><span>{t("agent")}</span>{node.agent_version
                ? <b className={node.needs_update ? "ver old" : "ver"}>{node.agent_version}</b>
                : <b>—</b>}</div>
              <div className="kv"><span>{t("q24")}</span><b>—</b></div>
              <div className="kv"><span>{t("proxySessKv")}</span><b>—</b></div>
            </div>
            <div className="col">
              <div className="kv"><span>{t("hwCpu")}</span><b>—</b></div>
              <div className="kv"><span>{t("hwMem")}</span><b>—</b></div>
              <div className="kv"><span>{t("hwOs")}</span><b>—</b></div>
              <div className="kv"><span>{t("hwDisk")}</span><b>—</b></div>
            </div>
          </div>
          <div className="hint">{t("stateHwHint")}</div>
        </div>
      </section>

      {/* 2 · виталы: то, что реально хранит бэкенд. Вес не заводим — он
          принадлежит привязке домен↔нода и приедет со своим контрактом. */}
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

      {/* 3 · ключ связи: панель хранит его только зашифрованным; показ — после ⟳ */}
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

/* Краткий визард создания (задача 8 заменит лабовым со стикером связи):
   адрес → ключ и скрипт → проверка связи. */
function NewNodeModal({ onClose, reload, setError }) {
  const { t, err } = useI18n();
  const [step, setStep] = useState(0);
  const [created, setCreated] = useState(null);
  const [connect, setConnect] = useState(null);
  const [copied, setCopied] = useState("");
  const [busy, setBusy] = useState(false);

  async function copyText(label, text) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(label);
      setTimeout(() => setCopied(""), 1400);
    } catch { /* нет clipboard — текст остаётся виден в .tok */ }
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
      reload();
    } catch (e) { setError(e); }
    finally { setBusy(false); }
  }
  async function check() {
    if (busy || !created) return;
    setBusy(true);
    try {
      const out = await nodeConnect(created.node.id);
      setConnect(out);
      setStep(2);
      reload();
    } catch (e) { setError(e); }
    finally { setBusy(false); }
  }
  const install = created
    ? `dnsmarty-node install --role ${created.node.role} --key ${created.key} --port ${created.node.agent_port}`
    : "";

  const steps = [t("stepAddr"), t("stepKey"), t("stepLink")];
  return (
    <Modal open onClose={onClose} width={640} title={t("newNode")}>
      <div className="stepper">
        {steps.map((label, i) => (
          <span key={label} className={`step${i === step ? " on" : i < step ? " done" : ""}`}><b>{i < step ? "✓" : i + 1}</b>{label}</span>
        ))}
      </div>
      {step === 0 && (
        <form onSubmit={create}>
          <section className="sect">
            <h3>{t("stepAddr")}<small>{t("nodeHint")}</small></h3>
            <div className="sbody f">
              <NodeNewFields />
            </div>
          </section>
          <div className="mfoot">
            <span />
            <div className="mbtns">
              <button type="button" className="btn ghost" onClick={onClose}>{t("cancel")}</button>
              <button type="submit" className="btn" disabled={busy}>{t("next")}</button>
            </div>
          </div>
        </form>
      )}
      {step === 1 && created && (
        <section className="sect">
          <h3>{t("stepKey")}<small>{t("keySectSub")}</small></h3>
          <div className="sbody f">
            <div className="tok">
              {created.key}{" "}
              <button type="button" className="copybtn" onClick={() => copyText("key", created.key)}>
                {copied === "key" ? t("copied") : t("copy")}
              </button>
            </div>
            <div>
              <div className="hint">{t("scriptHint", { port: created.node.agent_port })}</div>
              <pre className="banner note" style={{ marginTop: 8 }}>{install}</pre>
              <button type="button" className="copybtn" onClick={() => copyText("cmd", install)}>
                {copied === "cmd" ? t("copied") : t("copy")}
              </button>{" "}
              <a href="/install/node.sh">{t("downloadScript")}</a>
            </div>
          </div>
          <div className="mfoot">
            <span />
            <div className="mbtns">
              <button type="button" className="btn ghost" onClick={onClose}>{t("cancel")}</button>
              <button type="button" className="btn" onClick={check} disabled={busy}>{t("checkLink")}</button>
            </div>
          </div>
        </section>
      )}
      {step === 2 && connect && (
        <>
          <section className="sect">
            <h3>{t("stepLink")}</h3>
            <div className="sbody">
              {connect.ok
                ? <div className="banner ok">{t("linked")}</div>
                : <div className="banner err">{t("notLinked", { error: err(connect.error) })}</div>}
            </div>
          </section>
          <div className="mfoot">
            <span />
            <div className="mbtns">
              {!connect.ok && <button type="button" className="btn ghost sm" onClick={check} disabled={busy}>{t("checkLink")}</button>}
              <button type="button" className="btn" onClick={onClose}>{t("close")}</button>
            </div>
          </div>
        </>
      )}
    </Modal>
  );
}

/* Поля формы создания: те же NodeInput, что и в модалке редактирования.
   Порт по умолчанию — по роли, как в dnsmarty-node.sh (dns → 9443, proxy → 9444)
   и в прежнем UI; роль управляемая, смена роли подставляет её порт по умолчанию. */
function NodeNewFields() {
  const { t } = useI18n();
  const [role, setRole] = useState("dns");
  return (
    <>
      <div className="frow">
        <div><label htmlFor="nn-name">{t("name")}</label><input id="nn-name" name="name" required /></div>
        <div><label htmlFor="nn-role">{t("role")}</label>
          <select id="nn-role" name="role" value={role} onChange={(e) => setRole(e.target.value)}><option value="dns">dns</option><option value="proxy">proxy</option></select>
        </div>
      </div>
      <div className="frow">
        <div><label htmlFor="nn-v4">{t("ipv4")}</label><input id="nn-v4" name="public_ipv4" placeholder="198.51.100.10" spellCheck={false} /></div>
        <div><label htmlFor="nn-v6">{t("ipv6")}</label><input id="nn-v6" name="public_ipv6" placeholder="2001:db8::10" spellCheck={false} /></div>
      </div>
      <div className="frow3">
        <div><label htmlFor="nn-region">{t("region")}</label><input id="nn-region" name="region" /></div>
        <div><label htmlFor="nn-host">{t("agentHost")}</label><input id="nn-host" name="agent_host" required /></div>
        <div><label htmlFor="nn-port">{t("agentPort")}</label><input id="nn-port" name="agent_port" key={role} defaultValue={role === "dns" ? "9443" : "9444"} required /></div>
      </div>
      <div className="check">
        <input type="checkbox" id="nn-enabled" name="enabled" defaultChecked />
        <label htmlFor="nn-enabled" style={{ margin: 0 }}>{t("inRotation")}</label>
      </div>
    </>
  );
}
