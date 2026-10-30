-- Etiquetas dos nós (ex: gpu=nvidia, disco=ssd) e as que um serviço exige:
-- as réplicas só vão para nós que tenham todas. labels são as que um admin
-- põe; auto_labels as que a plataforma deteta (ex: gpu=nvidia quando o
-- Docker do nó tem o runtime da NVIDIA) e refresca a cada sinal do nó.
ALTER TABLE launcher.nodes
    ADD COLUMN labels      JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN auto_labels JSONB NOT NULL DEFAULT '{}';

-- Como a memória e o CPU: o serviço guarda o que se quer, a revisão o que
-- entrou no deploy (é a revisão que decide onde vão as réplicas).
ALTER TABLE launcher.services ADD COLUMN node_selector JSONB NOT NULL DEFAULT '{}';
ALTER TABLE launcher.revisions ADD COLUMN node_selector JSONB NOT NULL DEFAULT '{}';
