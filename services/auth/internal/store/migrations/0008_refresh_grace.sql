-- Rotação com margem: um refresh token rodado há instantes pode ainda
-- servir (pedidos em paralelo do mesmo browser chegam todos com ele) --
-- mas só se foi a rotação a revogá-lo (não um logout) e do mesmo IP.
ALTER TABLE auth.refresh_tokens
    ADD COLUMN rotated_at TIMESTAMPTZ,
    ADD COLUMN rotated_ip TEXT NOT NULL DEFAULT '';
