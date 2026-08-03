import { cookies } from "next/headers";
import { decodeJwtPayload } from "@/lib/jwt";
import { ACCESS_COOKIE, EMAIL_COOKIE } from "@/lib/session";

export default async function DashboardPage() {
  const cookieStore = await cookies();
  const email = cookieStore.get(EMAIL_COOKIE)?.value;
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const claims = accessToken ? decodeJwtPayload(accessToken) : null;
  const exp = claims?.exp ? new Date((claims.exp as number) * 1000) : null;

  return (
    <div className="card wide">
      <h1>Sessão</h1>
      <p className="notice">Sessão ativa{email ? ` como ${email}` : ""}.</p>
      <table>
        <tbody>
          <tr>
            <th>User ID</th>
            <td>{(claims?.sub as string) ?? "—"}</td>
          </tr>
          <tr>
            <th>Emissor (iss)</th>
            <td>{(claims?.iss as string) ?? "—"}</td>
          </tr>
          <tr>
            <th>Access token expira</th>
            <td>{exp ? exp.toLocaleString("pt-PT") : "—"}</td>
          </tr>
        </tbody>
      </table>
      <p className="muted" style={{ marginTop: "1rem" }}>
        O access token dura 15 minutos por default e é renovado
        automaticamente enquanto a sessão (refresh token) continuar válida —
        não precisas de entrar de novo a cada expiração.
      </p>
    </div>
  );
}
