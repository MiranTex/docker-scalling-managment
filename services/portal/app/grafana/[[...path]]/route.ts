import { createHash } from "node:crypto";
import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { LAUNCHER_SERVICE_URL } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Serve o Grafana em /grafana com o login da plataforma. O Grafana confia
// no cabeçalho X-WEBAUTH-USER só vindo da rede grafana-proxy (onde este
// portal está), por isso a identidade tem de ser a certa: vem do launcher
// (POST /v1/grafana/session), que verifica o access token -- este portal
// não verifica assinaturas, e o cookie do email é editável pelo browser.

const GRAFANA_URL = process.env.GRAFANA_INTERNAL_URL ?? "http://monitoring-grafana-1:3000";

type Identity = { login: string; email: string; expires: number };

// Cache por token (hash): um dashboard faz dezenas de pedidos, não vale a
// pena perguntar ao launcher em cada um.
const identities = new Map<string, Identity>();
const IDENTITY_TTL_MS = 60_000;

async function identity(token: string): Promise<Identity | null> {
  const key = createHash("sha256").update(token).digest("hex");
  const cached = identities.get(key);
  if (cached && cached.expires > Date.now()) return cached;
  const res = await fetch(`${LAUNCHER_SERVICE_URL}/v1/grafana/session`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}` },
    cache: "no-store",
  });
  if (!res.ok) return null;
  const data = await res.json();
  const id: Identity = { login: data.login, email: data.email, expires: Date.now() + IDENTITY_TTL_MS };
  for (const [k, v] of identities) if (v.expires < Date.now()) identities.delete(k);
  identities.set(key, id);
  return id;
}

// Cabeçalhos que não passam: os de identidade (só este código os define)
// e os de ligação, que não fazem sentido de um salto para o outro.
const DROP_REQUEST = ["host", "connection", "content-length", "x-webauth-user", "x-webauth-email", "x-webauth-name"];
const DROP_RESPONSE = ["content-encoding", "content-length", "transfer-encoding", "connection"];

async function proxy(req: NextRequest, { params }: { params: Promise<{ path?: string[] }> }) {
  const cookieStore = await cookies();
  const token = cookieStore.get(ACCESS_COOKIE)?.value;
  if (!token) {
    return NextResponse.redirect(new URL(`/login?from=${encodeURIComponent(req.nextUrl.pathname)}`, req.url));
  }
  const who = await identity(token);
  if (!who) {
    return new NextResponse("Não foi possível abrir o Grafana: o launcher não confirmou a sessão.", { status: 502 });
  }

  const { path = [] } = await params;
  const target = `${GRAFANA_URL}/grafana/${path.map(encodeURIComponent).join("/")}${req.nextUrl.search}`;
  const headers = new Headers(req.headers);
  for (const h of DROP_REQUEST) headers.delete(h);
  headers.set("X-WEBAUTH-USER", who.login);
  headers.set("X-WEBAUTH-EMAIL", who.email);
  headers.set("X-WEBAUTH-NAME", who.email);

  const hasBody = !["GET", "HEAD"].includes(req.method);
  const res = await fetch(target, {
    method: req.method,
    headers,
    body: hasBody ? await req.arrayBuffer() : undefined,
    redirect: "manual",
    cache: "no-store",
  });

  const out = new Headers();
  res.headers.forEach((value, name) => {
    if (!DROP_RESPONSE.includes(name) && name !== "set-cookie") out.set(name, value);
  });
  for (const c of res.headers.getSetCookie()) out.append("set-cookie", c);
  // O Grafana escreve redirects absolutos com o seu root_url; tornam-se
  // relativos para funcionarem em qualquer porta/host do portal.
  const location = res.headers.get("location");
  if (location) out.set("location", location.replace(/^https?:\/\/[^/]+(?=\/grafana)/, ""));
  return new NextResponse(res.body, { status: res.status, headers: out });
}

export { proxy as GET, proxy as POST, proxy as PUT, proxy as PATCH, proxy as DELETE, proxy as HEAD, proxy as OPTIONS };
