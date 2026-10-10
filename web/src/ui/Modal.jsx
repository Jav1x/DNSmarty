import * as RD from "@radix-ui/react-dialog"
import { useI18n } from "../i18n"

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
