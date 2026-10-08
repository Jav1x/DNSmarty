import { useI18n } from "../i18n";

// Small shared widgets: skeleton rows, empty table cell, error banner, lamp, pager.
export function Err({ text }) {
  const { err } = useI18n();
  if (!text) return null;
  return <div className="banner err">{err(text)}</div>;
}

export function SkeletonRows({ cols, rows = 5 }) {
  return Array.from({ length: rows }, (_, i) => (
    <tr key={i}>
      {Array.from({ length: cols }, (_, j) => (
        <td key={j}><div className="skeleton" style={{ width: `${40 + ((i * 13 + j * 29) % 55)}%` }} /></td>
      ))}
    </tr>
  ));
}

export function EmptyRow({ colSpan, children }) {
  return <tr><td colSpan={colSpan} className="empty">{children}</td></tr>;
}

export function StatusLamp({ on }) {
  return <span className={`lamp ${on ? "on" : "off"}`} />;
}

export function Pager({ page, pages, onPage, label }) {
  const { t } = useI18n();
  return (
    <div className="pager">
      <span>{label}</span>
      <button type="button" className="ghost tiny" disabled={page === 0} onClick={() => onPage(page - 1)}>{t("prev")}</button>
      <span>{page + 1} {t("of")} {pages}</span>
      <button type="button" className="ghost tiny" disabled={page + 1 >= pages} onClick={() => onPage(page + 1)}>{t("nextPage")}</button>
    </div>
  );
}
