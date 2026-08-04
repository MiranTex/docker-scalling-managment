import { cookies } from "next/headers";
import Nav from "@/components/Nav";
import { decodeJwtPayload } from "@/lib/jwt";
import { ACCESS_COOKIE, EMAIL_COOKIE } from "@/lib/session";

export default async function ProtectedLayout({ children }: { children: React.ReactNode }) {
  const cookieStore = await cookies();
  const email = cookieStore.get(EMAIL_COOKIE)?.value;
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;
  return (
    <main className="page">
      <Nav email={email} role={role} />
      {children}
    </main>
  );
}
