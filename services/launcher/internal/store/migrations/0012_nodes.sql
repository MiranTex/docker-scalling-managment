-- Nós: as máquinas onde correm as apps e os addons dos projetos. Para já
-- só existe o "local" (a máquina do launcher), registado no arranque com o
-- que o Docker dela diz; os outros entram com "basestack node join".
-- online/offline não se guarda: deduz-se de last_seen_at.
CREATE TABLE launcher.nodes (
    name           TEXT PRIMARY KEY,
    address        TEXT NOT NULL DEFAULT '',
    os             TEXT NOT NULL,
    arch           TEXT NOT NULL,
    cpus           INTEGER NOT NULL,
    memory_mb      BIGINT NOT NULL,
    docker_version TEXT NOT NULL DEFAULT '',
    -- false: não recebe réplicas novas (manutenção); as que tem continuam.
    schedulable    BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
