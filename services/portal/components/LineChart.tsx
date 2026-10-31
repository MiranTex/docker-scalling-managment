// LineChart: um gráfico de linha em SVG, sem bibliotecas -- para as
// séries curtas das métricas das máquinas (as últimas horas).
export type Series = { label: string; values: number[]; color?: string };

const W = 600;
const H = 160;
const PAD = { top: 10, right: 10, bottom: 22, left: 52 };

export default function LineChart({
  times,
  series,
  max,
  format,
}: {
  times: string[];
  series: Series[];
  max?: number;
  format: (v: number) => string;
}) {
  if (times.length < 2) {
    return <p className="muted">A recolher dados -- os gráficos aparecem ao fim de um minuto.</p>;
  }
  const top = max ?? Math.max(1, ...series.flatMap((s) => s.values)) * 1.1;
  const x = (i: number) => PAD.left + (i / (times.length - 1)) * (W - PAD.left - PAD.right);
  const y = (v: number) => PAD.top + (1 - Math.min(v, top) / top) * (H - PAD.top - PAD.bottom);
  const t0 = new Date(times[0]);
  const t1 = new Date(times[times.length - 1]);
  const hhmm = (d: Date) => d.toLocaleTimeString("pt-PT", { hour: "2-digit", minute: "2-digit" });
  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H}`} className="line-chart" role="img">
        {[0, 0.5, 1].map((f) => (
          <g key={f}>
            <line x1={PAD.left} x2={W - PAD.right} y1={y(top * f)} y2={y(top * f)} className="grid" />
            <text x={PAD.left - 6} y={y(top * f) + 4} textAnchor="end" className="axis">
              {format(top * f)}
            </text>
          </g>
        ))}
        <text x={PAD.left} y={H - 4} className="axis">
          {hhmm(t0)}
        </text>
        <text x={W - PAD.right} y={H - 4} textAnchor="end" className="axis">
          {hhmm(t1)}
        </text>
        {series.map((s, si) => (
          <polyline
            key={s.label}
            fill="none"
            strokeWidth={1.5}
            stroke={s.color ?? ["var(--accent)", "#16a34a", "#d97706"][si % 3]}
            points={s.values.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ")}
          />
        ))}
      </svg>
      {series.length > 1 && (
        <div className="row" style={{ gap: 12 }}>
          {series.map((s, si) => (
            <span key={s.label} className="muted">
              <span style={{ color: s.color ?? ["var(--accent)", "#16a34a", "#d97706"][si % 3] }}>■</span> {s.label}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
