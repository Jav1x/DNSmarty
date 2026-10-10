import { useI18n } from "../i18n"

export default function Savebar({ dirty = false, count = 0, reset, save, children }) {
  const { t } = useI18n()
  return (
    <div className={`savebar${dirty ? " unsaved" : ""}`}>
      <span className="st">
        {dirty ? (<><b>●</b>&nbsp;{t("unsaved", { n: count })}</>) : <span className="muted">● {t("saved")}</span>}
      </span>
      <div className="mbtns">
        {children}
        <button type="button" className="btn ghost" onClick={reset} disabled={!dirty}>{t("reset")}</button>
        <button type="button" className="btn" onClick={save} disabled={!dirty}>{t("save")}</button>
      </div>
    </div>
  )
}
