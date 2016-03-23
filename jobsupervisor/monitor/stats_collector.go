// +build windows

package monitor

import (
	"sync"
	"time"

	"github.com/cloudfoundry/bosh-agent/platform/stats"
)

type collector struct {
	m    *Monitor
	cond *sync.Cond
}

func NewStatsCollector() stats.Collector {
	return &collector{cond: sync.NewCond(new(sync.Mutex))}
}

func (c *collector) StartCollecting(freq time.Duration, updateSema chan struct{}) {
	if c.m == nil {
		c.m, _ = condMonitor(freq, c.cond)
	}
	for {
		c.cond.L.Lock()
		c.cond.Wait()
		c.cond.L.Unlock()
		updateSema <- struct{}{}
	}
}

// Not implemented on Windows.
func (c *collector) GetCPULoad() (load stats.CPULoad, err error) { return }

func (c *collector) GetCPUStats() (stats.CPUStats, error) {
	const mult float64 = 100000
	cpu, err := c.m.CPU()
	load := stats.CPUStats{
		User:  uint64(cpu.User * mult),
		Sys:   uint64(cpu.Kernel * mult),
		Total: uint64((cpu.User + cpu.Kernel) * mult),
	}
	return load, err
}

func (c *collector) GetMemStats() (stats.Usage, error) {
	mem, err := SystemMemStats()
	usage := stats.Usage{
		Total: mem.Total.Uint64(),
		Used:  mem.Total.Uint64() - mem.Avail.Uint64(),
	}
	return usage, err
}

// Not implemented on Windows.
func (c *collector) GetSwapStats() (usage stats.Usage, err error) { return }

func (c *collector) GetDiskStats(path string) (stats.DiskStats, error) {
	// TODO (CEV): Implement InodeUsage
	u, err := UsedDiskSpace(path)
	d := stats.DiskStats{
		DiskUsage: stats.Usage{
			Used:  u.Used.Uint64(),
			Total: u.Total.Uint64(),
		},
	}
	return d, err
}