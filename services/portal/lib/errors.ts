// authError: a mensagem de um erro do auth ({error: {code, message}}) ou da
// API da plataforma ({error, message}), ou o texto por defeito.
export function authError(data: unknown, fallback: string): string {
  const d = data as { message?: unknown; error?: { message?: unknown } } | null;
  if (d && typeof d.error === "object" && d.error && typeof d.error.message === "string") return d.error.message;
  if (d && typeof d.message === "string") return d.message;
  return fallback;
}
