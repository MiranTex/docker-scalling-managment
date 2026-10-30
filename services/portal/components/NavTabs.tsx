"use client";

import Link from "next/link";
import { useLayoutEffect, useRef, useState } from "react";

export type NavTabItem = { href: string; label: string; active: boolean; scroll?: boolean };

// Navegação por separadores com um indicador que desliza (só transform).
export default function NavTabs({ items }: { items: NavTabItem[] }) {
  const navRef = useRef<HTMLElement>(null);
  const [pos, setPos] = useState<{ x: number; w: number } | null>(null);
  const [ready, setReady] = useState(false);
  const activeKey = items.find((i) => i.active)?.href;

  useLayoutEffect(() => {
    const measure = () => {
      const el = navRef.current?.querySelector<HTMLElement>("a.active");
      setPos(el ? { x: el.offsetLeft, w: el.offsetWidth } : null);
    };
    measure();
    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, [activeKey]);

  // Só ativa a transição depois da primeira posição, para o indicador não "voar" do canto.
  useLayoutEffect(() => {
    if (pos && !ready) requestAnimationFrame(() => setReady(true));
  }, [pos, ready]);

  return (
    <nav className="subnav" ref={navRef}>
      {items.map((i) => (
        <Link key={i.href} href={i.href} scroll={i.scroll} className={i.active ? "active" : ""}>
          {i.label}
        </Link>
      ))}
      {pos && (
        <span
          className={`subnav-indicator${ready ? " animated" : ""}`}
          style={{ transform: `translateX(${pos.x}px) scaleX(${pos.w})` }}
        />
      )}
    </nav>
  );
}
