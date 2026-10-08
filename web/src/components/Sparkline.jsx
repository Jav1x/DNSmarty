// Inline SVG sparkline: an area under a line, no chart library.
// values are plotted in order; danger marks the refused series.
export function Sparkline({ values, danger, height = 40 }) {
  const max = Math.max(1, ...values);
  const n = values.length;
  if (n < 2) return <svg className="spark" viewBox={`0 0 100 ${height}`} aria-hidden="true" />;
  const x = (i) => (i / (n - 1)) * 100;
  const y = (v) => height - 3 - (v / max) * (height - 6);
  const pts = values.map((v, i) => `${x(i).toFixed(2)},${y(v).toFixed(2)}`).join(" ");
  const area = `0,${height} ${pts} 100,${height}`;
  return (
    <svg className="spark" viewBox={`0 0 100 ${height}`} preserveAspectRatio="none" aria-hidden="true">
      <polygon className="fill" points={area} style={danger ? { fill: "var(--danger-soft)" } : undefined} />
      <polyline className={`line${danger ? " danger" : ""}`} points={pts} />
    </svg>
  );
}
