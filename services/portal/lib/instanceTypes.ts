import type { InstanceType } from "./templatesAdminClient";

// Rótulo curto de um tamanho, usado em <select> e tabelas -- "t1.small ·
// 0.5 vCPU / 512 MB". Deliberadamente não usa displayName: quem escolhe
// precisa de ver os números para comparar dois tamanhos lado a lado.
export function formatInstanceType(it: InstanceType): string {
  return `${it.name} · ${it.vcpu} vCPU / ${formatMemory(it.memoryMb)}`;
}

export function formatMemory(mb: number): string {
  if (mb >= 1024) return `${(mb / 1024).toFixed(mb % 1024 === 0 ? 0 : 1)} GB`;
  return `${mb} MB`;
}
