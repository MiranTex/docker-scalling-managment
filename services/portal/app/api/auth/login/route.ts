import { NextResponse } from "next/server";
import { login } from "@/lib/authClient";
import { isHttps, setSessionCookies } from "@/lib/session";

export async function POST(req: Request) {
  const { email, password } = await req.json();
  // O IP real (o Traefik põe-no no X-Forwarded-For; o primeiro é o do
  // cliente) -- para o auth abrandar quem adivinha passwords.
  const clientIP = (req.headers.get("x-forwarded-for") ?? "").split(",")[0].trim() || req.headers.get("x-real-ip") || undefined;
  const authRes = await login(email, password, clientIP);
  const data = await authRes.json().catch(() => ({}));
  if (!authRes.ok) {
    return NextResponse.json(data, { status: authRes.status });
  }

  const res = NextResponse.json({ ok: true });
  setSessionCookies(res, data, isHttps(req), email);
  return res;
}
