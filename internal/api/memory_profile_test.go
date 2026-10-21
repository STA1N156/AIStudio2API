package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type memoryUpstream struct{}

func (memoryUpstream) Models(context.Context) ([]aistudio.Model, error) { return nil, nil }
func (memoryUpstream) CountTokens(context.Context, aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	return aistudio.TokenCount{}, nil
}
func (memoryUpstream) Generate(ctx context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	events := make(chan aistudio.Event)
	go func() {
		defer close(events)
		// Keep the input alive until generation finishes, as the actual retry loop does.
		defer runtime.KeepAlive(request)
		for i := range 256 {
			text := fmt.Sprintf("%04d", i) + strings.Repeat("x", 4092)
			select {
			case events <- aistudio.Event{Kind: aistudio.EventText, Text: text}:
			case <-ctx.Done():
				return
			}
		}
		events <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop"}
	}()
	return events, nil
}

type slowMemoryWriter struct {
	header http.Header
	first  time.Time
	bytes  int
	chunks int
}

func (w *slowMemoryWriter) Header() http.Header { return w.header }
func (w *slowMemoryWriter) WriteHeader(int)     {}
func (w *slowMemoryWriter) Flush()              {}
func (w *slowMemoryWriter) Write(data []byte) (int, error) {
	if w.first.IsZero() {
		w.first = time.Now()
	}
	if strings.Contains(string(data), `"content":`) {
		w.chunks++
	}
	w.bytes += len(data)
	time.Sleep(3 * time.Millisecond)
	return len(data), nil
}

func TestMemorySimulation(t *testing.T) {
	if os.Getenv("MEMORY_PROFILE") == "" {
		t.Skip("explicit local memory simulation only")
	}
	runtime.GC()
	var initial runtime.MemStats
	runtime.ReadMemStats(&initial)
	var peak atomic.Uint64
	done := make(chan struct{})
	go func() {
		for {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			for old := peak.Load(); stats.HeapAlloc > old && !peak.CompareAndSwap(old, stats.HeapAlloc); old = peak.Load() {
			}
			select {
			case <-done:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	s := &server{service: memoryUpstream{}, thoughtSignatures: newThoughtSignatureStore()}
	start := make(chan struct{})
	var tasks sync.WaitGroup
	var latency atomic.Int64
	var first atomic.Int64
	var chunks atomic.Int64
	for range 100 {
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			<-start
			body := `{"model":"test","stream":true,"messages":[{"role":"user","content":"` + strings.Repeat("word ", 210000) + `"}]}`
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			writer := &slowMemoryWriter{header: http.Header{}}
			began := time.Now()
			s.handleChatCompletions(writer, req)
			latency.Add(time.Since(began).Microseconds())
			first.Add(writer.first.Sub(began).Microseconds())
			chunks.Add(int64(writer.chunks))
		}()
	}
	began := time.Now()
	close(start)
	tasks.Wait()
	close(done)
	var final runtime.MemStats
	runtime.ReadMemStats(&final)
	if chunks.Load() != 25600 {
		t.Fatalf("lost chunks: %d", chunks.Load())
	}
	if path := os.Getenv("MEMORY_PROFILE_OUTPUT"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.WriteHeapProfile(file); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
	}
	t.Logf("MEMORY_RESULT concurrent=100 chunks=%d peak_heap_mib=%.2f allocated_mib=%.2f first_ms=%.2f avg_total_ms=%.2f wall_ms=%d", chunks.Load(), float64(peak.Load()-initial.HeapAlloc)/(1<<20), float64(final.TotalAlloc-initial.TotalAlloc)/(1<<20), float64(first.Load())/100000, float64(latency.Load())/100000, time.Since(began).Milliseconds())
}
