"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { hasInfraAdminAccess } from "@/lib/roles";

export default function Sidebar({ role }: { role?: string }) {
  const pathname = usePathname();
  const linkClass = (href: string) => (pathname === href ? "active" : "");

  return (
    <aside className="sidebar">
      <div className="sidebar-brand">base-stack</div>
      <nav>
        <Link href="/dashboard" className={linkClass("/dashboard")}>
          Sessão
        </Link>
        <Link href="/tokens" className={linkClass("/tokens")}>
          API tokens
        </Link>
        {role === "super-admin" && (
          <Link href="/admin/users" className={linkClass("/admin/users")}>
            Utilizadores
          </Link>
        )}
        {hasInfraAdminAccess(role) && (
          <Link href="/admin/database" className={linkClass("/admin/database")}>
            Base de dados
          </Link>
        )}
        {hasInfraAdminAccess(role) && (
          <Link href="/admin/autoscaler" className={linkClass("/admin/autoscaler")}>
            Autoscalers
          </Link>
        )}
        {hasInfraAdminAccess(role) && (
          <Link href="/admin/secrets" className={linkClass("/admin/secrets")}>
            Segredos
          </Link>
        )}
        {hasInfraAdminAccess(role) && (
          <Link href="/admin/templates" className={linkClass("/admin/templates")}>
            Modelos de serviço
          </Link>
        )}
        {hasInfraAdminAccess(role) && (
          <Link href="/admin/launcher" className={linkClass("/admin/launcher")}>
            Instâncias
          </Link>
        )}
        {hasInfraAdminAccess(role) && (
          <Link href="/admin/networks" className={linkClass("/admin/networks")}>
            Redes
          </Link>
        )}
      </nav>
    </aside>
  );
}
