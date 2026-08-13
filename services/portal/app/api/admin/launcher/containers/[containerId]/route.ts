import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { removeContainer } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para eliminar (parar + remover) UM container desta
// plataforma pelo seu containerId -- funciona para uma instância "solo"
// ou uma réplica, nunca para um group inteiro (ver
// services/launcher/internal/httpapi.handleRemoveContainer).
export async function DELETE(_req: NextRequest, { params }: { params: Promise<{ containerId: string }> }) {
  const { containerId } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await removeContainer(accessToken, containerId);
  if (res.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
