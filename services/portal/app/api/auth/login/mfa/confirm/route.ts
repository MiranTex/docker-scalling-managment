import { NextResponse } from "next/server";
import { loginMFAConfirm } from "@/lib/authClient";
import { isHttps, setSessionCookies } from "@/lib/session";

// Confirma a ativação com o primeiro código e entra; os códigos de
// recuperação vão para a página mostrar uma vez.
export async function POST(req: Request) {
  const { mfaToken, code, email } = await req.json();
  const authRes = await loginMFAConfirm(mfaToken, code);
  const data = await authRes.json().catch(() => ({}));
  if (!authRes.ok) {
    return NextResponse.json(data, { status: authRes.status });
  }
  const res = NextResponse.json({ ok: true, recoveryCodes: data.recovery_codes ?? [] });
  setSessionCookies(res, data, isHttps(req), email);
  return res;
}
