import { Modal } from "./Modal";
import { useI18n } from "../i18n";

export function ConfirmDialog({ text, onYes, onClose, danger }) {
  const { t } = useI18n();
  return (
    <Modal title={t("confirmTitle")} onClose={onClose} wide>
      <p>{text}</p>
      <div className="modal-foot">
        <button type="button" className="ghost" onClick={onClose}>{t("cancel")}</button>
        <button type="button" className={danger ? "alarm" : ""} onClick={() => { onYes(); onClose(); }}>{t("confirmYes")}</button>
      </div>
    </Modal>
  );
}
