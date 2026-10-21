package waa

import (
	"context"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestMemorySimulation(t *testing.T) {
	if os.Getenv("MEMORY_PROFILE") == "" {
		t.Skip("explicit local memory simulation only")
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	instances := make([]*Runtime, 20)
	var group sync.WaitGroup
	for i := range instances {
		group.Add(1)
		go func() {
			defer group.Done()
			value, err := NewRuntime(context.Background(), Options{
				Challenge:   Challenge{GlobalName: "BotGuard", Program: "test", InterpreterHash: "memory-simulation"},
				Interpreter: `var BotGuard = {a: function(program, ready) { ready(function(callback) { callback("!test"); }, function() {}); }};`,
				Profile:     Profile{PageURL: "https://aistudio.google.com/", Locale: "en-US", TimeZone: "UTC"},
			})
			if err != nil {
				t.Error(err)
				return
			}
			instances[i] = value
		}()
	}
	group.Wait()
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	peak := after.HeapAlloc - before.HeapAlloc
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("MEMORY_RESULT runtimes=20 heap_mib=%.2f retained_mib=%.2f allocated_mib=%.2f init_ms=%d", float64(peak)/(1<<20), float64(after.HeapAlloc-before.HeapAlloc)/(1<<20), float64(allocated)/(1<<20), elapsed.Milliseconds())
	for _, instance := range instances {
		if instance != nil {
			instance.Close()
		}
	}
}
