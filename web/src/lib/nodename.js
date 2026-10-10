export function nextNodeName(names) {
  let max = 0;
  for (const n of names || []) {
    const m = /^node-(\d+)$/.exec(String(n).trim());
    if (m) max = Math.max(max, Number(m[1]));
  }
  return `node-${max + 1}`;
}
