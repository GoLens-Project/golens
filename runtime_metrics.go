package golens

import (
	"context"
	"runtime"
	"syscall"
	"time"
)

const (
	MetricGoAllocBytes    = "go_memstats_alloc_bytes"
	MetricGoSysBytes      = "go_memstats_sys_bytes"
	MetricGoHeapInuse     = "go_memstats_heap_inuse_bytes"
	MetricGoHeapObjects   = "go_memstats_heap_objects"
	MetricGoGoroutines    = "go_goroutines"
	MetricCPUUsagePercent = "cpu_usage_percent"
)

// startRuntimeMetrics launches a background goroutine that periodically
// collects Go runtime stats and records them as gauge metrics.
// The goroutine exits when ctx is cancelled.
func startRuntimeMetrics(ctx context.Context, r *Registry, interval time.Duration) {
	r.Register(MetricGoAllocBytes, GaugeType, "currently allocated heap bytes", nil, nil, 0, 0)
	r.Register(MetricGoSysBytes, GaugeType, "bytes obtained from the OS", nil, nil, 0, 0)
	r.Register(MetricGoHeapInuse, GaugeType, "bytes in active heap spans", nil, nil, 0, 0)
	r.Register(MetricGoHeapObjects, GaugeType, "total number of allocated objects", nil, nil, 0, 0)
	r.Register(MetricGoGoroutines, GaugeType, "application goroutines (excludes runtime)", nil, nil, 0, 0)
	r.Register(MetricCPUUsagePercent, GaugeType, "process CPU usage percentage", nil, nil, 0, 100)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		prevCPU := cpuTime()
		prevWall := time.Now()
		collectRuntime(r)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				curCPU := cpuTime()
				wallDelta := now.Sub(prevWall).Seconds()
				if wallDelta > 0 {
					cpuDelta := curCPU - prevCPU
					pct := (cpuDelta / wallDelta) * 100
					if pct < 0 {
						pct = 0
					}
					if pct > 100*float64(runtime.NumCPU()) {
						pct = 100 * float64(runtime.NumCPU())
					}
					r.Record(MetricCPUUsagePercent, pct)
				}
				prevCPU = curCPU
				prevWall = now
				collectRuntime(r)
			}
		}
	}()
}

func collectRuntime(r *Registry) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	r.Record(MetricGoAllocBytes, float64(ms.Alloc))
	r.Record(MetricGoSysBytes, float64(ms.Sys))
	r.Record(MetricGoHeapInuse, float64(ms.HeapInuse))
	r.Record(MetricGoHeapObjects, float64(ms.HeapObjects))

	// Show only application goroutines (total - baseline - continuous background goroutines)
	// We subtract 2 to account for the main loop and runtime metrics collector goroutines
	totalGoroutines := runtime.NumGoroutine()
	appGoroutines := totalGoroutines - r.GoroutineBaseline - 2
	if appGoroutines < 0 {
		appGoroutines = 0 // Shouldn't happen, but safeguard
	}
	r.Record(MetricGoGoroutines, float64(appGoroutines))
}

// cpuTime returns the total CPU time (user + system) in seconds for the
// current process.
func cpuTime() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	user := float64(ru.Utime.Sec) + float64(ru.Utime.Usec)/1e6
	sys := float64(ru.Stime.Sec) + float64(ru.Stime.Usec)/1e6
	return user + sys
}
