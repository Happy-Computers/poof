package mount

import (
	"log"
	"runtime"
	"sync"
	"time"
)

func startMetrics(interval time.Duration) func() {
	if interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				log.Printf(
					"metrics heap_alloc_bytes=%d heap_sys_bytes=%d gc_cycles=%d goroutines=%d",
					memory.HeapAlloc,
					memory.HeapSys,
					memory.NumGC,
					runtime.NumGoroutine(),
				)
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(done)
		})
	}
}
