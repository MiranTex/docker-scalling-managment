import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { ACCESS_COOKIE, REFRESH_COOKIE } from "@/lib/session";

export default async function Home() {
  const cookieStore = await cookies();
  const hasSession = Boolean(cookieStore.get(ACCESS_COOKIE) ?? cookieStore.get(REFRESH_COOKIE));
  redirect(hasSession ? "/dashboard" : "/login");
}
