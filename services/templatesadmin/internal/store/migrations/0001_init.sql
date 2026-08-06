-- Uma linha por modelo de serviço: os campos do launch template do
-- autoscaler (image/cmd/env/labels/binds/network/extra_hosts, ver
-- services/autoscaler/internal/executor.LaunchTemplate) mais os campos de
-- grupo que hoje só existem como env vars de um serviço "group-*" no
-- docker-compose (ver demo/docker-compose.yml, bloco comentado
-- "group-demo"). Nada aqui é segredo -- valores sensíveis do "env"
-- continuam a viver só no secretsadmin, referenciados por
-- "${secret:NOME}" (ver services/autoscaler/internal/secretsclient).
CREATE TABLE service_templates.templates (
    name                   TEXT PRIMARY KEY,
    image                  TEXT NOT NULL,
    cmd                    JSONB NOT NULL DEFAULT '[]',
    env                    JSONB NOT NULL DEFAULT '[]',
    labels                 JSONB NOT NULL DEFAULT '{}',
    binds                  JSONB NOT NULL DEFAULT '[]',
    network                TEXT NOT NULL DEFAULT '',
    extra_hosts            JSONB NOT NULL DEFAULT '[]',

    target_service         TEXT NOT NULL,
    backend_port           INTEGER NOT NULL DEFAULT 80,
    min_replicas           INTEGER NOT NULL DEFAULT 1,
    max_replicas           INTEGER NOT NULL DEFAULT 3,
    cpu_scale_up_percent   DOUBLE PRECISION NOT NULL DEFAULT 50,
    cpu_scale_down_percent DOUBLE PRECISION NOT NULL DEFAULT 20,
    host_proxy_port        INTEGER,
    host_metrics_port      INTEGER,

    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by             TEXT NOT NULL
);
