import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { deleteSecret, upsertSecret } from "@/lib/secretsAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para PUT/DELETE de UM segredo. O valor em claro passa por
// este handler só de ida (no corpo do PUT) -- a resposta do secretsadmin
// nunca o ecoa de volta (ver services/secretsadmin/internal/httpapi.handleUpsert).

export async function PUT(req: NextRequest, { params }: { params: Promise<{ name: string }> }) {
  const { name } = await params;
  const body = await req.json().catch(() => ({}));
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await upsertSecret(accessToken, name, body.value ?? "");
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}

export async function DELETE(_req: NextRequest, { params }: { params: Promise<{ name: string }> }) {
  const { name } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await deleteSecret(accessToken, name);
  if (res.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
