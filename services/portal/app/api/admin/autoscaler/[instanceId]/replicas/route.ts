import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { addReplica, findGroup } from "@/lib/autoscalerAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para POST /v1/replicas de UM group -- cria uma réplica extra
// imediatamente, fora do ciclo normal do scaler (ver
// services/autoscaler/internal/adminapi.handleAddReplica).
export async function POST(_req: NextRequest, { params }: { params: Promise<{ instanceId: string }> }) {
  const { instanceId } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;

  const group = await findGroup(accessToken, instanceId);
  if (!group) {
    return NextResponse.json({ error: "unknown_instance", message: `instância desconhecida: ${instanceId}` }, { status: 404 });
  }

  const res = await addReplica(group.adminUrl, accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
