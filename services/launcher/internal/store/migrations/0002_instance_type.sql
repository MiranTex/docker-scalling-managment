-- Cópia dos recursos efetivamente aplicados a esta instância, além do
-- nome do tipo. Desnormalizado de propósito: o catálogo em
-- service_templates.instance_types é editável, e sem este snapshot editar
-- um tipo reescreveria retroativamente o histórico e a contabilidade de
-- capacidade de containers que já estão a correr com os valores antigos.
-- Zero = instância criada antes desta feature, sem limites aplicados.
ALTER TABLE launcher.instances
    ADD COLUMN instance_type TEXT   NOT NULL DEFAULT '',
    ADD COLUMN vcpu          NUMERIC(6,3) NOT NULL DEFAULT 0,
    ADD COLUMN memory_mb     BIGINT NOT NULL DEFAULT 0;
