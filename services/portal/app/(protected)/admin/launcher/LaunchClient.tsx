"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import type { InstanceType, ServiceTemplate } from "@/lib/templatesAdminClient";
import { formatInstanceType } from "@/lib/instanceTypes";
import NetworkPicker from "./NetworkPicker";
import ExtraNetworksPicker from "./ExtraNetworksPicker";

export default function LaunchClient() {
  const router = useRouter();
  const [templates, setTemplates] = useState<ServiceTemplate[] | null>(null);
  const [templateName, setTemplateName] = useState("");
  const [instanceTypes, setInstanceTypes] = useState<InstanceType[]>([]);
  const [instanceType, setInstanceType] = useState("");
  const [network, setNetwork] = useState("");
  const [extraNetworks, setExtraNetworks] = useState<string[]>([]);
  const [exposeAs, setExposeAs] = useState("");
  const [exposePort, setExposePort] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    (async () => {
      try {
        const res = await fetch("/api/admin/instance-types");
        if (res.ok) setInstanceTypes(((await res.json()) ?? []).filter((it: InstanceType) => it.enabled));
      } catch {
        // Sem catálogo, o <select> fica só com "herdar do modelo" -- que é o
        // comportamento por omissão do launcher de qualquer forma.
      }
    })();
    (async () => {
      try {
        const res = await fetch("/api/admin/templates");
        if (!res.ok) return;
        const data: ServiceTemplate[] = await res.json();
        setTemplates(data);
        setTemplateName((current) => current || (data.length > 0 ? data[0].name : ""));
      } catch {
        // A lista de modelos é só para preencher o <select> -- se falhar,
        // quem quiser lançar uma instância ainda pode escrever o nome à mão.
      }
    })();
  }, []);

  async function handleCreate() {
    setFormError(null);
    if (!templateName.trim()) {
      setFormError("nome do modelo é obrigatório");
      return;
    }
    if (exposeAs.trim() && !exposePort.trim()) {
      setFormError("porta interna é obrigatória para expor a instância");
      return;
    }
    setSaving(true);
    try {
      const res = await fetch("/api/admin/launcher", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          templateName,
          kind: "solo",
          ...(instanceType ? { instanceType } : {}),
          ...(network.trim() ? { network: network.trim() } : {}),
          ...(extraNetworks.length ? { extraNetworks } : {}),
          ...(exposeAs.trim() ? { exposeAs: exposeAs.trim(), exposePort: Number(exposePort) } : {}),
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setFormError(data.message ?? "erro ao lançar instância");
        return;
      }
      router.push("/admin/launcher");
    } catch {
      setFormError("erro ao lançar instância");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="card wide">
      <h2>Lançar nova instância</h2>
      {formError && <div className="error">{formError}</div>}

      <div className="section-grid">
        <div>
          <h3>Modelo e tamanho</h3>
          <label htmlFor="instance-template">Modelo</label>
          {templates && templates.length > 0 ? (
            <select id="instance-template" value={templateName} onChange={(e) => setTemplateName(e.target.value)}>
              {templates.map((t) => (
                <option key={t.name} value={t.name}>
                  {t.name}
                </option>
              ))}
            </select>
          ) : (
            <input
              id="instance-template"
              type="text"
              value={templateName}
              onChange={(e) => setTemplateName(e.target.value)}
              placeholder="nome do modelo (services/templatesadmin)"
            />
          )}

          <label htmlFor="instance-type">Tipo de instância</label>
          <select id="instance-type" value={instanceType} onChange={(e) => setInstanceType(e.target.value)}>
            <option value="">(herdar do modelo)</option>
            {instanceTypes.map((it) => (
              <option key={it.name} value={it.name}>
                {formatInstanceType(it)}
              </option>
            ))}
          </select>
          <p className="muted">
            Limites de CPU e memória aplicados ao container. Gerir o catálogo em{" "}
            <a href="/admin/instance-types">/admin/instance-types</a>.
          </p>
        </div>

        <div>
          <h3>Rede</h3>
          <label htmlFor="instance-network">Rede Docker (opcional, substitui a do modelo)</label>
          <NetworkPicker id="instance-network" value={network} onChange={setNetwork} />
          <p className="muted">
            Deixa em branco para usar a rede definida no modelo. Se o container ficar "inatingível" depois de lançado, é
            normalmente porque nem o modelo nem este campo têm a rede certa (o Docker usa "bridge" por defeito, isolada do
            launcher/portal).
          </p>

          <label htmlFor="instance-extra-networks">Redes adicionais (opcional)</label>
          <ExtraNetworksPicker
            id="instance-extra-networks"
            exclude={network || undefined}
            value={extraNetworks}
            onChange={setExtraNetworks}
          />
          <p className="muted">
            Liga a instância a mais redes além da acima, ao mesmo tempo -- ex: a própria rede da app E{" "}
            <code>observability-net</code> (ver <a href="/admin/networks">/admin/networks</a>). Ctrl/Cmd+clique para
            escolher mais do que uma.
          </p>
        </div>

        <div>
          <h3>Exposição pública</h3>
          <label htmlFor="instance-expose-as">Expor publicamente como (opcional)</label>
          <input
            id="instance-expose-as"
            type="text"
            value={exposeAs}
            onChange={(e) => setExposeAs(e.target.value)}
            placeholder="minha-app"
          />
          <label htmlFor="instance-expose-port">Porta interna (obrigatória se expor publicamente)</label>
          <input
            id="instance-expose-port"
            type="number"
            value={exposePort}
            onChange={(e) => setExposePort(e.target.value)}
            placeholder="80"
          />
          <p className="muted">
            Em branco, a instância fica só na rede interna. Preenchido, fica acessível em{" "}
            <code>http://{exposeAs.trim() || "..."}.PUBLIC_BASE_DOMAIN</code> através do gateway único (Traefik) -- nunca
            publica porta nenhuma no host. A porta interna é a que a APLICAÇÃO escuta dentro do container. Qualquer
            subdomínio na frente (ex: <code>tenant.{exposeAs.trim() || "..."}.PUBLIC_BASE_DOMAIN</code>) também chega a
            este mesmo container -- útil para uma app multi-tenant.
          </p>
        </div>
      </div>

      <div className="row">
        <button className="primary" disabled={saving} onClick={handleCreate}>
          {saving ? "A lançar..." : "Lançar"}
        </button>
        <button className="secondary" onClick={() => router.push("/admin/launcher")}>
          Cancelar
        </button>
      </div>
    </div>
  );
}
