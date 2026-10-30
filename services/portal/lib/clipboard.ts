import { toast } from "@/components/Toast";

// Lança se o browser recusar o acesso ao clipboard; o chamador decide o que fazer.
export async function copyText(text: string, message = "Copiado"): Promise<void> {
  await navigator.clipboard.writeText(text);
  toast(message);
}
