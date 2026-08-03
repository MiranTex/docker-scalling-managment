import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { createApiKey, listApiKeys } from "@/lib/authClient";
import { ACCESS_COOKIE } from "@/lib/session";

// O middleware já garante que access_token está presente e válido para
// tudo debaixo de /api/tokens (ver matcher em middleware.ts) -- aqui é só
// encaminhar para o auth service com o Bearer certo.

export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const authRes = await listApiKeys(accessToken);
  const data = await authRes.json().catch(() => []);
  return NextResponse.json(data, { status: authRes.status });
}

export async function POST(req: Request) {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const { scopes, ttl_seconds } = await req.json();
  const authRes = await createApiKey(accessToken, scopes ?? [], ttl_seconds);
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}
