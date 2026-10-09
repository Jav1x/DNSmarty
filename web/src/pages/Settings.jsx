// Настройки — порт lab13: нумерованные секции 01–04, sticky-сейвбар,
// drag-таблица апстримов с чипами протокола (вычисляются на клиенте).
// Секции Bootstrap больше нет; поле bootstrap_cidr в API не трогаем —
// уходит в теле сохранения серверное значение без изменений.
// Сетевой контракт прежний: GET /api/settings, POST /api/settings,
// POST /api/upstreams, POST /api/upstreams/:id, DELETE /api/upstreams/:id.
import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { useLoad } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { Err, SkeletonRows } from "../components/Bits";
import { useConfirm, useToast } from "../components/Toast";
import Savebar from "../ui/Savebar";
import { protoOf } from "../lib/util";

// Редактируемые поля настроек; bootstrap_cidr сюда не входит.
const FIELDS = ["ttl", "pull_interval_sec", "retention_days", "session_limit", "dial_timeout_ms", "idle_timeout_ms", "dns_rate_qps", "audit_retention_days", "agent_image"];

// Копия настроек и апстримов из данных сервера — для сверки dirty.
function serverCopy(data) {
  return { settings: { ...data.settings }, upstreams: (data.upstreams || []).map((u) => ({ ...u })) };
}
function formOf(s) {
  const f = {};
  for (const k of FIELDS) f[k] = s[k] ?? "";
  f.bootstrap_cidr = s.bootstrap_cidr ?? ""; // не редактируется, но уходит в теле как раньше
  return f;
}
function rowsOf(upstreams) {
  return upstreams.map((u) => ({ key: u.id, id: u.id, addr: u.addr }));
}

export function Settings() {
  const { t } = useI18n();
  const { data, loading, error, setError, reload } = useLoad("/api/settings");
  const [ask, confirmRow] = useConfirm();
  const toast = useToast();
  const [server, setServer] = useState(null); // последняя серверная копия
  const [form, setForm] = useState(null);     // редактируемые значения
  const [rows, setRows] = useState([]);       // апстримы: {key, id|null, addr}
  // drag-перестановка строк (лаба 13): строка-источник и строка-цель
  const [dragKey, setDragKey] = useState(null);
  const [overKey, setOverKey] = useState(null);

  // Новые данные с сервера попадают в состояние, только когда это разрешено
  // (на старте и после сохранения), чтобы reload не затёр несохранённые правки.
  const syncRef = useRef(true);
  useEffect(() => {
    if (!data) return;
    const sv = serverCopy(data);
    setServer(sv);
    if (syncRef.current) {
      setForm(formOf(data.settings));
      setRows(rowsOf(data.upstreams || []));
      syncRef.current = false;
    }
  }, [data]);

  // Сколько значений расходится с серверной копией.
  function countDirty() {
    if (!form || !server) return 0;
    let n = 0;
    for (const k of FIELDS) if (String(form[k]) !== String(server.settings[k] ?? "")) n++;
    const svRows = server.upstreams;
    for (let i = 0; i < rows.length; i++) {
      const r = rows[i];
      if (!r.id) { if (r.addr.trim()) n++; continue; } // новая строка с адресом
      const ui = svRows.findIndex((x) => x.id === r.id);
      if (ui < 0) { n++; continue; }
      // сверка с позицией в серверном списке (а не с сырым ordinal — он может
      // приходить с пропусками от прежнего UI): правка адреса или перестановка
      if (r.addr !== svRows[ui].addr || ui !== i) n++;
    }
    n += svRows.filter((u) => !rows.some((r) => r.id === u.id)).length; // удалённые
    return n;
  }
  const dirtyCount = countDirty();
  const dirty = dirtyCount > 0;

  function setField(k) {
    return (e) => setForm({ ...form, [k]: e.target.value });
  }
  function setRow(key) {
    return (e) => setRows(rows.map((r) => (r.key === key ? { ...r, addr: e.target.value } : r)));
  }
  function addRow() {
    const key = `new-${Date.now()}`;
    setRows([...rows, { key, id: null, addr: "" }]);
  }
  function removeRow(r) {
    if (!r.id) { setRows(rows.filter((x) => x.key !== r.key)); return; }
    ask(t("confirmDelete", { name: r.addr }), () => {
      api(`/api/upstreams/${r.id}`, { method: "DELETE" })
        .then(() => {
          setRows((rs) => rs.filter((x) => x.key !== r.key));
          setServer((sv) => ({ ...sv, upstreams: sv.upstreams.filter((u) => u.id !== r.id) }));
        })
        .catch(setError);
    });
  }
  function dropRow(key) {
    setRows((rs) => {
      const from = rs.findIndex((r) => r.key === dragKey);
      const to = rs.findIndex((r) => r.key === key);
      if (from < 0 || to < 0 || from === to) return rs;
      const next = [...rs];
      const [moved] = next.splice(from, 1);
      next.splice(to, 0, moved);
      return next;
    });
    setDragKey(null);
    setOverKey(null);
  }

  function settingsBody() {
    const b = {};
    for (const [k, v] of Object.entries(form)) b[k] = k === "agent_image" || k === "bootstrap_cidr" || v === "" || Number.isNaN(Number(v)) ? v : Number(v);
    return b;
  }
  async function save() {
    try {
      // Апстримы: новые — создать, изменённые — обновить (порядок 1..N).
      for (let i = 0; i < rows.length; i++) {
        const r = rows[i];
        if (!r.id) {
          if (r.addr.trim()) await api("/api/upstreams", { method: "POST", body: JSON.stringify({ addr: r.addr.trim() }) });
        } else {
          const u = server.upstreams.find((x) => x.id === r.id);
          if (u && (u.addr !== r.addr || u.ordinal !== i + 1)) {
            await api(`/api/upstreams/${r.id}`, { method: "POST", body: JSON.stringify({ addr: r.addr.trim(), ordinal: i + 1 }) });
          }
        }
      }
      await api("/api/settings", { method: "POST", body: JSON.stringify(settingsBody()) });
      toast(t("saved"));
      syncRef.current = true; // после сохранения форма заново сверится с сервером
      reload();
    } catch (e) { setError(e); }
  }
  // «Сбросить» возвращает серверные значения.
  function reset() {
    setForm(formOf(server.settings));
    setRows(rowsOf(server.upstreams));
  }

  if (loading && !data) return <div><Err text={error} />{!error && <SkeletonRows cols={4} />}</div>;
  if (!form || !server) return <Err text={error} />;

  const chip = { doh: "DoH", dot: "DoT", udp: "UDP" };
  const label = (key, apply, tip) => (
    <label>
      <span className={tip ? "tip" : undefined} data-tip={tip ? t(tip) : undefined}>{t(key)}</span>
      {apply && <span className={`apply ${apply === "now" ? "now" : "push"}`}>{t(apply === "now" ? "applyNow" : "applyPush")}</span>}
    </label>
  );

  return (
    <>
      {confirmRow}
      <Err text={error} />
      <div className="hrow">
        <h1>{t("settings")}</h1>
        <span className="crumb">{t("settingsSub")}</span>
      </div>
      <Savebar dirty={dirty} count={dirtyCount} reset={reset} save={save} />

      {/* 01 · резолвер */}
      <section className="sect">
        <h2><span className="wrapl"><span className="snum">01</span>{t("resolvers")} <small>{t("resolversSub")}</small></span></h2>
        <div className="sbody">
          <div className="f">
            <label>
              {t("upServers")}
              <span className="prio">{t("prioDohDot")}</span>
              <span className="apply push">{t("applyPush")}</span>
            </label>
            <table className="uptable">
              <tbody>
                {rows.map((r, i) => {
                  const p = protoOf(r.addr);
                  return (
                    <tr
                      key={r.key}
                      draggable
                      onDragStart={() => setDragKey(r.key)}
                      onDragEnd={() => { setDragKey(null); setOverKey(null); }}
                      onDragOver={(e) => { e.preventDefault(); if (r.key !== dragKey) setOverKey(r.key); }}
                      onDrop={(e) => { e.preventDefault(); dropRow(r.key); }}
                      className={`${r.key === dragKey ? "dragging" : ""} ${r.key === overKey && r.key !== dragKey ? "drop-before" : ""}`}
                    >
                      <td className="drag">⠿</td>
                      <td className="chipcol"><span className={`protochip ${p === "doh" ? "doh" : p === "dot" ? "tls" : ""}`}>{chip[p]}</span></td>
                      <td>
                        <input type="text" value={r.addr} placeholder="9.9.9.9:53" onChange={setRow(r.key)} autoFocus={!r.id} />
                      </td>
                      <td className="upact">
                        <button type="button" className="abtn del" title={t("delete")} onClick={() => removeRow(r)}>✕</button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {!rows.length && <p className="hint">{t("upstreamEmpty")}</p>}
            <div className="hint">{t("upHint")}</div>
            <p><button type="button" className="btn ghost sm" onClick={addRow}>+ {t("addServer")}</button></p>
          </div>
          <div className="grid2">
            <div className="f">
              {label("ttl", "now", "tipTtl")}
              <input type="number" min="1" max="300" value={form.ttl} onChange={setField("ttl")} />
              <div className="hint">{t("ttlHint")}</div>
            </div>
            <div className="f">
              {label("dnsRate", "now", "tipQps")}
              <input type="number" min="0" max="100000" value={form.dns_rate_qps} onChange={setField("dns_rate_qps")} />
              <div className="hint">{t("qpsHint")}</div>
            </div>
          </div>
        </div>
      </section>

      {/* 02 · журналы */}
      <section className="sect">
        <h2><span className="wrapl"><span className="snum">02</span>{t("logs")} <small>{t("logsSub")}</small></span></h2>
        <div className="sbody">
          <div className="grid2">
            <div className="f">
              {label("retention", null, "tipRetention")}
              <input type="number" min="1" max="30" value={form.retention_days} onChange={setField("retention_days")} />
              <div className="hint">{t("retHint")}</div>
            </div>
            <div className="f">
              {label("auditRetention", null, "tipAudit")}
              <input type="number" min="7" max="3650" value={form.audit_retention_days} onChange={setField("audit_retention_days")} />
              <div className="hint">{t("audHint")}</div>
            </div>
          </div>
        </div>
      </section>

      {/* 03 · прокси */}
      <section className="sect">
        <h2><span className="wrapl"><span className="snum">03</span>{t("proxyTitle")} <small>{t("proxySessSub")}</small></span></h2>
        <div className="sbody">
          <div className="grid3">
            <div className="f">
              {label("sessionsPerIp", null, "tipSess")}
              <input type="number" min="1" max="10000" value={form.session_limit} onChange={setField("session_limit")} />
              <div className="hint">{t("sessHint")}</div>
            </div>
            <div className="f">
              {label("dialMs", null, "tipDial")}
              <input type="number" min="100" max="60000" value={form.dial_timeout_ms} onChange={setField("dial_timeout_ms")} />
              <div className="hint">{t("dialHint")}</div>
            </div>
            <div className="f">
              {label("idleMs", null, "tipIdle")}
              <input type="number" min="1000" max="600000" value={form.idle_timeout_ms} onChange={setField("idle_timeout_ms")} />
              <div className="hint">{t("idleHint")}</div>
            </div>
          </div>
        </div>
      </section>

      {/* 04 · агенты */}
      <section className="sect">
        <h2><span className="wrapl"><span className="snum">04</span>{t("agents")} <small>{t("agentsSub")}</small></span></h2>
        <div className="sbody">
          <div className="grid2">
            <div className="f">
              {label("pushInterval", "now", "tipPoll")}
              <input type="number" min="2" max="15" value={form.pull_interval_sec} onChange={setField("pull_interval_sec")} />
              <div className="hint">{t("pollHint")}</div>
            </div>
            <div className="f">
              {label("agentImage", "push", "tipImage")}
              <input type="text" value={form.agent_image} onChange={setField("agent_image")} />
              <div className="hint">{t("imgHint")}</div>
            </div>
          </div>
        </div>
      </section>
      <div className="endpad" />
    </>
  );
}
