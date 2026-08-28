// Capacidade e limites de recursos: traduz um "tipo de instância" do
// catálogo (services/templatesadmin, service_templates.instance_types)
// para limites reais de cgroup no Docker, e trava lançamentos que
// estourem a memória física do host.
//
// Por que só a memória trava: CPU é um recurso compressível -- somar 20
// vCPU num host de 8 só deixa toda a gente mais lenta, e recusar aí daria
// falsos negativos constantes em ambientes de desenvolvimento. Memória
// não é: quando a soma dos limites passa a RAM real, o kernel começa a
// matar containers à sorte (OOM), incluindo os da própria plataforma.
// Esse é o único cenário destrutivo, e é o único que bloqueamos.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"launcher/internal/dockerclient"
	"launcher/internal/templatesclient"
)

const (
	// labelInstanceType/labelVCPU/labelMemoryMB gravam o tamanho aplicado
	// nas labels do próprio container. É a única fonte de verdade da
	// contabilidade de capacidade: uma réplica criada por um
	// autoscaler-group nunca passa por launcher.instances, então somar a
	// base de dados deixaria de fora exatamente os containers que mais
	// aparecem e desaparecem. Ler as labels de um único ListContainers
	// cobre instâncias e réplicas com uma só chamada ao Docker.
	labelInstanceType = "base-stack.resources.instance_type"
	labelVCPU         = "base-stack.resources.vcpu"
	labelMemoryMB     = "base-stack.resources.memory_mb"

	// bytesPerMB é MiB, não MB decimal -- é a unidade que o Docker usa e
	// que `docker inspect` devolve.
	bytesPerMB = 1024 * 1024
	// nanosPerCPU é a unidade de HostConfig.NanoCpus.
	nanosPerCPU = 1_000_000_000
)

// CapacityConfig descreve o host onde este launcher cria containers e o
// tamanho aplicado quando ninguém escolhe nenhum.
type CapacityConfig struct {
	// DefaultInstanceType é o último recurso da cascata de resolução
	// (pedido -> default do modelo -> este valor).
	DefaultInstanceType string
	// GroupInstanceType é o tamanho do container do PRÓPRIO cmd/group --
	// um processo de controlo, não a aplicação; nunca deve herdar o
	// tamanho pedido para as réplicas que ele vai gerir.
	GroupInstanceType string
	// HostVCPU/HostMemoryMB são a capacidade física declarada. Zero
	// desliga a contabilidade e, com ela, o bloqueio.
	HostVCPU     float64
	HostMemoryMB int64
	// MemoryOvercommitRatio permite aceitar alguma sobrecarga deliberada
	// (1.0 = nenhuma). Só afeta o bloqueio, nunca os limites por container.
	MemoryOvercommitRatio float64
}

// resources traduz um tipo de instância em limites de cgroup.
func resources(it templatesclient.InstanceType) dockerclient.Resources {
	memory := it.MemoryMB * bytesPerMB
	// MemorySwap igual a Memory desativa o swap para o container: sem
	// isto, um container que estoura o limite passa a paginar para disco
	// e arrasta o host inteiro em vez de morrer depressa e ser recriado.
	swap := memory
	if it.MemorySwapMB != nil {
		swap = *it.MemorySwapMB * bytesPerMB
	}
	r := dockerclient.Resources{
		NanoCPUs:   int64(it.VCPU * nanosPerCPU),
		Memory:     memory,
		MemorySwap: swap,
	}
	if it.PidsLimit > 0 {
		limit := it.PidsLimit
		r.PidsLimit = &limit
	}
	return r
}

// resourceLabels são as labels de contabilidade que acompanham todo
// container criado com um tamanho definido.
func resourceLabels(it templatesclient.InstanceType) map[string]string {
	return map[string]string{
		labelInstanceType: it.Name,
		labelVCPU:         strconv.FormatFloat(it.VCPU, 'f', -1, 64),
		labelMemoryMB:     strconv.FormatInt(it.MemoryMB, 10),
	}
}

// resolveInstanceType aplica a cascata pedido -> default do modelo ->
// default global, e valida o resultado contra o catálogo. O token de quem
// chamou é reenviado ao templatesadmin, tal como em Templates.Get.
func (h *Handler) resolveInstanceType(ctx context.Context, requested, templateDefault, token string) (templatesclient.InstanceType, error) {
	name := requested
	if name == "" {
		name = templateDefault
	}
	if name == "" {
		name = h.capacity.DefaultInstanceType
	}
	if name == "" {
		return templatesclient.InstanceType{}, errors.New(`nenhum tipo de instância escolhido e não há default configurado (LAUNCHER_DEFAULT_INSTANCE_TYPE)`)
	}

	it, err := h.templates.GetInstanceType(ctx, name, token)
	if err != nil {
		return templatesclient.InstanceType{}, fmt.Errorf("tipo de instância %q: %w", name, err)
	}
	// Um tipo desativado continua legível (o ecrã de administração
	// precisa dele), mas deixa de poder ser lançado -- é assim que se
	// retira um tamanho de circulação sem partir quem já o usa.
	if !it.Enabled {
		return templatesclient.InstanceType{}, fmt.Errorf("tipo de instância %q está desativado", name)
	}
	return it, nil
}

// Capacity é o corpo de GET /v1/capacity.
type Capacity struct {
	HostVCPU          float64 `json:"hostVcpu"`
	HostMemoryMB      int64   `json:"hostMemoryMb"`
	AllocatedVCPU     float64 `json:"allocatedVcpu"`
	AllocatedMemoryMB int64   `json:"allocatedMemoryMb"`
	// ContainerCount conta só os containers da plataforma que declaram um
	// tamanho -- os criados antes desta feature não entram na soma.
	ContainerCount int `json:"containerCount"`
	// Enforced diz se um lançamento pode de facto ser recusado por falta
	// de memória; falso quando HostMemoryMB não foi configurado.
	Enforced bool `json:"enforced"`
}

// allocated soma os recursos de todos os containers da plataforma em
// execução, instâncias e réplicas incluídas.
func (h *Handler) allocated(ctx context.Context) (Capacity, error) {
	containers, err := h.docker.ListContainers(ctx, false, map[string][]string{
		"label": {dockerclient.PlatformLabel + "=" + dockerclient.PlatformLabelValue},
	})
	if err != nil {
		return Capacity{}, fmt.Errorf("listando containers para contabilizar capacidade: %w", err)
	}

	out := Capacity{
		HostVCPU:     h.capacity.HostVCPU,
		HostMemoryMB: h.capacity.HostMemoryMB,
		Enforced:     h.capacity.HostMemoryMB > 0,
	}
	for _, c := range containers {
		mem, err := strconv.ParseInt(c.Labels[labelMemoryMB], 10, 64)
		if err != nil {
			continue
		}
		vcpu, _ := strconv.ParseFloat(c.Labels[labelVCPU], 64)
		out.AllocatedVCPU += vcpu
		out.AllocatedMemoryMB += mem
		out.ContainerCount++
	}
	return out, nil
}

// errInsufficientCapacity é devolvido quando lançar mais memória do que o
// host tem -- ver o package doc para por que só a memória trava.
var errInsufficientCapacity = errors.New("capacidade de memória insuficiente no host")

func (h *Handler) checkMemoryCapacity(ctx context.Context, wantMemoryMB int64) error {
	if h.capacity.HostMemoryMB <= 0 {
		return nil
	}
	current, err := h.allocated(ctx)
	if err != nil {
		return err
	}
	ratio := h.capacity.MemoryOvercommitRatio
	if ratio <= 0 {
		ratio = 1
	}
	budget := int64(float64(h.capacity.HostMemoryMB) * ratio)
	if current.AllocatedMemoryMB+wantMemoryMB > budget {
		return fmt.Errorf("%w: %d MB já alocados + %d MB pedidos excedem o teto de %d MB",
			errInsufficientCapacity, current.AllocatedMemoryMB, wantMemoryMB, budget)
	}
	return nil
}

func (h *Handler) handleCapacity(w http.ResponseWriter, r *http.Request, _, _ string) {
	c, err := h.allocated(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "capacity_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}
