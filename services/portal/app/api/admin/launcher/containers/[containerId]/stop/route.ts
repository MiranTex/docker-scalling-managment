import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { stopContainer } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para parar (sem remover) UM container desta plataforma pelo
// seu containerId -- ver
// services/launcher/internal/httpapi.handleStopContainer.
export async function POST(_req: NextRequest, { params }: { params: Promise<{ containerId: string }> }) {
  const { containerId } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await stopContainer(accessToken, containerId);
  if (res.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
