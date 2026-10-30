package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

func TestScheduleHonoursNodeLabels(t *testing.T) {
	cands := []candidate{
		{Node: store.Node{Name: "grande", OS: "linux", Arch: "amd64", Schedulable: true}, Online: true, Network: "cluster", Capacity: 65536},
		{Node: store.Node{Name: "gpu", OS: "linux", Arch: "amd64", Schedulable: true, AutoLabels: map[string]string{"gpu": "nvidia"}},
			Online: true, Network: "cluster", Capacity: 8192},
		{Node: store.Node{Name: "ssd", OS: "linux", Arch: "amd64", Schedulable: true, Labels: map[string]string{"disco": "ssd"}},
			Online: true, Network: "cluster", Capacity: 4096},
	}
	for _, c := range []struct {
		selector map[string]string
		want     string
	}{
		{nil, "grande"},
		{map[string]string{"gpu": "nvidia"}, "gpu"},
		{map[string]string{"disco": "ssd"}, "ssd"},
	} {
		if got, err := schedule(cands, placement{MemoryMB: 512, Selector: c.selector}); err != nil || got != c.want {
			t.Errorf("selector %v: %q, %v -- queria %q", c.selector, got, err, c.want)
		}
	}
	_, err := schedule(cands, placement{MemoryMB: 512, Selector: map[string]string{"gpu": "nvidia", "disco": "ssd"}})
	if !errors.Is(err, ErrNoNode) || !strings.Contains(err.Error(), "gpu: sem disco=ssd") {
		t.Errorf("sem nó com as duas etiquetas: %v", err)
	}

	// As do admin ganham às detetadas.
	n := store.Node{AutoLabels: map[string]string{"gpu": "nvidia"}, Labels: map[string]string{"gpu": "nenhuma"}}
	if n.EffectiveLabels()["gpu"] != "nenhuma" {
		t.Errorf("EffectiveLabels = %v", n.EffectiveLabels())
	}
}

func TestDetectLabels(t *testing.T) {
	var info dockerclient.SystemInfo
	_ = json.Unmarshal([]byte(`{"Runtimes":{"runc":{"path":"runc"},"nvidia":{"path":"nvidia-container-runtime"}}}`), &info)
	if got := detectLabels(&info); got["gpu"] != "nvidia" {
		t.Errorf("com runtime nvidia: %v", got)
	}
	if got := detectLabels(&dockerclient.SystemInfo{}); len(got) != 0 {
		t.Errorf("sem runtime nvidia: %v", got)
	}
}

func TestNodeLabelsAndServiceSelector(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newDeployService(t)
	if _, err := svc.SetNodeLabels(ctx, LocalNode, map[string]string{"Disco": "ssd"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("chave com maiúsculas: %v", err)
	}
	if _, err := svc.SetNodeLabels(ctx, "nao-existe", map[string]string{"disco": "ssd"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("nó desconhecido: %v", err)
	}
	if _, err := svc.CreateService(ctx, "loja", "prod", ServiceInput{Slug: "web", Kind: "web", Port: intp(80)}, "t"); err != nil {
		t.Fatal(err)
	}
	gpu := map[string]string{"gpu": "nvidia"}
	if _, err := svc.UpdateService(ctx, "loja", "prod", "web", ServiceInput{NodeSelector: &gpu}); err != nil {
		t.Fatal(err)
	}
	// Nenhum nó tem gpu=nvidia: o deploy não consegue pôr a réplica.
	dep, err := svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	final := waitDeploy(t, svc, "web", dep.ID)
	if final.Status != store.DeployFailed || !strings.Contains(final.Error, "sem gpu=nvidia") {
		t.Fatalf("deploy sem nó com gpu = %s: %s", final.Status, final.Error)
	}
	// O nó local passa a ter a etiqueta: o deploy seguinte entra.
	view, err := svc.SetNodeLabels(ctx, LocalNode, gpu)
	if err != nil || view.Labels["gpu"] != "nvidia" {
		t.Fatalf("SetNodeLabels = %+v, %v", view.Labels, err)
	}
	dep, _ = svc.Deploy(ctx, "loja", "prod", "web", DeployInput{Image: "app:1"}, "t")
	waitDeploy(t, svc, "web", dep.ID)
	s, _ := svc.GetService(ctx, "loja", "prod", "web")
	if s.CurrentRevision == nil || s.CurrentRevision.NodeSelector["gpu"] != "nvidia" {
		t.Errorf("revisão sem o selector: %+v", s.CurrentRevision)
	}
}
