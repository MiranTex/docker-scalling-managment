// Placeholder com a altura aproximada do conteúdo final, para o layout não saltar.
export default function Loading({ rows = 4 }: { rows?: number }) {
  return (
    <div className="skeleton-list" role="status" aria-label="A carregar">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="skeleton" style={{ width: `${100 - (i % 3) * 12}%` }} />
      ))}
    </div>
  );
}
