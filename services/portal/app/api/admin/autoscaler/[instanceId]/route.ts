import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { deleteGroup } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

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
