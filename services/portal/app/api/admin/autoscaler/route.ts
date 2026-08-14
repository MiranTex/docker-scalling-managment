import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { getStatus, type InstanceSummary } from "@/lib/autoscalerAdminClient";
import { listGroups, type DiscoveredGroup } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Lista o estado de todos os autoscaler-groups vivos, descobertos
// dinamicamente contra o launcher (GET /v1/groups) -- não há mais lista
// estática (AUTOSCALER_ADMIN_INSTANCES foi retirado). Cada group é a sua
// própria API de administração -- é ela quem faz a autorização de
// verdade (requireRole em services/autoscaler/internal/adminapi), este
// handler é só um proxy fino, igual a app/api/admin/database/route.ts.
//
// Um group inalcançável não falha a lista inteira -- devolve-se o erro
// desse group isolado, para os outros continuarem visíveis.
export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;

  const groupsRes = await listGroups(accessToken);
  if (!groupsRes.ok) {
    const data = await groupsRes.json().catch(() => ({}));
    return NextResponse.json(data, { status: groupsRes.status });
  }
  const groups: DiscoveredGroup[] = await groupsRes.json();

  const results: InstanceSummary[] = await Promise.all(
    groups.map(async (group) => {
      const base = {
        id: group.containerId,
        label: group.targetService || group.name,
        url: group.adminUrl,
        network: group.network,
        exposedHost: group.exposedHost,
      };
      try {
        const res = await getStatus(group.adminUrl, accessToken);
        const data = await res.json().catch(() => ({}));
        if (!res.ok) {
          return { ...base, error: data.message ?? `erro ${res.status}` };
        }
        return { ...base, status: data };
      } catch {
        return { ...base, error: "instância inalcançável" };
      }
    })
  );

  return NextResponse.json({ instances: results });
}
