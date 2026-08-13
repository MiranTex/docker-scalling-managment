-- Remove os campos de config de grupo (réplicas, thresholds de CPU,
-- target service, portas de host) do modelo de serviço -- essa
-- config passou a ser preenchida no momento de CRIAR um group, não a
-- viver junto do launch template (ver services/launcher/internal/httpapi,
-- POST /v1/instances com kind="group", e o formulário "Criar grupo" em
-- /admin/autoscaler no portal). Um modelo aqui volta a ser só o que o
-- container da aplicação precisa: imagem, comando, env, labels, volumes,
-- rede.
ALTER TABLE service_templates.templates
    DROP COLUMN target_service,
    DROP COLUMN backend_port,
    DROP COLUMN min_replicas,
    DROP COLUMN max_replicas,
    DROP COLUMN cpu_scale_up_percent,
    DROP COLUMN cpu_scale_down_percent,
    DROP COLUMN host_proxy_port,
    DROP COLUMN host_metrics_port;
