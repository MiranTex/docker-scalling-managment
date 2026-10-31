import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { LAUNCHER_SERVICE_URL } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Os ficheiros de um bucket, em streaming nos dois sentidos (envio e
// download) -- o proxy de /api/platform lê o corpo como texto e não serve.
// /api/files/<projeto>/<ambiente>/<addon>/<bucket>/<ficheiro...>
//
// Fica fora do middleware (ver o matcher em middleware.ts): o Next limita
// o corpo dos pedidos que lá passam, e um envio pode ter até 1 GB. A
// sessão renova-se nos pedidos normais do explorador, antes de cada envio.
async function proxy(req: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  const { path } = await params;
  if (path.length < 5) {
    return NextResponse.json({ error: "not_found", message: "caminho desconhecido" }, { status: 404 });
  }
  const accessToken = (await cookies()).get(ACCESS_COOKIE)?.value;
  if (!accessToken) {
    return NextResponse.json({ error: "unauthorized", message: "a sessão expirou -- recarrega a página" }, { status: 401 });
  }
  const [project, env, addon, bucket, ...key] = path.map(encodeURIComponent);
  const target = `${LAUNCHER_SERVICE_URL}/v1/projects/${project}/environments/${env}/addons/${addon}/buckets/${bucket}/files/${key.join("/")}`;

  const upload = req.method === "PUT";
  const headers: Record<string, string> = { Authorization: `Bearer ${accessToken}` };
  if (upload) {
    headers["Content-Type"] = req.headers.get("content-type") ?? "application/octet-stream";
    const length = req.headers.get("content-length");
    if (length) headers["Content-Length"] = length;
  }
  const res = await fetch(target, {
    method: req.method,
    headers,
    body: upload ? req.body : undefined,
    // @ts-expect-error -- o fetch do Node exige-o para enviar um stream.
    duplex: upload ? "half" : undefined,
    cache: "no-store",
  });

  const out = new Headers();
  for (const h of ["content-type", "content-length", "content-disposition"]) {
    const v = res.headers.get(h);
    if (v) out.set(h, v);
  }
  return new NextResponse(res.status === 204 ? null : res.body, { status: res.status, headers: out });
}

export { proxy as GET, proxy as PUT };
