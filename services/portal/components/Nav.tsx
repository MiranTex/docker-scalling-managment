"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import LogoutButton from "./LogoutButton";

export default function Nav({ email, role }: { email?: string; role?: string }) {
  const pathname = usePathname();
  return (
    <nav className="topbar">
      <div>
        <Link href="/dashboard" className={pathname === "/dashboard" ? "active" : ""}>
          Sessão
        </Link>
        <Link href="/tokens" className={pathname === "/tokens" ? "active" : ""}>
          API tokens
        </Link>
        {role === "super-admin" && (
          <Link href="/admin/users" className={pathname === "/admin/users" ? "active" : ""}>
            Utilizadores
          </Link>
        )}
        {role === "infra-admin" && (
          <Link href="/admin/database" className={pathname === "/admin/database" ? "active" : ""}>
            Base de dados
          </Link>
        )}
        {role === "infra-admin" && (
          <Link href="/admin/autoscaler" className={pathname === "/admin/autoscaler" ? "active" : ""}>
            Autoscalers
          </Link>
        )}
      </div>
      <div className="row">
        {email && <span className="muted">{email}</span>}
        <LogoutButton />
      </div>
    </nav>
  );
}
