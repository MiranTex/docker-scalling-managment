-- Um addon pode ser parado de propósito (fica parado até alguém o
-- arrancar; o reconciliador respeita-o). Os dados ficam no volume.
ALTER TABLE launcher.addons ADD COLUMN desired_state TEXT NOT NULL DEFAULT 'running'
    CHECK (desired_state IN ('running', 'stopped'));
