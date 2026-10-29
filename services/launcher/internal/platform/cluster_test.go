package platform

import (
	"context"
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
