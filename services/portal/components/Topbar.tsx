import Link from "next/link";
import LogoutButton from "./LogoutButton";

export default function Topbar({ email }: { email?: string }) {
  return (
    <header className="app-topbar">
      {email && (
        <Link href="/account" className="muted" title="A minha conta (trocar a password)">
          {email}
        </Link>
      )}
      <LogoutButton />
    </header>
  );
}
