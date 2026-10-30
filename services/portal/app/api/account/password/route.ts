import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { changeOwnPassword } from "@/lib/authClient";
import { ACCESS_COOKIE, clearSessionCookies } from "@/lib/session";

// Trocar a própria password. O auth revoga as sessões da conta, por isso
// esta também termina: a pessoa volta a entrar com a password nova.
export async function POST(req: Request) {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const { currentPassword, newPassword } = await req.json();
  const authRes = await changeOwnPassword(accessToken, currentPassword, newPassword);
  if (authRes.status !== 204) {
    const data = await authRes.json().catch(() => ({}));
    return NextResponse.json(data, { status: authRes.status });
  }
  const res = NextResponse.json({ ok: true });
  clearSessionCookies(res);
  return res;
}
