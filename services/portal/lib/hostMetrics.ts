import type { HostSample } from "@/lib/platformClient";

// Os limiares das barras e dos avisos: amarelo a partir de 80%, vermelho
// a partir de 90%.
export const WARN = 80;
export const BAD = 90;

export function level(percent: number): "ok" | "warn" | "bad" {
  return percent >= BAD ? "bad" : percent >= WARN ? "warn" : "ok";
}

export const memoryUsed = (s: HostSample) => s.memoryTotalBytes - s.memoryAvailableBytes;
export const memoryPercent = (s: HostSample) => (s.memoryTotalBytes > 0 ? (100 * memoryUsed(s)) / s.memoryTotalBytes : 0);
export const diskUsed = (s: HostSample) => s.diskTotalBytes - s.diskAvailableBytes;
export const diskPercent = (s: HostSample) => (s.diskTotalBytes > 0 ? (100 * diskUsed(s)) / s.diskTotalBytes : 0);

export function bytes(n: number): string {
  if (n >= 2 ** 40) return `${(n / 2 ** 40).toFixed(1)} TB`;
  if (n >= 2 ** 30) return `${(n / 2 ** 30).toFixed(1)} GB`;
  if (n >= 2 ** 20) return `${Math.round(n / 2 ** 20)} MB`;
  return `${Math.round(n / 1024)} KB`;
}

// warnings: o que merece aviso numa leitura (disco a encher, memória a
// acabar, CPU no máximo).
export function warnings(s: HostSample): string[] {
  const out: string[] = [];
  if (diskPercent(s) >= 85) out.push(`disco a ${Math.round(diskPercent(s))}% (${bytes(s.diskAvailableBytes)} livres)`);
  if (s.memoryTotalBytes > 0 && s.memoryAvailableBytes / s.memoryTotalBytes < 0.1) out.push(`memória quase esgotada (${bytes(s.memoryAvailableBytes)} livres)`);
  if (s.cpuPercent >= BAD) out.push(`CPU a ${Math.round(s.cpuPercent)}%`);
  return out;
}
