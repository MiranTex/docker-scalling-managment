import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { findGroup, getStatus, type InstanceSummary } from "@/lib/autoscalerAdminClient";
import { deleteGroup } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para o estado de UM group -- o que alimenta a página de
// detalhe (/admin/autoscaler/[id]), sem precisar buscar o estado de TODOS
// os groups como GET /api/admin/autoscaler faz para a listagem.
export async function GET(_req: NextRequest, { params }: { params: Promise<{ instanceId: string }> }) {
  const { instanceId } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;

  const group = await findGroup(accessToken, instanceId);
  if (!group) {
    return NextResponse.json({ error: "unknown_instance", message: `instância desconhecida: ${instanceId}` }, { status: 404 });
  }

  const base = { id: group.containerId, label: group.targetService || group.name, url: group.adminUrl, network: group.network };
  try {
    const res = await getStatus(group.adminUrl, accessToken);
    const data = await res.json().catch(() => ({}));
    const result: InstanceSummary = res.ok ? { ...base, status: data } : { ...base, error: data.message ?? `erro ${res.status}` };
    return NextResponse.json(result);
  } catch {
    const result: InstanceSummary = { ...base, error: "instância inalcançável" };
    return NextResponse.json(result);
  }
}

// Proxy fino para "Parar grupo" -- ao contrário de policy/restart/replicas
// (que falam com a API admin do PRÓPRIO group), isto fala com o launcher
// (DELETE /v1/groups/{containerId}), porque é ele quem sabe parar+remover
// o container do group -- inclusive um group que o launcher nunca chegou
// a "lançar" (ex: group-authd, estático no docker-compose.yml). instanceId
// aqui É o containerId (mesmo valor devolvido por GET /v1/groups).
export async function DELETE(_req: NextRequest, { params }: { params: Promise<{ instanceId: string }> }) {
  const { instanceId } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;

  const res = await deleteGroup(accessToken, instanceId);
  if (res.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
