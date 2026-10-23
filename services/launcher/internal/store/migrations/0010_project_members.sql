-- Membros de um projeto: quem, além dos admins da plataforma (infra-admin e
-- super-admin, que veem tudo), pode ver ou mexer neste projeto, e com que
-- papel. user_id é o id da conta no auth; o email é guardado só para
-- mostrar, sem ter de perguntar ao auth a cada listagem.
CREATE TABLE launcher.project_members (
    project_id TEXT NOT NULL REFERENCES launcher.projects(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL,
    email      TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('viewer', 'developer', 'admin')),
    added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    added_by   TEXT NOT NULL,
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX project_members_user_idx ON launcher.project_members (user_id);
