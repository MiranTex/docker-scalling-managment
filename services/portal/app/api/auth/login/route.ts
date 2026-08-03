import { NextResponse } from "next/server";
import { login } from "@/lib/authClient";
import { setSessionCookies } from "@/lib/session";

export async function POST(req: Request) {
  const { email, password } = await req.json();
  const authRes = await login(email, password);
  const data = await authRes.json().catch(() => ({}));
  if (!authRes.ok) {
    return NextResponse.json(data, { status: authRes.status });
  }

  const res = NextResponse.json({ ok: true });
  setSessionCookies(res, data, email);
  return res;
}
