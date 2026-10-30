package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// A rede entre os nós de um cluster (swarm + overlay cifrada) usa portas
// do próprio Docker da máquina -- que, ao contrário das portas publicadas
// pelos containers, a ufw bloqueia. Só se abrem para a rede local desta
// máquina.

type firewallRole string

const (
	rolePlatform firewallRole = "plataforma"
	roleNode     firewallRole = "nó"
)

// firewallPorts: o que cada papel precisa de receber dos outros.
func firewallPorts(role firewallRole) []string {
	ports := []string{"7946/tcp+udp (swarm: membros)", "4789/udp (overlay: dados)", "ESP (overlay cifrada, IPsec)"}
	if role == rolePlatform {
		ports = append([]string{"2377/tcp (swarm: os nós chegam ao manager)"}, ports...)
	}
	return ports
}

func ufwScript(role firewallRole, cidr string) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	if role == rolePlatform {
		fmt.Fprintf(&b, "ufw allow from %s to any port 2377 proto tcp comment 'basestack swarm'\n", cidr)
	}
	fmt.Fprintf(&b, "ufw allow from %s to any port 7946 comment 'basestack swarm'\n", cidr)
	fmt.Fprintf(&b, "ufw allow from %s to any port 4789 proto udp comment 'basestack overlay'\n", cidr)
	fmt.Fprintf(&b, "ufw allow from %s proto esp comment 'basestack overlay cifrada'\n", cidr)
	return b.String()
}

// ufwActive: a ufw está instalada e ligada (sem precisar de root).
func ufwActive() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := exec.LookPath("ufw"); err != nil {
		return false
	}
	out, _ := exec.Command("systemctl", "is-active", "ufw").Output()
	return strings.TrimSpace(string(out)) == "active"
}

// lanCIDR é a rede local desta máquina (ex: 192.168.1.0/24): a da
// interface com o lanIP.
func lanCIDR() (string, error) {
	ip := net.ParseIP(lanIP())
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
				return (&net.IPNet{IP: n.IP.Mask(n.Mask), Mask: n.Mask}).String(), nil
			}
		}
	}
	return "", fmt.Errorf("não encontrei a rede local de %s", ip)
}

// offerFirewall mostra o que é preciso e, com a ufw ativa, aplica-o --
// como root logo, num terminal depois de perguntar, sem terminal nunca.
// Devolve true se ficou aplicado.
func offerFirewall(role firewallRole) (bool, error) {
	cidr, err := lanCIDR()
	if err != nil {
		return false, err
	}
	if !ufwActive() {
		fmt.Printf("Sem ufw ativa nesta máquina. Se houver outra firewall entre os nós, abre para %s:\n", cidr)
		for _, p := range firewallPorts(role) {
			fmt.Println("    " + p)
		}
		return false, nil
	}
	script := ufwScript(role, cidr)
	fmt.Printf("\nA ufw está ativa: a rede do cluster precisa destas regras (só para %s):\n\n%s\n", cidr, indent(strings.TrimPrefix(script, "set -e\n")))
	var cmd *exec.Cmd
	switch {
	case os.Geteuid() == 0:
		cmd = exec.Command("sh", "-c", script)
	case isTerminal(os.Stdin):
		fmt.Print("Aplicar agora com sudo? [s/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "s" && a != "sim" && a != "y" && a != "yes" {
			fmt.Println("Ficou por fazer: sem estas regras, os nós não entram no swarm. Mais tarde: basestack firewall")
			return false, nil
		}
		cmd = exec.Command("sudo", "sh", "-c", script)
	default:
		fmt.Println("Ficou por fazer (sem terminal para pedir o sudo): basestack firewall")
		return false, nil
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("aplicando as regras da firewall: %w", err)
	}
	return true, nil
}

// cmdFirewall: as regras para o papel desta máquina, e aplicá-las.
func cmdFirewall(args []string) error {
	if s, dir, err := loadSettings(); err == nil {
		if !s.Cluster {
			fmt.Println("A plataforma desta máquina não está em modo cluster: não há rede entre nós a abrir.")
			return nil
		}
		applied, err := offerFirewall(rolePlatform)
		if err != nil || !applied {
			return err
		}
		s.FirewallApplied = true
		return saveSettings(dir, s)
	} else if !errors.Is(err, errNotInstalled) {
		return err
	}
	n, dir, err := loadNode()
	if err != nil {
		return errors.New("esta máquina não tem a plataforma nem é um nó -- nada a abrir")
	}
	applied, err := offerFirewall(roleNode)
	if err != nil || !applied {
		return err
	}
	n.Firewall = true
	return saveNode(dir, n)
}

func saveNode(dir string, n nodeFile) error {
	raw, _ := json.MarshalIndent(n, "", "  ")
	return writeFile(filepath.Join(dir, "node.json"), raw, 0o600)
}
