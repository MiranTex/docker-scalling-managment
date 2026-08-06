import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { deleteTemplate, getTemplate, upsertTemplate } from "@/lib/templatesAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para GET/PUT/DELETE de UM modelo -- mesmo padrão de
// app/api/admin/secrets/[name]/route.ts.

export async function GET(_req: NextRequest, { params }: { params: Promise<{ name: string }> }) {
  const { name } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await getTemplate(accessToken, name);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}

export async function PUT(req: NextRequest, { params }: { params: Promise<{ name: string }> }) {
  const { name } = await params;
  const body = await req.json().catch(() => ({}));
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await upsertTemplate(accessToken, name, body);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}

export async function DELETE(_req: NextRequest, { params }: { params: Promise<{ name: string }> }) {
  const { name } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await deleteTemplate(accessToken, name);
  if (res.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
