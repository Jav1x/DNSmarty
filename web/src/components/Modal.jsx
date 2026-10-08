import { useEffect, useRef } from "react";
import { X } from "lucide-react";
import { useI18n } from "../i18n";

// Modal traps focus inside, closes on Escape and restores focus to the opener.
// stacked puts the dialog above other open modals (a confirm inside an editor).
export function Modal({ title, onClose, children, wide, stacked }) {
  const { t } = useI18n();
  const box = useRef(null);
  const opener = useRef(null);

  useEffect(() => {
    opener.current = document.activeElement;
    const node = box.current;
    const focusables = () => node?.querySelectorAll('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])');
    focusables()?.[0]?.focus();
    function onKey(e) {
      if (e.key === "Escape") { onClose(); return; }
      if (e.key !== "Tab") return;
      const items = Array.from(focusables() || []);
      if (!items.length) return;
      const first = items[0], last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) { last.focus(); e.preventDefault(); }
      else if (!e.shiftKey && document.activeElement === last) { first.focus(); e.preventDefault(); }
    }
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      opener.current?.focus?.();
    };
  }, [onClose]);

  return (
    <div className={`modal-back${stacked ? " stacked" : ""}`} onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div className={`modal${wide ? " confirm" : ""}`} role="dialog" aria-modal="true" aria-label={title} ref={box}>
        <div className="modal-head">
          <h2>{title}</h2>
          <button type="button" className="ghost tiny" onClick={onClose} aria-label={t("close")}><X size={16} /></button>
        </div>
        {children}
      </div>
    </div>
  );
}
