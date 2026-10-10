export default function Pill({ on = false, onClick, children, disabled }) {
  return (
    <button type="button" className={`pill${on ? " on" : ""}`} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  )
}
