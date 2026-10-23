"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { hasInfraAdminAccess } from "@/lib/roles";

type Item = { href: string; label: string; prefix?: boolean };
type Group = { title: string; items: Item[]; visible: boolean; collapsible?: boolean };

// LEGACY_KEY guarda se o grupo "Legado" está aberto -- só conveniência
// por browser, por isso falhar a ler/escrever não muda nada.
const LEGACY_KEY = "sidebar.legacy.open";

export default function Sidebar({ role }: { role?: string }) {
  const pathname = usePathname();
  const infra = hasInfraAdminAccess(role);

  const groups: Group[] = [
    {
      title: "Conta",
      visible: true,
      items: [
        { href: "/dashboard", label: "Visão geral" },
        { href: "/tokens", label: "API tokens" },
      ],
    },
    {
      title: "Plataforma",
      visible: infra,
      items: [{ href: "/projects", label: "Projetos", prefix: true }],
    },
    {
      title: "Dados",
      visible: infra,
      items: [
        { href: "/admin/database", label: "Base de dados" },
        { href: "/admin/volumes", label: "Volumes" },
      ],
    },
    {
      title: "Administração",
      visible: role === "super-admin",
      items: [{ href: "/admin/users", label: "Utilizadores" }],
    },
    // O modelo de antes dos projetos: tudo global, sem isolamento. Fica
    // acessível enquanto não for removido, mas fora do caminho.
    {
      title: "Legado",
      visible: infra,
      collapsible: true,
      items: [
        { href: "/admin/launcher", label: "Instâncias" },
        { href: "/admin/autoscaler", label: "Autoscalers", prefix: true },
        { href: "/admin/templates", label: "Modelos de serviço", prefix: true },
        { href: "/admin/instance-types", label: "Tipos de instância" },
        { href: "/admin/secrets", label: "Segredos globais" },
        { href: "/admin/networks", label: "Redes" },
      ],
    },
  ];

  const isActive = (i: Item) => (i.prefix ? pathname.startsWith(i.href) : pathname === i.href);
  const legacyActive = groups.find((g) => g.collapsible)?.items.some(isActive) ?? false;

  const [legacyOpen, setLegacyOpen] = useState(false);
  useEffect(() => {
    try {
      setLegacyOpen(localStorage.getItem(LEGACY_KEY) === "1");
    } catch {}
  }, []);
  function toggleLegacy() {
    setLegacyOpen((open) => {
      try {
        localStorage.setItem(LEGACY_KEY, open ? "0" : "1");
      } catch {}
      return !open;
    });
  }

  return (
    <aside className="sidebar">
      <div className="sidebar-brand">base-stack</div>
      <nav>
        {groups
          .filter((g) => g.visible)
          .map((g) => {
            // Estar numa página do grupo abre-o, para o link ativo se ver.
            const open = !g.collapsible || legacyOpen || legacyActive;
            return (
              <div key={g.title} style={{ display: "contents" }}>
                {g.collapsible ? (
                  <button className="nav-group nav-toggle" onClick={toggleLegacy} aria-expanded={open}>
                    {open ? "▾" : "▸"} {g.title}
                  </button>
                ) : (
                  <div className="nav-group">{g.title}</div>
                )}
                {open &&
                  g.items.map((i) => (
                    <Link key={i.href} href={i.href} className={isActive(i) ? "active" : ""}>
                      {i.label}
                    </Link>
                  ))}
              </div>
            );
          })}
      </nav>
    </aside>
  );
}
