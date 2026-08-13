import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { findGroup, removeReplica } from "@/lib/autoscalerAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para DELETE /v1/replicas/{id} de UM group -- para e remove
// imediatamente a réplica escolhida (ver
// services/autoscaler/internal/adminapi.handleRemoveReplica).
export async function DELETE(
  _req: NextRequest,
  { params }: { params: Promise<{ instanceId: string; replicaId: string }> }
) {
  const { instanceId, replicaId } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;

  const group = await findGroup(accessToken, instanceId);
  if (!group) {
    return NextResponse.json({ error: "unknown_instance", message: `instância desconhecida: ${instanceId}` }, { status: 404 });
  }

  const res = await removeReplica(group.adminUrl, accessToken, replicaId);
  if (res.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
