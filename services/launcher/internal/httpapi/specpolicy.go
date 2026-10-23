package httpapi

// Política aplicada a tudo o que este launcher cria a partir de um spec
// vindo de fora (modelo do templatesadmin, pedido de réplica de um group).
// O launcher é o único com o socket do Docker em modo escrita, por isso é
// aqui que se impede que um spec seja uma forma de ganhar o host: montar o
// socket ou "/" do host, partilhar a rede do host, ou montar o volume de
// dados de outra stack (ex: o Postgres da plataforma).

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// composeProjectLabel é a label que o docker compose põe em todo volume
// que declara -- um volume assim pertence a uma stack estática (a própria
// plataforma, o monitoring, outra app do host), nunca a algo lançado aqui.
const composeProjectLabel = "com.docker.compose.project"

// deniedHostPaths nunca podem ser montados, mesmo dentro de uma prefixo
// permitido em LAUNCHER_ALLOWED_BIND_PATHS: qualquer um deles dá controlo
// do host (socket do Docker, config do sistema, kernel, dados de outros
// containers).
var deniedHostPaths = []string{
	"/etc", "/proc", "/sys", "/dev", "/boot", "/root",
	"/run", "/var/run", "/var/lib/docker",
}

// SpecPolicy -- ver o comentário do pacote acima.
type SpecPolicy struct {
	// AllowedBindPaths são os prefixos de caminhos do HOST que um spec pode
	// montar (ex: "/srv/apps" para código-fonte em dev). Vazio = nenhum
	// caminho do host, só volumes nomeados.
	AllowedBindPaths []string
}

// validateSpec confere os binds e a rede de um spec antes de criar seja o
// que for. Erros são do pedido (o spec é que está errado), não do Docker.
func (h *Handler) validateSpec(ctx context.Context, binds []string, network string) error {
	if network == "host" || strings.HasPrefix(network, "container:") {
		return fmt.Errorf("rede %q não é permitida: partilha a pilha de rede do host ou de outro container", network)
	}
	if err := h.checkNotProjectNetwork(ctx, network); err != nil {
		return err
	}
	for _, b := range binds {
		if err := h.validateBind(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) validateBind(ctx context.Context, bind string) error {
	source, _, hasTarget := strings.Cut(bind, ":")
	if !hasTarget {
		// Só um caminho no container ("/data"): volume anónimo, sempre seguro.
		return nil
	}

	if strings.HasPrefix(source, "/") {
		return h.validateHostPath(bind, source)
	}
	if strings.HasPrefix(source, ".") || strings.HasPrefix(source, "~") {
		return fmt.Errorf("bind %q: caminhos relativos não são suportados", bind)
	}

	vol, err := h.docker.FindVolume(ctx, source)
	if err != nil {
		return fmt.Errorf("bind %q: erro a verificar o volume: %w", bind, err)
	}
	if vol != nil && vol.Labels[composeProjectLabel] != "" {
		return fmt.Errorf("bind %q: o volume %q pertence à stack compose %q e não pode ser montado por uma aplicação", bind, source, vol.Labels[composeProjectLabel])
	}
	return nil
}

func (h *Handler) validateHostPath(bind, source string) error {
	// path.Clean resolve "..", para "/srv/apps/../../etc" não passar como
	// estando dentro de "/srv/apps".
	clean := path.Clean(source)
	if clean == "/" || strings.HasSuffix(clean, "docker.sock") {
		return fmt.Errorf("bind %q: caminho do host proibido", bind)
	}
	for _, denied := range deniedHostPaths {
		if within(clean, denied) {
			return fmt.Errorf("bind %q: caminho do host proibido (%s)", bind, denied)
		}
	}
	for _, allowed := range h.policy.AllowedBindPaths {
		if within(clean, path.Clean(allowed)) {
			return nil
		}
	}
	return fmt.Errorf("bind %q: caminhos do host só são permitidos dentro de LAUNCHER_ALLOWED_BIND_PATHS -- usa um volume nomeado", bind)
}

// within diz se p é dir ou está dentro dele, respeitando a fronteira de
// diretório ("/srv/apps2" não está dentro de "/srv/apps").
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}
