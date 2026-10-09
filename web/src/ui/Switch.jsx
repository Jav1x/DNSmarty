import * as RS from "@radix-ui/react-switch"

/** Radix Switch в лабовом классе .switch (ui.css; data-state="checked").
    on — включён; onChange(next) — смена; label — aria-label (может быть строкой i18n). */
export default function Switch({ on = false, onChange, disabled, label, id }) {
  return (
    <RS.Root
      className="switch"
      checked={on}
      onCheckedChange={onChange}
      disabled={disabled}
      aria-label={typeof label === "string" ? label : undefined}
      id={id}
    />
  )
}

export { Switch }
