import { level } from "@/lib/hostMetrics";

// UsageBar: uma barra de uso (amarela a partir de 80%, vermelha a partir
// de 90%) com o detalhe por baixo -- ex: "3.1 GB de 8.0 GB".
export default function UsageBar({ percent, detail, note }: { percent: number; detail: string; note?: string }) {
  const p = Math.max(0, Math.min(100, percent));
  return (
    <div className="usage" title={note}>
      <div className="usage-track">
        <div className={`usage-fill ${level(p)}`} style={{ width: `${p}%` }} />
      </div>
      <div className="usage-text">
        <strong>{Math.round(p)}%</strong> <span className="muted">{detail}</span>
      </div>
      {note && <div className="muted usage-note">{note}</div>}
    </div>
  );
}
