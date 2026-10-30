"use client";

import { useSearchParams } from "next/navigation";
import NavTabs from "./NavTabs";

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
    <NavTabs
      items={tabs.map((t) => ({
        href: t.key === tabs[0].key ? "?" : `?tab=${t.key}`,
        label: t.label,
        active: t.key === active,
        scroll: false,
      }))}
    />
  );
}
