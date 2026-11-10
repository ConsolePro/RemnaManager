package stats

import (
	"context"
	"testing"
	"time"
)

func TestCores(t *testing.T) {
	for n, want := range map[int]string{1: "ядро", 2: "ядра", 4: "ядра", 5: "ядер", 11: "ядер", 14: "ядер", 21: "ядро", 22: "ядра", 111: "ядер"} {
		if Cores(n) != want {
			t.Fatal(n, Cores(n))
		}
	}
}
func TestRealSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s := Collect(ctx)
	if s.CPUs < 1 || s.CPU < 0 || s.CPU > 100 || s.Available > s.Memory || s.Free > s.Disk || s.DiskPercent < 0 || s.DiskPercent > 100 {
		t.Fatal(s)
	}
	if s.IPv4 == "" || s.IPv6 == "" {
		t.Fatal(s)
	}
	if s.Uptime == 0 || s.DiskUsed > s.Disk {
		t.Fatal("invalid host uptime or disk usage", s)
	}
}
