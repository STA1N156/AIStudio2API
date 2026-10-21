package aistudio

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Run explicitly: OUTPUT_COPY_PROFILE=1 go test -run '^TestOutputCopyProfile$' -v ./internal/aistudio
func TestOutputCopyProfile(t *testing.T) {
	if os.Getenv("OUTPUT_COPY_PROFILE") == "" {
		t.Skip("explicit local output accumulation comparison only")
	}
	const workers, chunks = 100, 256
	chunk := strings.Repeat("0123456789abcdef", 64)
	outputs := make([]generatedOutputParts, workers)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	peak.Store(before.HeapAlloc)
	stop, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			if stats.HeapAlloc > peak.Load() {
				peak.Store(stats.HeapAlloc)
			}
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	start := make(chan struct{})
	var tasks sync.WaitGroup
	for i := range workers {
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			<-start
			output := &outputs[i]
			prefix := fmt.Sprintf("%03d:", i)
			output.observe(Event{Kind: EventText, Text: prefix})
			output.observe(Event{Kind: EventReasoning, Text: prefix})
			for range chunks {
				output.observe(Event{Kind: EventText, Text: chunk})
				output.observe(Event{Kind: EventReasoning, Text: chunk})
			}
		}()
	}
	started := time.Now()
	close(start)
	tasks.Wait()
	elapsed := time.Since(started)
	close(stop)
	<-sampled
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	if after.HeapAlloc > peak.Load() {
		peak.Store(after.HeapAlloc)
	}
	runtime.GC()
	var retained runtime.MemStats
	runtime.ReadMemStats(&retained)
	for i, output := range outputs {
		want := fmt.Sprintf("%03d:", i) + strings.Repeat(chunk, chunks)
		if len(output.visible) != 1 || len(output.reasoning) != 1 ||
			output.visible[0].Text != want || output.reasoning[0].Text != want {
			t.Fatalf("output changed for request %d", i)
		}
	}
	runtime.KeepAlive(outputs)
	t.Logf("workers=%d chunks_per_channel=%d chunk_bytes=%d payload_bytes=%d elapsed_ms=%.2f allocated_mib=%.2f peak_heap_mib=%.2f retained_heap_mib=%.2f output=identical",
		workers, chunks, len(chunk), workers*2*(4+chunks*len(chunk)), float64(elapsed)/float64(time.Millisecond),
		float64(allocated)/(1<<20), float64(peak.Load()-before.HeapAlloc)/(1<<20), float64(retained.HeapAlloc-before.HeapAlloc)/(1<<20))
}
