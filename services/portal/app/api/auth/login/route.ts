import { NextResponse } from "next/server";
import { login } from "@/lib/authClient";
import { clientIP, isHttps, setSessionCookies } from "@/lib/session";

export async function POST(req: Request) {
  const { email, password } = await req.json();
  const authRes = await login(email, password, clientIP(req));
  const data = await authRes.json().catch(() => ({}));
  if (!authRes.ok) {
    return NextResponse.json(data, { status: authRes.status });
  }
  // Password certa mas falta o 2FA: a página pede o código (ou a ativação)
  // e continua em /api/auth/login/mfa*.
  if (data.mfa_required || data.mfa_setup_required) {
    return NextResponse.json(data);
  }

  const res = NextResponse.json({ ok: true });
  setSessionCookies(res, data, isHttps(req), email);
  return res;
}
