-- Contas que já entraram no Grafana pelo portal. A sincronização com o
-- Grafana (internal/grafanasync) precisa de saber quem é admin da
-- plataforma (para lhe dar a organização "Plataforma" e as de todos os
-- projetos) e o email de cada um -- e o auth não tem uma listagem de
-- contas para isso. Atualizado a cada sessão.
CREATE TABLE launcher.grafana_users (
    user_id        TEXT PRIMARY KEY,
    email          TEXT NOT NULL,
    platform_admin BOOLEAN NOT NULL,
    seen_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
