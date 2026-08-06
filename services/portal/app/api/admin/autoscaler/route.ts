import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { getInstances, getStatus } from "@/lib/autoscalerAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Lista o estado de todas as instâncias de autoscaler configuradas para
// este ambiente (ver AUTOSCALER_ADMIN_INSTANCES). Cada instância é a API
// de administração de cada respetivo group -- é ela quem faz a
// autorização de verdade (requireRole em
// services/autoscaler/internal/adminapi), este handler é só um proxy
// fino, igual a app/api/admin/database/route.ts.
//
// Uma instância inalcançável não falha a lista inteira -- devolve-se o
// erro dessa instância isolado, para as outras continuarem visíveis.
export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const instances = getInstances();

  const results = await Promise.all(
    instances.map(async (instance) => {
      try {
        const res = await getStatus(instance.url, accessToken);
        const data = await res.json().catch(() => ({}));
        if (!res.ok) {
          return { id: instance.id, label: instance.label, url: instance.url, error: data.message ?? `erro ${res.status}` };
        }
        return { id: instance.id, label: instance.label, url: instance.url, status: data };
      } catch {
        return { id: instance.id, label: instance.label, url: instance.url, error: "instância inalcançável" };
      }
    })
  );

  return NextResponse.json({ instances: results });
}
