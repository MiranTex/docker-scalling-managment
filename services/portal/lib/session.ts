import { NextResponse } from "next/server";
import type { TokenPair } from "./authClient";

// Cookies de sessão são todos httpOnly (exceto o e-mail, só para exibição)
// -- os tokens nunca ficam acessíveis a JS no browser, então um XSS na
// página não consegue roubá-los. sameSite=lax é suficiente aqui: não há
// nenhuma ação sensível disparada por um simples GET vindo de outro site.
const COOKIE_OPTS = {
  httpOnly: true,
  sameSite: "lax" as const,
  path: "/",
};

// isHttps diz se o browser chegou por HTTPS -- diretamente ou através do
// Traefik (X-Forwarded-Proto). Só aí os cookies podem ser "Secure": por
// HTTP o browser deita-os fora (exceto em localhost), e o login numa rede
// local sem certificados (portal.<ip>.nip.io) nunca ficava com sessão.
export function isHttps(req: Request): boolean {
  const forwarded = req.headers.get("x-forwarded-proto")?.split(",")[0].trim();
  return forwarded ? forwarded === "https" : new URL(req.url).protocol === "https:";
}

// clientIP: o IP real de quem pede (o Traefik põe-no no X-Forwarded-For; o
// primeiro é o do cliente) -- para o auth abrandar quem adivinha passwords
// e códigos.
export function clientIP(req: Request): string | undefined {
  return (req.headers.get("x-forwarded-for") ?? "").split(",")[0].trim() || req.headers.get("x-real-ip") || undefined;
}

export const ACCESS_COOKIE = "access_token";
export const REFRESH_COOKIE = "refresh_token";
export const EMAIL_COOKIE = "user_email";
// REMEMBER_COOKIE: "manter sessão iniciada" -- sem ele, os cookies da
// sessão morrem quando o browser fecha.
export const REMEMBER_COOKIE = "remember";
// A validade do refresh token no auth (AUTH_REFRESH_TOKEN_TTL, 14 dias).
const REMEMBER_SECONDS = 14 * 24 * 3600;

export function setSessionCookies(res: NextResponse, pair: TokenPair, secure: boolean, email?: string, remember = false) {
  const opts = { ...COOKIE_OPTS, secure };
  const persist = remember ? { maxAge: REMEMBER_SECONDS } : {};
  res.cookies.set(ACCESS_COOKIE, pair.access_token, { ...opts, maxAge: pair.expires_in });
  res.cookies.set(REFRESH_COOKIE, pair.refresh_token, { ...opts, ...persist });
  if (remember) {
    res.cookies.set(REMEMBER_COOKIE, "1", { ...opts, ...persist });
  }
  if (email) {
    res.cookies.set(EMAIL_COOKIE, email, { ...opts, ...persist, httpOnly: false });
  }
}

export function clearSessionCookies(res: NextResponse) {
  res.cookies.delete(ACCESS_COOKIE);
  res.cookies.delete(REFRESH_COOKIE);
  res.cookies.delete(EMAIL_COOKIE);
  res.cookies.delete(REMEMBER_COOKIE);
}
