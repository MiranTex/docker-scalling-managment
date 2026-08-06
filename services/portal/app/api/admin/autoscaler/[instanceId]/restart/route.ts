import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { getInstance, restart } from "@/lib/autoscalerAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para POST /v1/restart de UMA instância de autoscaler. O
// restart é síncrono do lado da instância (bloqueia até terminar, ver
// services/autoscaler/cmd/group/restart.go) -- este pedido também fica à
// espera, não há job/polling para esta ação.
export async function POST(_req: NextRequest, { params }: { params: Promise<{ instanceId: string }> }) {
  const { instanceId } = await params;
  const instance = getInstance(instanceId);
  if (!instance) {
    return NextResponse.json({ error: "unknown_instance", message: `instância desconhecida: ${instanceId}` }, { status: 404 });
  }

  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await restart(instance.url, accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
