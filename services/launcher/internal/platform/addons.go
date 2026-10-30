package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

const (
	// Base e utilizador dentro de todo addon Postgres. Um addon = um
	// cluster = uma base: quem precisa de outra base cria outro addon.
	addonPGUser     = "app"
	addonPGDatabase = "app"

	defaultPostgresVCPU     = 0.5
	defaultPostgresMemoryMB = 512
	defaultRedisVCPU        = 0.25
	defaultRedisMemoryMB    = 256
	defaultStorageVCPU      = 0.25
	defaultStorageMemoryMB  = 256

	// Um addon storage = um bucket, criado no arranque. A região não conta
	// para o SeaweedFS, mas os SDKs da AWS exigem uma.
	storageBucket = "app"
	storageRegion = "us-east-1"

	backupKeep = 7
)

var envVarRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// backupFileRe protege o restore: o nome vem do pedido e entra num comando.
var backupFileRe = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z\.dump$`)

type CreateAddonInput struct {
	Slug     string  `json:"slug"`
	Kind     string  `json:"kind"`
	EnvVar   string  `json:"envVar"`
	VCPU     float64 `json:"vcpu"`
	MemoryMB int64   `json:"memoryMb"`
}

// AddonView é um addon com o estado lido ao vivo do Docker.
type AddonView struct {
	store.Addon
	// Status: running, starting, unhealthy, stopped ou missing.
	Status string `json:"status"`
	// Host é como os serviços do ambiente lhe chegam (alias DNS + porta).
	Host string `json:"host"`
	// EnvVars: as variáveis que o addon injeta nos serviços do ambiente.
	EnvVars []string `json:"envVars"`
}

func newAddonView(a store.Addon, status string) AddonView {
	return AddonView{Addon: a, Status: status, Host: fmt.Sprintf("%s:%d", a.Slug, addonPort(a.Kind)), EnvVars: addonEnvNames(a)}
}

func addonContainerName(p store.Project, e store.Environment, a store.Addon) string {
	return "bs-" + p.Slug + "-" + e.Slug + "-" + a.Slug
}

func addonVolume(p store.Project, e store.Environment, a store.Addon, suffix string) string {
	return addonContainerName(p, e, a) + "-" + suffix
}

func addonPort(kind string) int {
	switch kind {
	case store.AddonRedis:
		return 6379
	case store.AddonStorage:
		return 8333
	}
	return 5432
}

func randomPassword() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("platform: sem entropia: %v", err))
	}
	return hex.EncodeToString(buf)
}

// randomAccessKey: o "utilizador" do storage, no formato das chaves da
// AWS (20 caracteres maiúsculos).
func randomAccessKey() string {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("platform: sem entropia: %v", err))
	}
	return strings.ToUpper(hex.EncodeToString(buf))
}

func addonURL(a store.Addon, user, password string) string {
	switch a.Kind {
	case store.AddonRedis:
		// "default" explícito: com o utilizador vazio ("redis://:pw@..."),
		// redis-cli e vários clientes mandam AUTH "" <pw>, que o Redis 6+
		// recusa (WRONGPASS).
		return fmt.Sprintf("redis://default:%s@%s:6379/0", password, a.Slug)
	case store.AddonStorage:
		return fmt.Sprintf("http://%s:%s@%s:8333/%s", user, password, a.Slug, storageBucket)
	}
	return fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", addonPGUser, password, a.Slug, addonPGDatabase)
}

// addonURLVar é o segredo com a URL (e a password) do addon. No storage,
// EnvVar é o prefixo de todas as variáveis.
func addonURLVar(a store.Addon) string {
	if a.Kind == store.AddonStorage {
		return a.EnvVar + "_URL"
	}
	return a.EnvVar
}

// addonEnv são os segredos geridos que o addon põe no ambiente: a URL e,
// no storage, também cada valor à parte -- os SDKs do S3 pedem-nos assim.
func addonEnv(a store.Addon, user, password string) [][2]string {
	env := [][2]string{{addonURLVar(a), addonURL(a, user, password)}}
	if a.Kind == store.AddonStorage {
		env = append(env,
			[2]string{a.EnvVar + "_ENDPOINT", fmt.Sprintf("http://%s:8333", a.Slug)},
			[2]string{a.EnvVar + "_BUCKET", storageBucket},
			[2]string{a.EnvVar + "_REGION", storageRegion},
			[2]string{a.EnvVar + "_ACCESS_KEY_ID", user},
			[2]string{a.EnvVar + "_SECRET_ACCESS_KEY", password},
		)
	}
	return env
}

func addonEnvNames(a store.Addon) []string {
	var names []string
	for _, kv := range addonEnv(a, "", "") {
		names = append(names, kv[0])
	}
	return names
}

func (s *Service) ListAddons(ctx context.Context, projectSlug, envSlug string) ([]AddonView, error) {
	p, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return nil, err
	}
	addons, err := s.db.ListAddons(ctx, e.ID)
	if err != nil {
		return nil, err
	}
	out := make([]AddonView, 0, len(addons))
	for _, a := range addons {
		out = append(out, newAddonView(a, s.addonStatus(ctx, p, e, a)))
	}
	return out, nil
}

func (s *Service) addonStatus(ctx context.Context, p store.Project, e store.Environment, a store.Addon) string {
	info, err := s.docker.InspectContainer(ctx, addonContainerName(p, e, a))
	if err != nil {
		return "missing"
	}
	if !info.State.Running {
		return "stopped"
	}
	if info.State.Health != nil {
		switch info.State.Health.Status {
		case "healthy":
			return "running"
		case "unhealthy":
			return "unhealthy"
		default:
			return "starting"
		}
	}
	return "running"
}

func (s *Service) CreateAddon(ctx context.Context, projectSlug, envSlug string, in CreateAddonInput, by string) (AddonView, error) {
	p, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return AddonView{}, err
	}
	if s.box == nil {
		// A URL com a password tem de ficar guardada algures cifrada.
		return AddonView{}, ErrSecretsDisabled
	}
	if err := validSlug("slug do addon", in.Slug); err != nil {
		return AddonView{}, err
	}
	a := store.Addon{ID: newID(), EnvironmentID: e.ID, Slug: in.Slug, Kind: in.Kind, EnvVar: in.EnvVar, VCPU: in.VCPU, MemoryMB: in.MemoryMB, BackupEnabled: true, CreatedBy: by}
	switch in.Kind {
	case store.AddonPostgres:
		a.Image = s.cfg.PostgresImage
		if a.EnvVar == "" {
			a.EnvVar = "DATABASE_URL"
		}
		if a.VCPU == 0 {
			a.VCPU = defaultPostgresVCPU
		}
		if a.MemoryMB == 0 {
			a.MemoryMB = defaultPostgresMemoryMB
		}
	case store.AddonRedis:
		a.Image = s.cfg.RedisImage
		a.BackupEnabled = false // persistência via AOF no volume; sem dumps agendados
		if a.EnvVar == "" {
			a.EnvVar = "REDIS_URL"
		}
		if a.VCPU == 0 {
			a.VCPU = defaultRedisVCPU
		}
		if a.MemoryMB == 0 {
			a.MemoryMB = defaultRedisMemoryMB
		}
	case store.AddonStorage:
		a.Image = s.cfg.StorageImage
		a.BackupEnabled = false
		if a.EnvVar == "" {
			a.EnvVar = "STORAGE"
		}
		if a.VCPU == 0 {
			a.VCPU = defaultStorageVCPU
		}
		if a.MemoryMB == 0 {
			a.MemoryMB = defaultStorageMemoryMB
		}
	default:
		return AddonView{}, invalid("tipo de addon %q desconhecido -- use \"postgres\", \"redis\" ou \"storage\"", in.Kind)
	}
	if !envVarRe.MatchString(a.EnvVar) || len(a.EnvVar) > 100 {
		return AddonView{}, invalid("nome de variável %q inválido", a.EnvVar)
	}
	if a.VCPU < 0 || a.MemoryMB < 64 {
		return AddonView{}, invalid("recursos inválidos: vCPU >= 0 e pelo menos 64 MB de memória")
	}
	for _, name := range addonEnvNames(a) {
		if existing, err := s.db.GetSecret(ctx, e.ID, name); err == nil {
			return AddonView{}, fmt.Errorf("%w: já existe o segredo %s neste ambiente (gerido por %q) -- escolhe outra variável", ErrConflict, name, existing.ManagedBy)
		}
	}
	if err := s.checkQuota(ctx, p, a.VCPU, a.MemoryMB); err != nil {
		return AddonView{}, err
	}

	if err := s.db.CreateAddon(ctx, a); err != nil {
		return AddonView{}, mapStoreErr(err)
	}
	user, password := "", randomPassword()
	if a.Kind == store.AddonStorage {
		user = randomAccessKey()
	}
	undo := func() {
		s.deleteAddonSecrets(ctx, e.ID, a)
		_ = s.db.DeleteAddon(ctx, a.ID)
	}
	for _, kv := range addonEnv(a, user, password) {
		if err := s.putSecret(ctx, e.ID, kv[0], kv[1], "addon:"+a.Slug, by); err != nil {
			undo()
			return AddonView{}, err
		}
	}
	if err := s.startAddonContainer(ctx, p, e, a, user, password); err != nil {
		undo()
		return AddonView{}, err
	}
	s.logger.Info("addon criado", "project", p.Slug, "env", e.Slug, "addon", a.Slug, "kind", a.Kind)
	return newAddonView(a, "starting"), nil
}

func (s *Service) deleteAddonSecrets(ctx context.Context, envID string, a store.Addon) {
	for _, name := range addonEnvNames(a) {
		_ = s.db.DeleteSecret(ctx, envID, name)
	}
}

func addonLabels(p store.Project, e store.Environment, a store.Addon) map[string]string {
	labels := environmentLabels(p, e)
	labels[LabelRole] = RoleAddon
	labels[LabelAddon] = a.ID
	// O slug como "serviço" para os logs: no Grafana, os de uma base de
	// dados filtram-se como os de qualquer app.
	labels[LabelServiceSlug] = a.Slug
	labels[dockerclient.LabelInstanceType] = "addon-" + a.Kind
	labels[dockerclient.LabelVCPU] = strconv.FormatFloat(a.VCPU, 'f', -1, 64)
	labels[dockerclient.LabelMemoryMB] = strconv.FormatInt(a.MemoryMB, 10)
	return labels
}

// startAddonContainer cria os volumes (idempotente) e o container do addon.
// É também o caminho de recriação do reconciliador: o volume de dados
// sobrevive, só o container é novo.
func (s *Service) startAddonContainer(ctx context.Context, p store.Project, e store.Environment, a store.Addon, user, password string) error {
	labels := addonLabels(p, e, a)
	volLabels := environmentLabels(p, e)
	volLabels[LabelAddon] = a.ID
	dataVol := addonVolume(p, e, a, "data")
	if err := s.docker.CreateVolume(ctx, dataVol, volLabels); err != nil {
		return fmt.Errorf("criando volume %s: %w", dataVol, err)
	}
	if err := s.ensureImage(ctx, a.Image, p.ID); err != nil {
		return err
	}

	memory := a.MemoryMB * 1024 * 1024
	req := dockerclient.CreateContainerRequest{
		Image:  a.Image,
		Labels: labels,
		HostConfig: &dockerclient.CreateHostConfig{
			NetworkMode:   e.Network,
			RestartPolicy: dockerclient.UnlessStopped(),
			SecurityOpt:   dockerclient.AppSecurityOpt(),
			Resources: dockerclient.Resources{
				NanoCPUs:   int64(a.VCPU * 1e9),
				Memory:     memory,
				MemorySwap: memory,
			},
		},
		NetworkingConfig: dockerclient.WithAliases(e.Network, a.Slug),
	}
	second := int64(time.Second)
	switch a.Kind {
	case store.AddonPostgres:
		backupVol := addonVolume(p, e, a, "backups")
		if err := s.docker.CreateVolume(ctx, backupVol, volLabels); err != nil {
			return fmt.Errorf("criando volume %s: %w", backupVol, err)
		}
		req.Env = []string{"POSTGRES_USER=" + addonPGUser, "POSTGRES_PASSWORD=" + password, "POSTGRES_DB=" + addonPGDatabase}
		req.HostConfig.Binds = []string{dataVol + ":/var/lib/postgresql/data"}
		req.Healthcheck = &dockerclient.Healthcheck{
			Test:     []string{"CMD-SHELL", "pg_isready -U " + addonPGUser + " -d " + addonPGDatabase},
			Interval: 5 * second, Timeout: 3 * second, Retries: 5, StartPeriod: 20 * second,
		}
	case store.AddonRedis:
		// noeviction: um Redis cheio recusa escritas em vez de apagar
		// chaves em silêncio -- o certo para filas e sessões.
		maxMemory := a.MemoryMB * 9 / 10
		req.Env = []string{"REDISCLI_AUTH=" + password}
		req.Cmd = []string{"redis-server", "--requirepass", password, "--appendonly", "yes",
			"--maxmemory", fmt.Sprintf("%dmb", maxMemory), "--maxmemory-policy", "noeviction"}
		req.HostConfig.Binds = []string{dataVol + ":/data"}
		req.Healthcheck = &dockerclient.Healthcheck{
			Test:     []string{"CMD-SHELL", "redis-cli ping | grep -q PONG"},
			Interval: 5 * second, Timeout: 3 * second, Retries: 5, StartPeriod: 5 * second,
		}
	case store.AddonStorage:
		// "weed mini": master, volume, filer e a API do S3 num só processo.
		// As credenciais vêm do env; as portas internas (filer, volumes)
		// ficam fechadas com chaves JWT novas a cada arranque. -ip fixo:
		// os volumes registam-se no master por endereço, e o do container
		// muda quando é recriado. GOMEMLIMIT: o Go só devolve memória
		// quando sente o limite, e sem ele um envio grande chega ao do
		// container.
		req.Env = []string{
			fmt.Sprintf("GOMEMLIMIT=%dMiB", a.MemoryMB*8/10),
			"AWS_ACCESS_KEY_ID=" + user,
			"AWS_SECRET_ACCESS_KEY=" + password,
			"S3_BUCKET=" + storageBucket,
			"WEED_JWT_SIGNING_KEY=" + randomPassword(),
			"WEED_JWT_SIGNING_READ_KEY=" + randomPassword(),
			"WEED_JWT_FILER_SIGNING_KEY=" + randomPassword(),
			"WEED_JWT_FILER_SIGNING_READ_KEY=" + randomPassword(),
		}
		req.Cmd = []string{"mini", "-dir=/data", "-ip=127.0.0.1", "-ip.bind=0.0.0.0", "-admin.ui=false", "-master.telemetry=false"}
		req.HostConfig.Binds = []string{dataVol + ":/data"}
		req.Healthcheck = &dockerclient.Healthcheck{
			Test:     []string{"CMD-SHELL", "wget -qO- http://127.0.0.1:8333/healthz >/dev/null"},
			Interval: 5 * second, Timeout: 3 * second, Retries: 5, StartPeriod: 20 * second,
		}
	}

	// Os addons ficam no nó local: os dados estão num volume dessa máquina.
	req.Labels[LabelNode] = LocalNode
	name := addonContainerName(p, e, a)
	// Um container antigo com o mesmo nome (parado, ou de uma tentativa
	// falhada) impediria a criação.
	_ = s.docker.RemoveContainer(ctx, name, true)
	id, err := s.docker.CreateContainer(ctx, name, req)
	if err != nil {
		return fmt.Errorf("criando container do addon: %w", err)
	}
	if err := s.docker.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("arrancando addon: %w", err)
	}
	return nil
}

// addonLogin recupera o utilizador e a password a partir da URL guardada
// no segredo gerido -- não há segunda cópia em lado nenhum.
func (s *Service) addonLogin(ctx context.Context, envID string, a store.Addon) (user, password string, err error) {
	values, err := s.secretValues(ctx, envID)
	if err != nil {
		return "", "", err
	}
	name := addonURLVar(a)
	raw, ok := values[name]
	if !ok {
		return "", "", fmt.Errorf("segredo %s do addon %s desapareceu", name, a.Slug)
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return "", "", fmt.Errorf("URL do addon %s ilegível", a.Slug)
	}
	password, _ = u.User.Password()
	return u.User.Username(), password, nil
}

func (s *Service) addonScope(ctx context.Context, projectSlug, envSlug, addonSlug string) (store.Project, store.Environment, store.Addon, error) {
	p, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return store.Project{}, store.Environment{}, store.Addon{}, err
	}
	a, err := s.db.GetAddonBySlug(ctx, e.ID, addonSlug)
	if err != nil {
		return store.Project{}, store.Environment{}, store.Addon{}, mapStoreErr(err)
	}
	return p, e, a, nil
}

// DeleteAddon remove o container e o segredo. Os volumes (dados e
// backups) só vão com purge -- apagar uma base por engano não deve ser
// irreversível por defeito.
func (s *Service) DeleteAddon(ctx context.Context, projectSlug, envSlug, addonSlug string, purge bool) error {
	p, e, a, err := s.addonScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return err
	}
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	name := addonContainerName(p, e, a)
	_ = s.docker.StopContainer(ctx, name, 30)
	if err := s.docker.RemoveContainer(ctx, name, true); err != nil && !isNotFound(err) {
		return fmt.Errorf("removendo container do addon: %w", err)
	}
	if purge {
		for _, suffix := range []string{"data", "backups"} {
			if err := s.docker.RemoveVolume(ctx, addonVolume(p, e, a, suffix)); err != nil {
				return fmt.Errorf("apagando volume: %w", err)
			}
		}
	}
	s.deleteAddonSecrets(ctx, e.ID, a)
	return s.db.DeleteAddon(ctx, a.ID)
}

func (s *Service) SetAddonBackups(ctx context.Context, projectSlug, envSlug, addonSlug string, enabled bool) error {
	_, _, a, err := s.addonScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return err
	}
	if enabled && a.Kind != store.AddonPostgres {
		return invalid("backups agendados só existem para addons postgres")
	}
	return s.db.SetAddonBackupEnabled(ctx, a.ID, enabled)
}

// backupJob monta o job de pg_dump: corre com a própria imagem do addon
// (pg_dump da mesma versão do servidor), fala com ele pela rede do
// ambiente, e escreve no volume de backups, mantendo os backupKeep mais
// recentes.
func (s *Service) backupJob(ctx context.Context, p store.Project, e store.Environment, a store.Addon, by string) (jobRequest, error) {
	if a.Kind != store.AddonPostgres {
		return jobRequest{}, invalid("backups só existem para addons postgres")
	}
	_, pw, err := s.addonLogin(ctx, e.ID, a)
	if err != nil {
		return jobRequest{}, err
	}
	script := fmt.Sprintf(`set -e
f=/backups/$(date -u +%%Y%%m%%dT%%H%%M%%SZ).dump
pg_dump -Fc -h "$PGHOST" -U %s -d %s -f "$f"
echo "backup: $(basename "$f") ($(stat -c %%s "$f") bytes)"
ls -1t /backups/*.dump | tail -n +%d | xargs -r rm -f --`, addonPGUser, addonPGDatabase, backupKeep+1)
	id := a.ID
	return jobRequest{
		containerSpec: containerSpec{
			project: p, env: e, image: a.Image,
			cmd:     []string{"sh", "-c", script},
			envVars: []string{"PGHOST=" + a.Slug, "PGPASSWORD=" + pw},
			binds:   []string{addonVolume(p, e, a, "backups") + ":/backups"},
		},
		kind: "backup", addonID: &id, by: by,
	}, nil
}

// BackupAddon dispara um backup agora (em segundo plano).
func (s *Service) BackupAddon(ctx context.Context, projectSlug, envSlug, addonSlug, by string) (store.Job, error) {
	p, e, a, err := s.addonScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return store.Job{}, err
	}
	req, err := s.backupJob(ctx, p, e, a, by)
	if err != nil {
		return store.Job{}, err
	}
	return s.startJob(req, func(job store.Job) {
		if job.Status == store.JobSucceeded {
			_ = s.db.MarkAddonBackedUp(context.Background(), a.ID, time.Now())
		}
	})
}

type Backup struct {
	File string `json:"file"`
	Size int64  `json:"size"`
}

// ListAddonBackups lista o volume de backups com um container de vida
// curta -- não fica registado como job.
func (s *Service) ListAddonBackups(ctx context.Context, projectSlug, envSlug, addonSlug string) ([]Backup, error) {
	p, e, a, err := s.addonScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return nil, err
	}
	if a.Kind != store.AddonPostgres {
		return []Backup{}, nil
	}
	code, output, err := s.runToCompletion(ctx, containerSpec{
		project: p, env: e, image: a.Image,
		name:    "bs-" + p.Slug + "-" + e.Slug + "-ls-" + newID()[:8],
		cmd:     []string{"sh", "-c", `for f in /backups/*.dump; do [ -e "$f" ] && echo "$(basename "$f") $(stat -c %s "$f")"; done; true`},
		binds:   []string{addonVolume(p, e, a, "backups") + ":/backups:ro"},
		timeout: time.Minute,
	})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("listando backups (exit %d): %s", code, output)
	}
	return parseBackupList(output), nil
}

func parseBackupList(output string) []Backup {
	out := []Backup{}
	for _, line := range splitLines(output) {
		var b Backup
		if n, _ := fmt.Sscanf(line, "%s %d", &b.File, &b.Size); n == 2 && backupFileRe.MatchString(b.File) {
			out = append(out, b)
		}
	}
	// Mais recente primeiro: os nomes são timestamps ordenáveis.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// RestoreAddon repõe um backup por cima da base atual (pg_restore --clean).
func (s *Service) RestoreAddon(ctx context.Context, projectSlug, envSlug, addonSlug, file, by string) (store.Job, error) {
	p, e, a, err := s.addonScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return store.Job{}, err
	}
	if a.Kind != store.AddonPostgres {
		return store.Job{}, invalid("restore só existe para addons postgres")
	}
	if !backupFileRe.MatchString(file) {
		return store.Job{}, invalid("ficheiro de backup %q inválido", file)
	}
	_, pw, err := s.addonLogin(ctx, e.ID, a)
	if err != nil {
		return store.Job{}, err
	}
	id := a.ID
	return s.startJob(jobRequest{
		containerSpec: containerSpec{
			project: p, env: e, image: a.Image,
			cmd:     []string{"sh", "-c", fmt.Sprintf(`pg_restore --clean --if-exists --no-owner --no-privileges -h "$PGHOST" -U %s -d %s "/backups/$BACKUP_FILE"`, addonPGUser, addonPGDatabase)},
			envVars: []string{"PGHOST=" + a.Slug, "PGPASSWORD=" + pw, "BACKUP_FILE=" + file},
			binds:   []string{addonVolume(p, e, a, "backups") + ":/backups:ro"},
		},
		kind: "restore", addonID: &id, by: by,
	}, nil)
}
