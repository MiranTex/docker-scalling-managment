import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { logout } from "@/lib/authClient";
import { REFRESH_COOKIE, clearSessionCookies } from "@/lib/session";

export async function POST() {
  const cookieStore = await cookies();
  const refreshToken = cookieStore.get(REFRESH_COOKIE)?.value;
  if (refreshToken) {
    await logout(refreshToken).catch(() => {});
  }
  const res = NextResponse.json({ ok: true });
  clearSessionCookies(res);
  return res;
}
