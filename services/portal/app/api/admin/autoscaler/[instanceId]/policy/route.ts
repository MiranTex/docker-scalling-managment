import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { getInstance, getPolicy, updatePolicy } from "@/lib/autoscalerAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para GET/PUT /v1/policy de UMA instância de autoscaler
// (identificada por instanceId na lista estática AUTOSCALER_ADMIN_INSTANCES).
// A validação e a aplicação a quente da policy acontecem na própria
// instância (ver services/autoscaler/internal/adminapi) -- este handler só
// resolve o URL da instância e encaminha o pedido.

export async function GET(_req: NextRequest, { params }: { params: Promise<{ instanceId: string }> }) {
  const { instanceId } = await params;
  const instance = getInstance(instanceId);
  if (!instance) {
    return NextResponse.json({ error: "unknown_instance", message: `instância desconhecida: ${instanceId}` }, { status: 404 });
  }

  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await getPolicy(instance.url, accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}

export async function PUT(req: NextRequest, { params }: { params: Promise<{ instanceId: string }> }) {
  const { instanceId } = await params;
  const instance = getInstance(instanceId);
  if (!instance) {
    return NextResponse.json({ error: "unknown_instance", message: `instância desconhecida: ${instanceId}` }, { status: 404 });
  }

  const patch = await req.json().catch(() => ({}));
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await updatePolicy(instance.url, accessToken, patch);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
