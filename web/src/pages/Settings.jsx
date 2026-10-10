import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { useLoad } from "../hooks/useLoad";
import { useI18n } from "../i18n";
import { Err, SkeletonRows } from "../components/Bits";
import { useConfirm, useToast } from "../components/Toast";
import Savebar from "../ui/Savebar";
import { Modal } from "../ui/Modal";
import { Switch } from "../ui/Switch";
import { protoOf } from "../lib/util";

const FIELDS = ["ttl", "pull_interval_sec", "retention_days", "session_limit", "dial_timeout_ms", "idle_timeout_ms", "dns_rate_qps", "audit_retention_days", "agent_image"];

function serverCopy(data) {
  return { settings: { ...data.settings }, upstreams: (data.upstreams || []).map((u) => ({ ...u })) };
}
function formOf(s) {
  const f = {};
  for (const k of FIELDS) f[k] = s[k] ?? "";
  f.bootstrap_cidr = s.bootstrap_cidr ?? "";
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
  const [server, setServer] = useState(null);
  const [form, setForm] = useState(null);
  const [rows, setRows] = useState([]);
  const [dragKey, setDragKey] = useState(null);
  const [overKey, setOverKey] = useState(null);

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

  function countDirty() {
    if (!form || !server) return 0;
    let n = 0;
    for (const k of FIELDS) if (String(form[k]) !== String(server.settings[k] ?? "")) n++;
    const svRows = server.upstreams;
    for (let i = 0; i < rows.length; i++) {
      const r = rows[i];
      if (!r.id) { if (r.addr.trim()) n++; continue; }
      const ui = svRows.findIndex((x) => x.id === r.id);
      if (ui < 0) { n++; continue; }
      if (r.addr !== svRows[ui].addr || ui !== i) n++;
    }
    n += svRows.filter((u) => !rows.some((r) => r.id === u.id)).length;
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
      syncRef.current = true;
      reload();
    } catch (e) { setError(e); }
  }
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
      <OAuthSettings />
      <div className="endpad" />
    </>
  );
}

const OAUTH_NAME = {
  google: "oauthNameGoogle",
  github: "oauthNameGitHub",
  yandex: "oauthNameYandex",
};
const OAUTH_STEPS = ["oauthStepProvider", "oauthStepKeys", "oauthStepUrls"];

function oauthAddresses(id) {
  const origin = window.location.origin;
  const callback = `${origin}/api/auth/oauth/${id}/callback`;
  if (id === "github") {
    return [
      { key: "home", label: "oauthUrlHome", hint: "oauthUrlHomeHint", value: origin },
      { key: "cb", label: "oauthUrlCallback", hint: "oauthUrlCallbackHint", value: callback },
    ];
  }
  if (id === "yandex") {
    return [
      { key: "site", label: "oauthUrlSite", hint: "oauthUrlSiteHint", value: origin },
      { key: "cb", label: "oauthUrlCallbackYandex", hint: "oauthUrlCallbackYandexHint", value: callback },
    ];
  }
  return [
    { key: "origin", label: "oauthUrlOrigin", hint: "oauthUrlOriginHint", value: origin },
    { key: "redir", label: "oauthUrlRedirect", hint: "oauthUrlRedirectHint", value: callback },
  ];
}

function OAuthSettings() {
  const { t } = useI18n();
  const { data, error, reload } = useLoad("/api/settings/oauth");
  const [editing, setEditing] = useState(null);
  const providers = data?.providers || [];

  return (
    <section className="sect">
      <h2><span className="wrapl"><span className="snum">05</span>{t("oauthSect")} <small>{t("oauthSectSub")}</small></span></h2>
      <div className="sbody">
        <Err text={error} />
        {providers.map((p) => (
          <div className="oauth-line" key={p.id}>
            <span className={`lamp${p.enabled ? " on" : ""}`} aria-hidden="true" />
            <b>{t(OAUTH_NAME[p.id] || p.name)}</b>
            <span className="st">{p.enabled ? t("oauthOn") : t("oauthOff")}</span>
            <button type="button" className="btn sm" onClick={() => setEditing(p.id)}>{t("oauthConfigure")}</button>
          </div>
        ))}
      </div>
      {editing && (
        <OAuthModal
          provider={providers.find((p) => p.id === editing)}
          providers={providers}
          onClose={() => setEditing(null)}
          onSaved={reload}
        />
      )}
    </section>
  );
}

function OAuthModal({ provider, providers, onClose, onSaved }) {
  const { t } = useI18n();
  const [step, setStep] = useState(1);
  const [pick, setPick] = useState(provider?.id || "");
  const [clientId, setClientId] = useState(provider?.client_id || "");
  const [secret, setSecret] = useState("");
  const [enabled, setEnabled] = useState(!!provider?.enabled);
  const [hasSecret, setHasSecret] = useState(!!provider?.has_secret);
  const [copied, setCopied] = useState("");
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const current = providers.find((p) => p.id === pick) || provider;
  const name = pick ? t(OAUTH_NAME[pick] || current?.name || pick) : t("oauthStepProvider");

  function choose(id) {
    const found = providers.find((p) => p.id === id);
    setPick(id);
    setClientId(found?.client_id || "");
    setSecret("");
    setEnabled(!!found?.enabled);
    setHasSecret(!!found?.has_secret);
    setSaved(false);
    setError("");
  }

  async function copy(value) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(value);
    } catch (e) { setError(e); }
  }

  async function save(event) {
    event.preventDefault();
    setError("");
    try {
      await api("/api/settings/oauth", {
        method: "POST",
        body: JSON.stringify({ provider: pick, client_id: clientId, secret, enabled }),
      });
      setSaved(true);
      setSecret("");
      if (secret) setHasSecret(true);
      onSaved();
    } catch (e) { setError(e); }
  }

  return (
    <Modal
      open
      onClose={onClose}
      width={720}
      title={t("oauthSect")}
      note={<span className="dirty">{name} · {t(OAUTH_STEPS[step])}</span>}
      footer={(
        <>
          <button type="button" className="btn ghost" onClick={step === 0 ? onClose : () => setStep(step - 1)}>{step === 0 ? t("cancel") : t("back")}</button>
          {step < 2 && (
            <button type="button" className="btn" disabled={step === 0 && !pick} onClick={() => setStep(step + 1)}>{t("next")} →</button>
          )}
          {step === 2 && !saved && <button type="submit" form="oauth-save" className="btn">{t("save")}</button>}
          {step === 2 && saved && <button type="button" className="btn" onClick={onClose}>{t("totpDone")}</button>}
        </>
      )}
    >
      <div className="stepper">
        {OAUTH_STEPS.map((key, i) => (
          <span className="stepwrap" key={key}>
            {i > 0 && <span className="starrow" aria-hidden="true">→</span>}
            <span className={`step${i === step ? " on" : i < step ? " done" : ""}`}>
              <span className="n">{i < step ? "✓" : i + 1}</span>{t(key)}
            </span>
          </span>
        ))}
      </div>
      {step === 0 && (
        <div className="tplgrid">
          {providers.map((p) => (
            <button type="button" key={p.id} className={`tplcard${p.id === pick ? " on" : ""}`} onClick={() => choose(p.id)}>
              <b>{t(OAUTH_NAME[p.id] || p.name)}</b>
              <span>{p.enabled ? t("oauthOn") : t("oauthOff")}</span>
            </button>
          ))}
        </div>
      )}
      {step === 1 && (
        <form className="code-step" onSubmit={(e) => { e.preventDefault(); setStep(2); }}>
          <Err text={error} />
          <div className="f">
            <label htmlFor="oa-client">{t("oauthClientID")}</label>
            <input id="oa-client" value={clientId} spellCheck={false} autoComplete="off" onChange={(e) => { setClientId(e.target.value); setSaved(false); }} />
          </div>
          <div className="f">
            <label htmlFor="oa-secret">{t("oauthClientSecret")}</label>
            <input id="oa-secret" type="password" value={secret} autoComplete="new-password" placeholder={hasSecret ? "••••••••" : ""} onChange={(e) => { setSecret(e.target.value); setSaved(false); }} />
          </div>
          <div className="swrow">
            <span className="t"><b>{t("enabled")}</b><span>{t("oauthSecretKeep")}</span></span>
            <Switch on={enabled} onChange={(v) => { setEnabled(v); setSaved(false); }} label={t("enabled")} />
          </div>
        </form>
      )}
      {step === 2 && (
        <form id="oauth-save" className="code-step" onSubmit={save}>
          <Err text={error} />
          {saved && <div className="banner ok">{t("oauthSaved")}</div>}
          <p className="hint">{t("oauthUrlLead", { name })}</p>
          {oauthAddresses(pick).map((row) => (
            <div className="oauth-url" key={row.key}>
              <label>{t(row.label)}</label>
              <p className="hint">{t(row.hint)}</p>
              <div className="tok">
                <span>{row.value}</span>
                <button type="button" className="copybtn" onClick={() => copy(row.value)}>{copied === row.value ? t("copied") : t("copy")}</button>
              </div>
            </div>
          ))}
        </form>
      )}
    </Modal>
  );
}
