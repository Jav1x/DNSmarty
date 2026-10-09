/** Пилюля-фильтр `.pill` (ui.css). on — активное состояние. */
export default function Pill({ on = false, onClick, children, disabled }) {
  return (
    <button type="button" className={`pill${on ? " on" : ""}`} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  )
}
