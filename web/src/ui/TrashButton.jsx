import Tip from "./Tip"
import { useI18n } from "../i18n"

/** Кнопка-корзинка 🗑 (`.trashbtn`, ui.css) — единственный способ удаления.
    tip — пояснение действия (Radix Tooltip). */
export default function TrashButton({ tip, onClick, disabled }) {
  const { t } = useI18n()
  return (
    <Tip label={tip}>
      <button type="button" className="trashbtn" onClick={onClick} disabled={disabled} aria-label={t("delete")}>🗑</button>
    </Tip>
  )
}
