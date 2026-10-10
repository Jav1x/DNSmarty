import * as RS from "@radix-ui/react-switch"

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
