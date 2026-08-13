import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { listReplicas } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para listar réplicas vivas de QUALQUER autoscaler-group --
// ver services/launcher/internal/httpapi.handleListReplicas. É o que
// alimenta /admin/launcher com as réplicas que o autoscaler cria, além do
// que este launcher lançou diretamente.
export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await listReplicas(accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
