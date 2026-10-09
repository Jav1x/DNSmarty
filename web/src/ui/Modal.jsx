import { useEffect } from "react"
import * as RD from "@radix-ui/react-dialog"

/** Radix Dialog в классах лаб (ui.css): .modal-back/.modal/.mhead/.mfoot.
    footer — правый блок кнопок футера (типично <TrashButton/> + Отмена/Сохранить);
    слева в футере — note (например dirty-счётчик). Ширина — width (px, по умолчанию 560;
    lab10: 860 для нод, lab15: 820 для сервиса). Закрытие: Esc, клик по подложке, ✕. */
export default function Modal({ open, onClose, title, children, footer, note, width = 560 }) {
  // Блокировка скролла страницы, пока модалка открыта (портал Radix).
  useEffect(() => {
    if (!open) return
    const prev = document.body.style.overflow
    document.body.style.overflow = "hidden"
    return () => { document.body.style.overflow = prev }
  }, [open])

  return (
    <RD.Root open={open} onOpenChange={(o) => { if (!o) onClose?.() }}>
      <RD.Portal>
        <RD.Overlay className="modal-back" style={{ zIndex: 50 }} />
        <RD.Content
          className="modal"
          style={{ width: `min(${width}px, 100%)` }}
          onEscapeKeyDown={() => onClose?.()}
          onPointerDownOutside={() => onClose?.()}
        >
          <div className="mhead">
            <RD.Title asChild>
              <h2>{title}</h2>
            </RD.Title>
            <RD.Close asChild>
              <button className="trashbtn" aria-label="✕" type="button">✕</button>
            </RD.Close>
          </div>
          {children}
          {footer && (
            <div className="mfoot">
              <span className="mnote">{note}</span>
              <div className="mbtns">{footer}</div>
            </div>
          )}
        </RD.Content>
      </RD.Portal>
    </RD.Root>
  )
}

export { Modal }
