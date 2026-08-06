-- Alarga o conjunto de roles válidas para incluir "service" -- contas de
-- máquina (ex: o processo cmd/group do autoscaler), sem
-- password_credentials, que só recebem tokens via
-- POST /v1/admin/users/{id}/tokens (ver internal/store/role.go).
ALTER TABLE auth.users DROP CONSTRAINT users_role_check;
ALTER TABLE auth.users ADD CONSTRAINT users_role_check
    CHECK (role IN ('user', 'admin', 'infra-admin', 'super-admin', 'service'));
