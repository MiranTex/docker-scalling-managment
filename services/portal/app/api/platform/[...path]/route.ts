import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { LAUNCHER_SERVICE_URL } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para a API de plataforma do launcher (/v1/projects/...). Só
// caminhos começados por "projects" passam -- isto não é uma porta aberta
// para o resto da API do launcher. A autorização de verdade é feita lá.
async function proxy(req: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  const { path } = await params;
  if (path[0] !== "projects") {
    return NextResponse.json({ error: "not_found", message: "caminho desconhecido" }, { status: 404 });
  }
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;

  const target = `${LAUNCHER_SERVICE_URL}/v1/${path.map(encodeURIComponent).join("/")}${req.nextUrl.search}`;
  const hasBody = req.method !== "GET" && req.method !== "DELETE";
  const res = await fetch(target, {
    method: req.method,
    headers: {
      Authorization: `Bearer ${accessToken}`,
      ...(hasBody ? { "Content-Type": "application/json" } : {}),
    },
    body: hasBody ? await req.text() : undefined,
    cache: "no-store",
  });

  if (res.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const text = await res.text();
  return new NextResponse(text, {
    status: res.status,
    headers: { "Content-Type": res.headers.get("Content-Type") ?? "application/json" },
  });
}

export { proxy as GET, proxy as POST, proxy as PUT, proxy as PATCH, proxy as DELETE };
