// Имя новой ноды визарда (задача 8): «node-{N}» по существующим нодам —
// максимум уже занятого номера + 1 (бриф: «по числу существующих»; максимум,
// а не количество: node.name UNIQUE — после удаления node-2 «количество + 1»
// предложило бы занятое имя).
export function nextNodeName(names) {
  let max = 0;
  for (const n of names || []) {
    const m = /^node-(\d+)$/.exec(String(n).trim());
    if (m) max = Math.max(max, Number(m[1]));
  }
  return `node-${max + 1}`;
}
