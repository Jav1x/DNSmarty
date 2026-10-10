import * as RD from "@radix-ui/react-dialog"
import { useI18n } from "../i18n"

/** Radix Dialog в классах лаб (ui.css): .modal-back/.modal/.mhead/.mfoot.
    footer — правый блок кнопок футера (типично <TrashButton/> + Отмена/Сохранить);
    слева в футере — note (например dirty-счётчик). Ширина — width (px, по умолчанию 560;
    lab10: 860 для нод, lab15: 820 для сервиса). Закрытие: Esc, клик по подложке, ✕ —
    единый путь onOpenChange. Скролллок делает Radix (react-remove-scroll). */
export default function Modal({ open, onClose, title, children, footer, note, width = 560 }) {
  const { t } = useI18n()
  return (
    <RD.Root open={open} onOpenChange={(o) => { if (!o) onClose?.() }}>
      <RD.Portal>
        <RD.Overlay className="modal-back">
          <RD.Content className="modal" style={{ width: `min(${width}px, 100%)` }}>
            <div className="mhead">
              <RD.Title asChild>
                <h2>{title}</h2>
              </RD.Title>
              <RD.Close asChild>
                <button className="trashbtn" aria-label={t("close")} type="button">✕</button>
              </RD.Close>
            </div>
            {children}
            {footer && (
              <div className="mfoot">
                <span>{note}</span>
                <div className="mbtns">{footer}</div>
              </div>
            )}
          </RD.Content>
        </RD.Overlay>
      </RD.Portal>
    </RD.Root>
  )
}

export { Modal }
