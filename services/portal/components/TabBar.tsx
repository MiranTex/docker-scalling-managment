"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

export type TabDef = { key: string; label: string };

// O separador ativo vive na URL (?tab=...), por isso sobrevive a um refresh
// e pode ser partilhado num link.
export function useActiveTab(tabs: readonly TabDef[]): string {
  const params = useSearchParams();
  const wanted = params.get("tab");
  return tabs.some((t) => t.key === wanted) ? (wanted as string) : tabs[0].key;
}

export default function TabBar({ tabs, active }: { tabs: readonly TabDef[]; active: string }) {
  return (
    <nav className="subnav">
      {tabs.map((t) => (
        <Link
          key={t.key}
          href={t.key === tabs[0].key ? "?" : `?tab=${t.key}`}
          scroll={false}
          className={t.key === active ? "active" : ""}
        >
          {t.label}
        </Link>
      ))}
    </nav>
  );
}
