import { cookies } from "next/headers";
import Nav from "@/components/Nav";
import { EMAIL_COOKIE } from "@/lib/session";

export default async function ProtectedLayout({ children }: { children: React.ReactNode }) {
  const cookieStore = await cookies();
  const email = cookieStore.get(EMAIL_COOKIE)?.value;
  return (
    <main className="page">
      <Nav email={email} />
      {children}
    </main>
  );
}
