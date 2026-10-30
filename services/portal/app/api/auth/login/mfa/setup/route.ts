import { NextResponse } from "next/server";
import { loginMFASetup } from "@/lib/authClient";

// Um admin sem 2FA ativa-o antes de entrar: o segredo e o link para o QR.
export async function POST(req: Request) {
  const { mfaToken } = await req.json();
  const authRes = await loginMFASetup(mfaToken);
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}
