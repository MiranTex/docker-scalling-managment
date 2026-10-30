package platform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"launcher/internal/store"
)

func TestScheduleChoosesAFittingNode(t *testing.T) {
	node := func(name, arch string, schedulable bool) store.Node {
		return store.Node{Name: name, OS: "linux", Arch: arch, Schedulable: schedulable}
	}
	cands := []candidate{
		{Node: node("intel-cheio", "amd64", true), Online: true, Network: "cluster", Allocated: Usage{MemoryMB: 3800}, Capacity: 4096},
		{Node: node("intel", "amd64", true), Online: true, Network: "cluster", Allocated: Usage{MemoryMB: 1000}, Capacity: 4096},
		{Node: node("arm-grande", "arm64", true), Online: true, Network: "local", Capacity: 16384},
		{Node: node("em-manutencao", "amd64", false), Online: true, Network: "cluster", Capacity: 65536},
		{Node: node("sem-rede", "amd64", true), Online: true, Network: "a entrar no swarm", Capacity: 65536},
		{Node: node("desligado", "amd64", true), Online: false, Capacity: 65536},
	}

	for _, c := range []struct {
		name string
		p    placement
		want string
	}{
		{"sem saber a imagem: o com mais memória livre", placement{MemoryMB: 512}, "arm-grande"},
		{"imagem só amd64", placement{MemoryMB: 512, Platforms: []string{"linux/amd64"}}, "intel"},
		{"variante da arquitetura", placement{MemoryMB: 512, Platforms: []string{"linux/arm64/v8"}}, "arm-grande"},
		{"multi-arquitetura", placement{MemoryMB: 512, Platforms: []string{"linux/amd64", "linux/arm64"}}, "arm-grande"},
	} {
		if got, err := schedule(cands, c.p); err != nil || got != c.want {
			t.Errorf("%s: %q, %v -- queria %q", c.name, got, err, c.want)
		}
	}

	// Espalhar: o nó com menos réplicas do serviço ganha, mesmo com menos
	// memória livre.
	spread := []candidate{
		{Node: node("grande", "amd64", true), Online: true, Network: "cluster", Capacity: 32768, Siblings: 2},
		{Node: node("pequeno", "amd64", true), Online: true, Network: "cluster", Capacity: 4096, Siblings: 1},
	}
	if got, _ := schedule(spread, placement{MemoryMB: 512}); got != "pequeno" {
		t.Errorf("espalhar: %q, queria pequeno", got)
	}
	spread[1].Siblings = 2
	if got, _ := schedule(spread, placement{MemoryMB: 512}); got != "grande" {
		t.Errorf("empate em réplicas, decide a memória: %q, queria grande", got)
	}

	// Sem nenhum que sirva, o erro explica cada nó.
	_, err := schedule(cands, placement{MemoryMB: 4000, Platforms: []string{"linux/amd64"}})
	if !errors.Is(err, ErrNoNode) || strings.Contains(err.Error(), "quota") {
		t.Fatalf("erro = %v", err)
	}
	for _, want := range []string{"intel-cheio: 3800 MB alocados", "arm-grande: a imagem não tem linux/arm64", "em-manutencao: em manutenção", "desligado: offline", "sem-rede: sem rede para apps (a entrar no swarm)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("o erro não explica %q: %v", want, err)
		}
	}
	if _, err := schedule(nil, placement{}); err == nil || !strings.Contains(err.Error(), "não há nós") {
		t.Errorf("sem nós: %v", err)
	}
}

func TestReplicasArePlacedOnTheLocalNode(t *testing.T) {
	ctx := context.Background()
	svc, docker, _, _ := newDeployService(t)
	svc.registerLocalNode(ctx)
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(8080), Replicas: intp(2)}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, err := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if d := waitDeploy(t, svc, "web", dep.ID); d.Status != store.DeploySucceeded {
		t.Fatalf("deploy = %s: %s", d.Status, d.Error)
	}

	for _, name := range appContainers(docker, "app:1") {
		if node := docker.created[name].Labels[LabelNode]; node != LocalNode {
			t.Errorf("%s no nó %q", name, node)
		}
	}
	replicas, _ := svc.ListReplicas(ctx, "loja", "prod", "web")
	if len(replicas) != 2 || replicas[0].Node != LocalNode {
		t.Errorf("réplicas = %+v", replicas)
	}

	// O nó local tem o que o Docker diz (x86_64 -> amd64) e o que está
	// alocado.
	nodes, err := svc.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nós = %+v", nodes)
	}
	n := nodes[0]
	if n.Name != LocalNode || n.Arch != "amd64" || n.CPUs != 4 || n.MemoryMB != 16384 || !n.Online || !n.Schedulable {
		t.Errorf("nó local = %+v", n)
	}
	if n.Replicas != 2 || n.Allocated.MemoryMB != 2*512 {
		t.Errorf("alocado no nó = %d réplicas, %+v", n.Replicas, n.Allocated)
	}

	// Em manutenção, não recebe réplicas novas -- e o deploy diz porquê.
	if _, err := svc.SetNodeSchedulable(ctx, LocalNode, false); err != nil {
		t.Fatal(err)
	}
	dep, err = svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:2"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if d := waitDeploy(t, svc, "web", dep.ID); d.Status != store.DeployFailed || d.Error != "nenhum nó pode receber a réplica: local: em manutenção" {
		t.Errorf("deploy com o nó em manutenção = %s: %s", d.Status, d.Error)
	}
	if got := appContainers(docker, "app:1"); len(got) != 2 {
		t.Errorf("as réplicas que já lá estavam continuam: %v", got)
	}
	// Subir réplicas também não passa, e o serviço mostra porquê.
	if _, err := svc.UpdateService(ctx, "loja", "prod", "web", ServiceInput{Replicas: intp(3)}); err != nil {
		t.Fatal(err)
	}
	svc.reconcileOnce(ctx)
	view, _ := svc.GetService(ctx, "loja", "prod", "web")
	if !strings.Contains(view.Problem, "2 de 3") || !strings.Contains(view.Problem, "local: em manutenção") {
		t.Errorf("problema reportado = %q", view.Problem)
	}
	if _, err := svc.SetNodeSchedulable(ctx, "nao-existe", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("nó inexistente: %v", err)
	}
}
