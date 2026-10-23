-- Métricas da própria app: com metrics_port > 0, as réplicas recebem as
-- labels que o Prometheus usa para as descobrir (prometheus.scrape/port/
-- path). Vive também na revisão, como o resto da configuração de execução.
ALTER TABLE launcher.services
    ADD COLUMN metrics_port INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN metrics_path TEXT    NOT NULL DEFAULT '';
ALTER TABLE launcher.revisions
    ADD COLUMN metrics_port INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN metrics_path TEXT    NOT NULL DEFAULT '';
