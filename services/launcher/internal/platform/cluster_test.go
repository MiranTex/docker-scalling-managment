package platform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"launcher/internal/store"
)

// addRemoteNode regista um nó remoto ligado (com outro Docker falso) e,
// com inSwarm, já na rede do cluster.
func addRemoteNode(t *testing.T, svc *Service, name string, inSwarm bool) *fakeDocker {
	t.Helper()
	ctx := context.Background()
	if err := svc.db.CreateNodeJoinToken(ctx, hashToken("bsj_"+name), "t", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.JoinNode(ctx, hashToken("bsj_"+name), store.Node{Name: name}, hashToken("bsn_"+name)); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.UpdateNodeFacts(ctx, store.Node{Name: name, OS: "linux", Arch: "amd64", CPUs: 4, MemoryMB: 16384}); err != nil {
		t.Fatal(err)
	}
	remote := newFakeDocker()
	conn := &agentConn{docker: remote}
	if inSwarm {
		conn.swarmNodeID = "swarm-" + name
	}
	svc.agents.put(name, conn)
	return remote
}

func TestClusterPlacesReplicasOnRemoteNodes(t *testing.T) {
	ctx := context.Background()
	svc, local, _, _ := newDeployService(t)
	svc.cfg.Cluster = true
	svc.registerLocalNode(ctx)

	// Num cluster, as redes novas dos ambientes são overlay cifradas.
	if _, err := svc.CreateEnvironment(ctx, "loja", "staging", "t"); err != nil {
		t.Fatal(err)
	}
	if d := local.drivers["bs-loja-staging"]; d != "overlay" {
		t.Errorf("rede de um ambiente novo = %q, queria overlay", d)
	}

	// Um nó remoto que ainda não entrou no swarm não recebe apps.
	remote := addRemoteNode(t, svc, "node-1", false)
	if _, err := svc.SetNodeSchedulable(ctx, LocalNode, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80), Replicas: intp(2)}, "t"); err != nil {
		t.Fatal(err)
	}
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	if d := waitDeploy(t, svc, "web", dep.ID); d.Status != store.DeployFailed || !strings.Contains(d.Error, "node-1: sem rede para apps (a entrar no swarm)") {
		t.Fatalf("deploy sem rede = %s: %s", d.Status, d.Error)
	}

	// Já no swarm: as réplicas vão para lá (o local está em manutenção).
	svc.agents.get("node-1").swarmNodeID = "swarm-node-1"
	dep, _ = svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	if d := waitDeploy(t, svc, "web", dep.ID); d.Status != store.DeploySucceeded {
		t.Fatalf("deploy no nó remoto = %s: %s\n%s", d.Status, d.Error, d.Log)
	}
	onRemote := appContainers(remote, "app:1")
	if len(onRemote) != 2 || len(appContainers(local, "")) != 0 {
		t.Fatalf("réplicas no remoto = %v, no local = %v", onRemote, appContainers(local, ""))
	}
	req := remote.created[onRemote[0]]
	if req.Labels[LabelNode] != "node-1" || req.HostConfig.NetworkMode != "bs-loja-prod" {
		t.Errorf("réplica remota: nó %q, rede %q", req.Labels[LabelNode], req.HostConfig.NetworkMode)
	}

	// O resto da plataforma vê-as como se fossem locais: a lista diz o nó,
	// e parar o serviço para-as lá.
	replicas, err := svc.ListReplicas(ctx, "loja", "prod", "web")
	if err != nil || len(replicas) != 2 || replicas[0].Node != "node-1" {
		t.Fatalf("réplicas = %+v, %v", replicas, err)
	}
	nodes, _ := svc.ListNodes(ctx)
	for _, n := range nodes {
		if n.Name == "node-1" && (n.Replicas != 2 || n.Network != "cluster") {
			t.Errorf("node-1 = %d réplicas, rede %q", n.Replicas, n.Network)
		}
	}
	if err := svc.SetServiceState(ctx, "loja", "prod", "web", "stopped"); err != nil {
		t.Fatal(err)
	}
	svc.reconcileOnce(ctx)
	if got := runningApps(remote); len(got) != 0 {
		t.Errorf("depois de parar, ainda a correr no remoto: %v", got)
	}
}

func runningApps(d *fakeDocker) []string {
	var out []string
	for _, c := range d.byRole(RoleApp) {
		if c.State == "running" {
			out = append(out, containerName(c))
		}
	}
	return out
}

// twoRemoteNodes: cluster com o nó local em manutenção e dois nós remotos
// no swarm, e um serviço web de 2 réplicas já com deploy (uma em cada).
func twoRemoteNodes(t *testing.T) (*Service, map[string]*fakeDocker) {
	t.Helper()
	ctx := context.Background()
	svc, _, _, _ := newDeployService(t)
	svc.cfg.Cluster = true
	svc.registerLocalNode(ctx)
	nodes := map[string]*fakeDocker{"node-1": addRemoteNode(t, svc, "node-1", true)}
	if _, err := svc.SetNodeSchedulable(ctx, LocalNode, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80), Replicas: intp(2)}, "t"); err != nil {
		t.Fatal(err)
	}
	// Os dois nós entram antes do deploy, para ficar uma réplica em cada.
	nodes["node-2"] = addRemoteNode(t, svc, "node-2", true)
	dep, _ := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	if d := waitDeploy(t, svc, "web", dep.ID); d.Status != store.DeploySucceeded {
		t.Fatalf("deploy = %s: %s", d.Status, d.Error)
	}
	if len(runningApps(nodes["node-1"])) != 1 || len(runningApps(nodes["node-2"])) != 1 {
		t.Fatalf("réplicas: node-1 %v, node-2 %v", runningApps(nodes["node-1"]), runningApps(nodes["node-2"]))
	}
	return svc, nodes
}

func TestRemoteNodeLossAndReturn(t *testing.T) {
	ctx := context.Background()
	svc, nodes := twoRemoteNodes(t)
	svc.reconcileOnce(ctx) // o launcher vê o que corre em cada nó
	conn := svc.agents.get("node-1")

	// O túnel do node-1 cai: por agora, a réplica dele continua a contar.
	svc.agents.drop("node-1", nil)
	svc.reconcileOnce(ctx)
	if got := runningApps(nodes["node-2"]); len(got) != 1 {
		t.Fatalf("com o node-1 sem sinal, o node-2 ficou com %v -- não devia mudar nada", got)
	}

	// Passa o prazo: o node-1 está perdido e a réplica dele renasce no
	// node-2.
	svc.agents.mu.Lock()
	svc.agents.watch.down["node-1"] = time.Now().Add(-2 * nodeLossGrace)
	svc.agents.mu.Unlock()
	svc.reconcileOnce(ctx)
	if got := runningApps(nodes["node-2"]); len(got) != 2 {
		t.Fatalf("depois de perder o node-1, o node-2 tem %v -- queria 2", got)
	}
	nodeList, _ := svc.ListNodes(ctx)
	for _, n := range nodeList {
		if n.Name == "node-1" && n.Status != "perdido" {
			t.Errorf("node-1 = %q, queria perdido", n.Status)
		}
	}

	// O node-1 volta com a réplica antiga ainda a correr: sobra uma, e sai
	// a mais antiga -- a dele.
	svc.agents.put("node-1", conn)
	svc.reconcileOnce(ctx)
	time.Sleep(20 * time.Millisecond) // drainDelay dos testes
	svc.reconcileOnce(ctx)
	if got := runningApps(nodes["node-1"]); len(got) != 0 {
		t.Errorf("a réplica antiga do node-1 ficou: %v", got)
	}
	if got := runningApps(nodes["node-2"]); len(got) != 2 {
		t.Errorf("o node-2 tem %v -- queria as 2 réplicas", got)
	}
}

// O launcher acabou de arrancar e um nó ainda não voltou: não se sabe o
// que lá corre, por isso nada se cria (era o que duplicava réplicas).
func TestLauncherWaitsForRemoteNodesAfterStart(t *testing.T) {
	ctx := context.Background()
	svc, nodes := twoRemoteNodes(t)
	conn := svc.agents.get("node-1")
	svc.agents.drop("node-1", nil)
	svc.agents.mu.Lock()
	svc.agents.watch = newNodeWatch() // como depois de um restart
	svc.agents.mu.Unlock()

	svc.reconcileOnce(ctx)
	if got := runningApps(nodes["node-2"]); len(got) != 1 {
		t.Fatalf("com o node-1 ainda por voltar, o node-2 ficou com %v", got)
	}
	svc.agents.put("node-1", conn)
	svc.reconcileOnce(ctx)
	if a, b := runningApps(nodes["node-1"]), runningApps(nodes["node-2"]); len(a) != 1 || len(b) != 1 {
		t.Errorf("depois de o node-1 voltar: %v / %v -- nada devia ter mudado", a, b)
	}
}

func TestDrainNodeMovesReplicasAway(t *testing.T) {
	ctx := context.Background()
	svc, nodes := twoRemoteNodes(t)
	if err := svc.DrainNode(ctx, "node-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DrainNode(ctx, "node-1"); err == nil {
		t.Error("esvaziar duas vezes ao mesmo tempo devia falhar")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if draining, _ := svc.rt.nodeDrainState("node-1"); !draining {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, msg := svc.rt.nodeDrainState("node-1")
	if msg != "esvaziado: 1 réplica(s) mudada(s)" {
		t.Fatalf("estado do esvaziar = %q", msg)
	}
	time.Sleep(20 * time.Millisecond)
	svc.reconcileOnce(ctx)
	if a, b := runningApps(nodes["node-1"]), runningApps(nodes["node-2"]); len(a) != 0 || len(b) != 2 {
		t.Errorf("depois de esvaziar: node-1 %v, node-2 %v", a, b)
	}
	n, _ := svc.db.GetNode(ctx, "node-1")
	if n.Schedulable {
		t.Error("um nó esvaziado fica em manutenção")
	}
}

// Uma réplica a correr mas fora de qualquer rede nunca serve: substitui-se.
func TestDetachedReplicaIsReplaced(t *testing.T) {
	ctx := context.Background()
	svc, nodes := twoRemoteNodes(t)
	victim := runningApps(nodes["node-1"])[0]
	nodes["node-1"].mu.Lock()
	for id, c := range nodes["node-1"].containers {
		if containerName(c) == victim {
			c.NetworkSettings.Networks = map[string]json.RawMessage{}
			nodes["node-1"].containers[id] = c
		}
	}
	nodes["node-1"].mu.Unlock()
	svc.reconcileOnce(ctx)
	for _, d := range nodes {
		for _, name := range runningApps(d) {
			if name == victim {
				t.Fatalf("a réplica sem rede %s continua", victim)
			}
		}
	}
	if total := len(runningApps(nodes["node-1"])) + len(runningApps(nodes["node-2"])); total != 2 {
		t.Errorf("réplicas depois de substituir: %d, queria 2", total)
	}
}

// Um nó perdido cujas réplicas ainda respondem (o túnel caiu, a máquina
// não) continua a servir enquanto não houver outra réplica pronta.
func TestLostNodeServesUntilReplacementsAreReady(t *testing.T) {
	ctx := context.Background()
	svc, _ := twoRemoteNodes(t)
	svc.reconcileOnce(ctx)
	for _, n := range []string{"node-1", "node-2"} {
		svc.agents.drop(n, nil)
		svc.agents.mu.Lock()
		svc.agents.watch.down[n] = time.Now().Add(-2 * nodeLossGrace)
		svc.agents.mu.Unlock()
	}
	// Sem nós para as substitutas (o local está em manutenção).
	svc.reconcileOnce(ctx)
	cfg, err := svc.buildRouting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP == nil || len(cfg.HTTP.Services) != 1 {
		t.Fatalf("sem substitutas, as réplicas dos nós perdidos deviam continuar no routing: %+v", cfg.HTTP)
	}
	for _, svcCfg := range cfg.HTTP.Services {
		if n := len(svcCfg.LoadBalancer.Servers); n != 2 {
			t.Errorf("servidores = %d, queria as 2 réplicas antigas", n)
		}
	}
}

// Um nó sem sinal cujas réplicas já não respondem morreu mesmo: perde-se
// aos deadAfter, sem esperar pelo nodeLossGrace. Se respondem (só o túnel
// caiu), espera-se.
func TestDeadNodeIsLostEarly(t *testing.T) {
	ctx := context.Background()
	svc, nodes := twoRemoteNodes(t)
	svc.reconcileOnce(ctx)
	victim := runningApps(nodes["node-1"])[0]
	svc.agents.drop("node-1", nil)
	svc.agents.mu.Lock()
	svc.agents.watch.down["node-1"] = time.Now().Add(-deadAfter - time.Second)
	svc.agents.mu.Unlock()

	// As réplicas ainda respondem: continua só "sem sinal".
	svc.reconcileOnce(ctx)
	if got := runningApps(nodes["node-2"]); len(got) != 1 {
		t.Fatalf("com as réplicas a responder, não se substitui ainda: node-2 %v", got)
	}

	// Deixam de responder: perdido já, e a substituta nasce no node-2.
	svc.rt.observeSuspect(victim, false)
	svc.reconcileOnce(ctx)
	if got := runningApps(nodes["node-2"]); len(got) != 2 {
		t.Fatalf("nó morto: node-2 %v, queria 2", got)
	}
	if l, _ := svc.agents.liveness("node-1", time.Now()); l != nodeLost {
		t.Errorf("node-1 devia estar perdido")
	}
}
