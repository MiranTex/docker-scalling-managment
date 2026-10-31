import { toast } from "@/components/Toast";

// writeClipboard copia texto para o clipboard. A API moderna só existe em
// contextos seguros (HTTPS ou localhost) -- o portal na rede local
// (http://portal.<ip>.nip.io) não a tem, e aí copia-se à moda antiga, por
// uma caixa de texto escondida. Lança se o browser recusar as duas.
export async function writeClipboard(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // Recusado (ex: sem foco na página) -- tenta a outra via.
    }
  }
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  try {
    if (!document.execCommand("copy")) throw new Error("o browser recusou copiar");
  } finally {
    document.body.removeChild(area);
  }
}

// copyText copia e avisa. Lança se o browser recusar o acesso ao
// clipboard; o chamador decide o que fazer.
export async function copyText(text: string, message = "Copiado"): Promise<void> {
  await writeClipboard(text);
  toast(message);
}
