// iOS-style toggle. role="switch" with aria-checked; Enter and Space toggle natively for a button.
export function Switch({ checked, onChange, disabled, label }) {
  return (
    <button
      type="button"
      className="switch"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    />
  );
}
