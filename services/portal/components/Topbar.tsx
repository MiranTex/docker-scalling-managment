import LogoutButton from "./LogoutButton";

export default function Topbar({ email }: { email?: string }) {
  return (
    <header className="app-topbar">
      {email && <span className="muted">{email}</span>}
      <LogoutButton />
    </header>
  );
}
