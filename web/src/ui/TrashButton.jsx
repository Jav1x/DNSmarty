import Tip from "./Tip"

/** Кнопка-корзинка 🗑 (`.trashbtn`, ui.css) — единственный способ удаления.
    tip — пояснение действия (Radix Tooltip). */
export default function TrashButton({ tip, onClick, disabled }) {
  return (
    <Tip label={tip}>
      <button type="button" className="trashbtn" onClick={onClick} disabled={disabled} aria-label="🗑">🗑</button>
    </Tip>
  )
}
