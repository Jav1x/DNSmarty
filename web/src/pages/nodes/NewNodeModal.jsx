// Визард «Новая нода» — порт модалки mb2 из .design-lab/lab10.html (задача 8).
// Два экрана: 1) «Ключ и адрес» — ток ключа (копировать, «показан один раз»),
// IP ноды, порт агента (по умолчанию — по роли: dns → 9443, proxy → 9444, как
// default_port() в scripts/dnsmarty-node.sh), роль; 2) «Связь» — цикл опроса
// раз в 3 с (телеграм-стикер из лабы, счётчик попыток, без лимита), при ok —
// успех и «К таблице нод».
//
// Порядок шагов диктует замороженный бэкенд: ключ ноды существует только в
// ответе POST /api/nodes, а agent_host там обязателен (normalizeNode) — создать
// ноду до ввода адреса нельзя. Поэтому первый «Далее» создаёт ноду (имя авто
// node-{N} по существующим) и раскрывает ключ прямо на экране 1 — ровно то
// состояние, которое рисует лаба (её футер-примечание «нода уже создана в
// панели · ключ больше не покажется»); второй «Далее» уводит на экран 2 и
// запускает опрос. До создания в .tok — нейтральная заглушка.
//
// Цикл опроса: по брифу — «GET», фактический верб — POST /api/nodes/{id}/connect
// (маршрут server.go:52; бэкенд менять нельзя — тот же расход верба, что в
// задаче 7). Интервал живёт в ref и гасится на «Отмене», закрытии модалки и
// размонтировании (useEffect-cleanup + alive-ref: тик, ворвавшийся после
// отмены, не делает setState).
import { useEffect, useRef, useState } from "react";
import { api } from "../../api";
import { useI18n } from "../../i18n";
import Modal from "../../ui/Modal";
import { nextNodeName } from "../../lib/nodename";

export default function NewNodeModal({ nodes, onClose, reload, setError }) {
  const { t } = useI18n();
  // 0 — «Ключ и адрес», 1 — «Связь» (в лабе — страницы pg1/pg2 степпера).
  const [step, setStep] = useState(0);
  const [created, setCreated] = useState(null); // {node, key} из ответа create
  const [role, setRole] = useState("dns");
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);
  // Состояние цикла опроса: attempt — номер попытки, linked — null (идёт опрос)
  // | true (связь есть). timerRef — id setInterval, alive — признак живого цикла.
  const [attempt, setAttempt] = useState(0);
  const [linked, setLinked] = useState(null);
  const timerRef = useRef(null);
  const aliveRef = useRef(false);

  // Единственная точка остановки цикла: «Отмена»/«К таблице нод», закрытие
  // модалки, размонтирование, успех. Тик, застрявший в сети на момент отмены,
  // увидит alive=false и не тронет состояние закрытого компонента.
  function stopPolling() {
    aliveRef.current = false;
    if (timerRef.current) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }
  }
  useEffect(() => stopPolling, []); // размонтирование модалки

  function startPolling(id) {
    stopPolling();
    aliveRef.current = true;
    setLinked(null);
    let n = 1;
    setAttempt(n);
    async function tick() {
      if (!aliveRef.current) return;
      try {
        const out = await api(`/api/nodes/${id}/connect`, { method: "POST" });
        if (!aliveRef.current) return;
        if (out.ok) {
          stopPolling();
          setLinked(true);
          reload(); // агент ответил — в таблице появятся версия и «свежесть»
          return;
        } // ok:false — агент ещё молчит, попытка мимо; цикл продолжается
      } catch { /* сеть/сервер — попытка без ответа; без лимита попыток */ }
      if (!aliveRef.current) return;
      n += 1;
      setAttempt(n);
    }
    tick(); // первая попытка сразу, дальше — раз в 3 с
    timerRef.current = setInterval(tick, 3000);
  }

  async function create(event) {
    event.preventDefault();
    if (busy || created) return;
    const form = new FormData(event.target);
    setBusy(true);
    try {
      // lab10: один «IP ноды» — он же адрес дозвона; в контракте это
      // agent_host + public_ipv4 одним значением. Региона/IPv6 в лабе нет.
      const ip = String(form.get("ip") || "").trim();
      const out = await api("/api/nodes", {
        method: "POST",
        body: JSON.stringify({
          name: nextNodeName((nodes || []).map((n) => n.name)),
          role,
          public_ipv4: ip,
          public_ipv6: "",
          region: "",
          agent_host: ip,
          agent_port: Number(form.get("agent_port")),
          enabled: false, // лаба: после связи нода «в дежурном режиме», «В ротации» включают отдельно
        }),
      });
      setCreated(out);
      reload();
    } catch (e) { setError(e); }
    finally { setBusy(false); }
  }

  async function copyKey() {
    try {
      await navigator.clipboard.writeText(created.key);
      setCopied(true);
      setTimeout(() => setCopied(false), 1400);
    } catch { /* нет clipboard — ключ остаётся виден в .tok */ }
  }

  // «Отмена», «К таблице нод», Esc, ✕, клик по подложке — один путь: стоп
  // цикла и закрытие; список нод уже перечитан (reload после create/успеха).
  function toTable() {
    stopPolling();
    onClose();
  }

  const fresh = created ? (nodes || []).find((n) => n.id === created.node.id) : null;
  const port = created?.node.agent_port;

  return (
    <Modal
      open
      onClose={toTable}
      width={620}
      title={<>{t("newNode")} <small className="nn-lead">{t("nnLead")}</small></>}
      note={<span className="dirty">
        {step === 1 ? t("nnPollNote") : created ? t("nnCreatedNote") : t("nnPreNote")}
      </span>}
      footer={(
        <>
          <button type="button" className="btn ghost" onClick={toTable}>{t("cancel")}</button>
          {step === 0 && !created && (
            <button type="submit" form="nn-form" className="btn" disabled={busy}>{t("next")} →</button>
          )}
          {step === 0 && created && (
            <button type="button" className="btn" onClick={() => { setStep(1); startPolling(created.node.id); }}>
              {t("next")} →
            </button>
          )}
          {step === 1 && (
            <button type="button" className="btn" onClick={toTable}>{t("nnToTable")}</button>
          )}
        </>
      )}
    >
      <div className="stepper">
        <span className={`step${step === 0 ? " on" : ""}`}><span className="n">1</span>{t("nnStepKeyAddr")}</span>
        <span className="starrow" aria-hidden="true">→</span>
        <span className={`step${step === 1 ? " on" : created ? " done" : ""}`}>
          <span className="n">{step === 1 || !created ? "2" : "✓"}</span>{t("stepLink")}
        </span>
      </div>

      {step === 0 && (
        <form id="nn-form" onSubmit={create}>
          <section className="sect">
            <h3>{t("nnStepKeyAddr")}<small>{t("nnKeyAddrSub")}</small></h3>
            <div className="sbody f">
              {created ? (
                <div className="tok nn-tok">
                  <span>{created.key}</span>
                  <span className="nn-tokside">
                    <span className="nn-once">{t("nnOnce")}</span>
                    <button type="button" className="copybtn" onClick={copyKey}>{copied ? t("copied") : t("copy")}</button>
                  </span>
                </div>
              ) : (
                <div className="tok nn-tok nn-later">{t("nnKeyLater")}</div>
              )}
              <div className="frow3">
                <div>
                  <label htmlFor="nn-ip">{t("nnIp")}</label>
                  <input id="nn-ip" name="ip" placeholder={t("nnIpPh")} spellCheck={false} required disabled={!!created} />
                </div>
                <div>
                  <label htmlFor="nn-port">{t("agentPort")}</label>
                  <input id="nn-port" name="agent_port" type="number" min="1" max="65535" key={role}
                    defaultValue={role === "dns" ? "9443" : "9444"} required disabled={!!created} />
                </div>
                <div>
                  <label htmlFor="nn-role">{t("role")}</label>
                  <select id="nn-role" value={role} onChange={(e) => setRole(e.target.value)} disabled={!!created}>
                    <option value="dns">dns</option>
                    <option value="proxy">proxy</option>
                  </select>
                </div>
              </div>
            </div>
          </section>
        </form>
      )}

      {step === 1 && created && (
        <section className="sect">
          <h3>{t("stepLink")}<small>{t("nnPollSub")}</small></h3>
          <div className="sbody nn-center">
            <div className={`stick${linked ? " done" : ""}`}>
              <span className="ping"></span><span className="ping d2"></span><span className="ping d3"></span>
              <span className="orb"></span>
              <div className="srv"><i></i><i></i><span className="lamp"></span></div>
              <span className="tick">✓</span>
            </div>
            {linked ? (
              <div className="waitrow done nn-wait nn-done">
                <span className="wl"></span>
                <span>{t("nnLinkedA")}<b>{fresh?.agent_version || "—"}</b>{t("nnLinkedB")}
                  <small>{t("nnDoneSub")}</small></span>
              </div>
            ) : (
              <div className="waitrow nn-wait nn-try">
                <span className="wl"></span>
                <span>{created.node.agent_host}:{port} · {t("nnAttemptWord")} <b>{attempt}</b>…
                  <small>{t("nnWaitSub", { port })}</small></span>
              </div>
            )}
          </div>
        </section>
      )}
    </Modal>
  );
}
