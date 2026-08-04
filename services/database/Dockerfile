# Postgres oficial + pgBackRest (backups/PITR) + cron (agendamento) na
# mesma imagem -- ver README "Limitações conhecidas" para o porquê de não
# separar isto num repository host próprio (é o mais correto em
# produção, mas exige protocolo remoto SSH/TLS; fica pra uma fase
# futura).
#
# Base é `bookworm` (Debian), não `alpine`: o pacote `pgbackrest` oficial
# do PGDG (apt.postgresql.org, já vem pré-configurado nesta imagem) só
# publica pacotes .deb -- é o caminho testado e documentado pelo próprio
# projeto pgBackRest, ao contrário do pacote da Alpine (mantido pela
# comunidade, versão mais desatualizada).
FROM postgres:16-bookworm

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
       pgbackrest \
       cron \
       gettext-base \
  && rm -rf /var/lib/apt/lists/*

COPY config/pgbackrest.conf.template /etc/pgbackrest/pgbackrest.conf.template
COPY scripts/generate-pgbackrest-conf.sh scripts/ensure-stanza.sh scripts/run-backup.sh scripts/restore-pitr.sh /usr/local/bin/
COPY entrypoint-wrapper.sh /usr/local/bin/entrypoint-wrapper.sh
RUN chmod +x /usr/local/bin/entrypoint-wrapper.sh /usr/local/bin/generate-pgbackrest-conf.sh \
      /usr/local/bin/ensure-stanza.sh /usr/local/bin/run-backup.sh /usr/local/bin/restore-pitr.sh

ENTRYPOINT ["entrypoint-wrapper.sh"]
CMD ["postgres"]
