"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { hasInfraAdminAccess } from "@/lib/roles";

type Item = { href: string; label: string; prefix?: boolean };
type Group = { title: string; items: Item[]; visible: boolean };

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
      items: [
        { href: "/projects", label: "Projetos", prefix: true },
        { href: "/admin/launcher", label: "Instâncias" },
        { href: "/admin/autoscaler", label: "Autoscalers", prefix: true },
      ],
    },
    {
      title: "Dados",
      visible: infra,
      items: [
        { href: "/admin/database", label: "Base de dados" },
        { href: "/admin/secrets", label: "Segredos" },
      ],
    },
    {
      title: "Catálogo",
      visible: infra,
      items: [
        { href: "/admin/templates", label: "Modelos de serviço" },
        { href: "/admin/instance-types", label: "Tipos de instância" },
        { href: "/admin/networks", label: "Redes" },
      ],
    },
    {
      title: "Administração",
      visible: role === "super-admin",
      items: [{ href: "/admin/users", label: "Utilizadores" }],
    },
  ];

  const isActive = (i: Item) => (i.prefix ? pathname.startsWith(i.href) : pathname === i.href);

  return (
    <aside className="sidebar">
      <div className="sidebar-brand">base-stack</div>
      <nav>
        {groups
          .filter((g) => g.visible)
          .map((g) => (
            <div key={g.title} style={{ display: "contents" }}>
              <div className="nav-group">{g.title}</div>
              {g.items.map((i) => (
                <Link key={i.href} href={i.href} className={isActive(i) ? "active" : ""}>
                  {i.label}
                </Link>
              ))}
            </div>
          ))}
      </nav>
    </aside>
  );
}
