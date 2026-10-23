-- Domínios próprios de um serviço web (ex: loja.pt). O hostname é único em
-- toda a plataforma: o mesmo domínio a apontar para dois serviços seria
-- ambíguo no routing. Só domínios "active" (DNS verificado a apontar para
-- este servidor) entram no Traefik -- e só esses pedem certificado, para
-- não gastar o rate limit da Let's Encrypt com DNS ainda por configurar.
CREATE TABLE launcher.domains (
    hostname    TEXT PRIMARY KEY,
    service_id  TEXT NOT NULL REFERENCES launcher.services(id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'failed')),
    error       TEXT NOT NULL DEFAULT '',
    checked_at  TIMESTAMPTZ,
    verified_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  TEXT NOT NULL
);
CREATE INDEX domains_service_idx ON launcher.domains (service_id);
