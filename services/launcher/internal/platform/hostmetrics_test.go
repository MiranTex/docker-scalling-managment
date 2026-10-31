package platform

import (
	"math"
	"strings"
	"testing"
	"time"
)

const sampleOut = `cpu  1000 0 500 8000 500 0 0 0 0 0
MemTotal:        8000000 kB
MemAvailable:    2000000 kB
0.52 0.58 0.59 1/345 1234
overlay              100000000  60000000  40000000  60% /host/disk
`

func TestParseHostMetrics(t *testing.T) {
	s, cpu, err := parseHostMetrics(sampleOut)
	if err != nil {
		t.Fatal(err)
	}
	if cpu.total != 10000 || cpu.idle != 8500 {
		t.Errorf("cpu = %+v", cpu)
	}
	if s.MemoryTotalBytes != 8000000<<10 || s.MemoryAvailBytes != 2000000<<10 || s.Load1 != 0.52 || s.Load15 != 0.59 ||
		s.DiskTotalBytes != 100000000<<10 || s.DiskAvailableBytes != 40000000<<10 {
		t.Errorf("amostra = %+v", s)
	}
	if _, _, err := parseHostMetrics("lixo"); err == nil {
		t.Error("output incompleto aceite")
	}
}

// O CPU % sai da diferença entre duas leituras; a primeira só serve de base.
func TestHostMetricsCPUFromDeltas(t *testing.T) {
	h := newHostMetrics()
	h.add("n1", HostSample{At: time.Now()}, cpuTimes{total: 1000, idle: 900})
	if h.latest("n1") != nil {
		t.Fatal("a primeira leitura não devia dar amostra")
	}
	// +1000 de tempo, +250 parado: 75% ocupado.
	h.add("n1", HostSample{At: time.Now()}, cpuTimes{total: 2000, idle: 1150})
	got := h.latest("n1")
	if got == nil || math.Abs(got.CPUPercent-75) > 0.01 {
		t.Fatalf("CPU = %+v", got)
	}
	// Um reboot (contadores a zero) não dá uma amostra absurda.
	h.add("n1", HostSample{At: time.Now()}, cpuTimes{total: 50, idle: 40})
	if n := len(h.since("n1", time.Time{})); n != 1 {
		t.Errorf("amostras depois do reboot = %d", n)
	}
	// Uma amostra antiga já não conta como atual.
	h.series["n2"] = []HostSample{{At: time.Now().Add(-time.Hour)}}
	if h.latest("n2") != nil {
		t.Error("amostra de há uma hora tratada como atual")
	}
	if !strings.Contains(sampleOut, "/host/disk") {
		t.Fatal()
	}
}
