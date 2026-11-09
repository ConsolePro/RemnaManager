package stats

import (
	"context"
	"errors"
	"github.com/ConsolePro/RemnaManager/internal/system/network"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"runtime"
	"time"
)

type Snapshot struct {
	CPUs                                int
	CPU                                 float64
	Memory, Available, Swap, Disk, Free uint64
	DiskPercent                         float64
	IPv4, IPv6                          string
	Err                                 error
}

func Collect(ctx context.Context) Snapshot {
	s := Snapshot{CPUs: runtime.NumCPU(), IPv4: "Not connected", IPv6: "Not connected"}
	usage, e := cpu.PercentWithContext(ctx, 150*time.Millisecond, false)
	s.Err = errors.Join(s.Err, e)
	if len(usage) > 0 {
		s.CPU = usage[0]
	}
	memory, e := mem.VirtualMemoryWithContext(ctx)
	s.Err = errors.Join(s.Err, e)
	if memory != nil {
		s.Memory, s.Available = memory.Total, memory.Available
	}
	swap, e := mem.SwapMemoryWithContext(ctx)
	s.Err = errors.Join(s.Err, e)
	if swap != nil {
		s.Swap = swap.Total
	}
	path := "/"
	if runtime.GOOS == "windows" {
		path = `C:\`
	}
	space, e := disk.UsageWithContext(ctx, path)
	s.Err = errors.Join(s.Err, e)
	if space != nil {
		s.Disk, s.Free, s.DiskPercent = space.Total, space.Free, space.UsedPercent
	}
	s.IPv4, s.IPv6, e = network.Addresses()
	s.Err = errors.Join(s.Err, e)
	return s
}

func Cores(n int) string {
	if n%100 >= 11 && n%100 <= 14 {
		return "ядер"
	}
	switch n % 10 {
	case 1:
		return "ядро"
	case 2, 3, 4:
		return "ядра"
	}
	return "ядер"
}
