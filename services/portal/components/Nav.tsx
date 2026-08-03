"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import LogoutButton from "./LogoutButton";

export default function Nav({ email }: { email?: string }) {
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
      </div>
      <div className="row">
        {email && <span className="muted">{email}</span>}
        <LogoutButton />
      </div>
    </nav>
  );
}
