// Etiquetas (ex: gpu=nvidia) em texto: "chave=valor", separadas por
// vírgulas ou linhas. A validação a sério é no launcher.

export type Labels = Record<string, string>;

export function formatLabels(labels: Labels | undefined, sep = ", "): string {
  return Object.entries(labels ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}=${v}`)
    .join(sep);
}

// parseLabels devolve as etiquetas ou lança com a parte que não percebeu.
export function parseLabels(text: string): Labels {
  const out: Labels = {};
  for (const part of text.split(/[,\n]/)) {
    const item = part.trim();
    if (!item) continue;
    const eq = item.indexOf("=");
    if (eq <= 0) throw new Error(`"${item}" não é chave=valor`);
    out[item.slice(0, eq).trim()] = item.slice(eq + 1).trim();
  }
  return out;
}
