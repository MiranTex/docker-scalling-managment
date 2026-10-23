export default function Breadcrumbs({ items }: { items: { label: string; href?: string }[] }) {
  return (
    <p className="muted breadcrumbs">
      {items.map((i, idx) => (
        <span key={idx}>
          {idx > 0 && " / "}
          {i.href ? <a href={i.href}>{i.label}</a> : i.label}
        </span>
      ))}
    </p>
  );
}
