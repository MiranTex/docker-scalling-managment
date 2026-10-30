import { NextResponse } from "next/server";
import { loginMFA } from "@/lib/authClient";
import { clientIP, isHttps, setSessionCookies } from "@/lib/session";

// O segundo passo do login: o mfa_token que o login devolveu + o código.
export async function POST(req: Request) {
  const { mfaToken, code, email } = await req.json();
  const authRes = await loginMFA(mfaToken, code, clientIP(req));
  const data = await authRes.json().catch(() => ({}));
  if (!authRes.ok) {
    return NextResponse.json(data, { status: authRes.status });
  }
  const res = NextResponse.json({ ok: true });
  setSessionCookies(res, data, isHttps(req), email);
  return res;
}
