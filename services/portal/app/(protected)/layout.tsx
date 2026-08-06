import { cookies } from "next/headers";
import Sidebar from "@/components/Sidebar";
import Topbar from "@/components/Topbar";
import { decodeJwtPayload } from "@/lib/jwt";
import { ACCESS_COOKIE, EMAIL_COOKIE } from "@/lib/session";

export default async function ProtectedLayout({ children }: { children: React.ReactNode }) {
  const cookieStore = await cookies();
  const email = cookieStore.get(EMAIL_COOKIE)?.value;
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;
  return (
    <div className="app-shell">
      <Sidebar role={role} />
      <div className="app-main">
        <Topbar email={email} />
        <div className="app-content">{children}</div>
      </div>
    </div>
  );
}
