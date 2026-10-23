-- Modelos de serviço de um projeto. Cada gravação cria uma versão nova e
-- imutável; um serviço criado a partir de um modelo COPIA a configuração e
-- guarda de que versão veio, para mais tarde se ver o que mudou no modelo
-- e aplicar só isso. Arquivar um modelo não mexe em serviços.
CREATE TABLE launcher.project_templates (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES launcher.projects(id) ON DELETE CASCADE,
    slug        TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  TEXT NOT NULL,
    archived_at TIMESTAMPTZ,
    UNIQUE (project_id, slug)
);

CREATE TABLE launcher.template_versions (
    id          TEXT PRIMARY KEY,
    template_id TEXT NOT NULL REFERENCES launcher.project_templates(id) ON DELETE CASCADE,
    version     INTEGER NOT NULL,
    -- A configuração do serviço (ver platform.TemplateSpec), sem a imagem.
    spec        JSONB NOT NULL,
    -- Imagem do primeiro deploy de um serviço criado a partir desta versão.
    -- Fica fora de spec de propósito: atualizar um serviço para uma versão
    -- nova nunca troca a imagem que está a correr (essa muda por deploy).
    image       TEXT NOT NULL,
    notes       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  TEXT NOT NULL,
    UNIQUE (template_id, version)
);

ALTER TABLE launcher.services
    ADD COLUMN template_id      TEXT REFERENCES launcher.project_templates(id) ON DELETE SET NULL,
    ADD COLUMN template_version INTEGER;
