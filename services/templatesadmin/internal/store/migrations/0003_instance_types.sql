-- Catálogo de "tipos de instância" (t1.micro, t1.small, ...), no espírito
-- dos instance types da AWS: um nome estável que traduz um par
-- (vCPU, memória) aplicado como limite REAL do Docker no momento de criar
-- o container (HostConfig.NanoCpus / HostConfig.Memory, ver
-- services/launcher/internal/dockerclient). Vive aqui, junto dos modelos
-- de serviço, porque é a mesma preocupação -- "como é que este container
-- deve ser criado" -- e porque assim reaproveita a auth, as migrations e
-- o ecrã de administração já existentes.
CREATE TABLE service_templates.instance_types (
    name           TEXT PRIMARY KEY,
    display_name   TEXT NOT NULL DEFAULT '',
    -- vCPUs fracionários: 0.25 vira NanoCpus = 250000000. NUMERIC e não
    -- DOUBLE PRECISION porque a conversão para nanos tem de ser exata.
    vcpu           NUMERIC(6,3) NOT NULL CHECK (vcpu > 0),
    -- O Docker recusa limites de memória abaixo de 6 MiB.
    memory_mb      BIGINT NOT NULL CHECK (memory_mb >= 6),
    -- NULL = MemorySwap igual a Memory, ou seja, swap desativado para o
    -- container. É o default deliberado: com swap, um container que
    -- estoura o limite degrada o host inteiro em vez de ser morto.
    memory_swap_mb BIGINT CHECK (memory_swap_mb IS NULL OR memory_swap_mb >= memory_mb),
    -- 0 = ilimitado (mesma convenção do Docker).
    pids_limit     BIGINT NOT NULL DEFAULT 0 CHECK (pids_limit >= 0),
    enabled        BOOLEAN NOT NULL DEFAULT true,

    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by     TEXT NOT NULL
);

INSERT INTO service_templates.instance_types (name, display_name, vcpu, memory_mb, updated_by) VALUES
    ('t1.micro',  'Micro (0.25 vCPU / 256 MB)',  0.25,  256, 'system'),
    ('t1.small',  'Small (0.5 vCPU / 512 MB)',   0.5,   512, 'system'),
    ('t1.medium', 'Medium (1 vCPU / 1 GB)',      1,    1024, 'system'),
    ('t1.large',  'Large (2 vCPU / 2 GB)',       2,    2048, 'system'),
    ('t1.xlarge', 'XLarge (4 vCPU / 4 GB)',      4,    4096, 'system');

-- Tipo aplicado quando quem lança não escolhe nenhum. Vazio = cai no
-- default global do launcher (LAUNCHER_DEFAULT_INSTANCE_TYPE). Sem FK
-- para instance_types de propósito: apagar um tipo não deve partir o
-- modelo, e o launcher já valida a existência no momento do lançamento.
ALTER TABLE service_templates.templates
    ADD COLUMN default_instance_type TEXT NOT NULL DEFAULT '';
