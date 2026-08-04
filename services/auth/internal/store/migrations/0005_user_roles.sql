-- role: nível de autorização grosso de uma conta -- ver
-- internal/store/role.go para as constantes e o porquê de não ter
-- hierarquia nenhuma codificada aqui (cada endpoint admin decide
-- explicitamente que roles aceita). Default 'user': toda conta nasce sem
-- nenhum privilégio administrativo, precisa de ser promovida.
ALTER TABLE auth.users ADD COLUMN role TEXT NOT NULL DEFAULT 'user';
ALTER TABLE auth.users ADD CONSTRAINT users_role_check
    CHECK (role IN ('user', 'admin', 'infra-admin', 'super-admin'));
