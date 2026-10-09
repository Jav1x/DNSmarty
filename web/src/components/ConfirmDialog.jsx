import { Modal } from "../ui/Modal";
import { useI18n } from "../i18n";

export function ConfirmDialog({ text, onYes, onClose, danger }) {
  const { t } = useI18n();
  return (
    <Modal open onClose={onClose} title={t("confirmTitle")} width={440}
      footer={<button type="button" className={danger ? "btn" : "btn ghost"}
        style={danger ? { background: "var(--danger)" } : undefined}
        onClick={() => { onYes(); onClose(); }}>{t("confirmYes")}</button>}
      note={<button type="button" className="btn ghost sm" onClick={onClose}>{t("cancel")}</button>}
    >
      <p>{text}</p>
    </Modal>
  );
}
