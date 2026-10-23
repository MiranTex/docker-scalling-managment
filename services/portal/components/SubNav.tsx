"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

export type SubNavItem = { href: string; label: string; exact?: boolean };

export default function SubNav({ items }: { items: SubNavItem[] }) {
  const pathname = usePathname();
  return (
    <nav className="subnav">
      {items.map((i) => {
        const active = i.exact ? pathname === i.href : pathname.startsWith(i.href);
        return (
          <Link key={i.href} href={i.href} className={active ? "active" : ""}>
            {i.label}
          </Link>
        );
      })}
    </nav>
  );
}
