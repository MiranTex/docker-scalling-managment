"use client";

import { usePathname } from "next/navigation";
import NavTabs from "./NavTabs";

export type SubNavItem = { href: string; label: string; exact?: boolean };

export default function SubNav({ items }: { items: SubNavItem[] }) {
  const pathname = usePathname();
  return (
    <NavTabs
      items={items.map((i) => ({
        href: i.href,
        label: i.label,
        active: i.exact ? pathname === i.href : pathname.startsWith(i.href),
      }))}
    />
  );
}
