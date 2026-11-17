package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func distribution(values []float64) map[string]float64 {
	if len(values) == 0 {
		return nil
	}
	slices.Sort(values)
	return map[string]float64{"p50": values[len(values)/2], "p95": values[(len(values)-1)*95/100], "max": values[len(values)-1]}
}

func milliseconds(value time.Duration) float64 { return float64(value) / float64(time.Millisecond) }

func run(shared bool, count, rpm int) map[string]any {
	accounts := make([]*aistudio.Account, 5000)
	warm := make([]string, 600)
	for i := range accounts {
		id := fmt.Sprintf("account-%04d", i)
		accounts[i] = &aistudio.Account{ID: id, Config: aistudio.DefaultAccountConfig(id + "@example.invalid"), State: aistudio.AccountReady, BenefitTier: aistudio.BenefitTierPro}
		if i < len(warm) {
			warm[i] = id
		}
	}
	accounts[0].Models = []aistudio.Model{{ID: "flash", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}}
	pool := aistudio.NewAccountPool(accounts, 5)
	pool.SetUpstreamChannels([]aistudio.Channel{aistudio.ChannelPlayground})
	if shared {
		aistudio.NewPooledService(pool, aistudio.NewClient(aistudio.ClientOptions{})).UseCatalogAccount(accounts[0].ID)
	}
	selection := aistudio.AccountSelection{ModelID: "flash", Method: "generateContent", AllowedAccountIDs: warm}
	groups, err := pool.ClassifyCandidates(context.Background(), selection, warm)
	if err != nil {
		panic(err)
	}
	eligible := len(groups.WarmReady)
	runtime.GC()
	var startMemory runtime.MemStats
	runtime.ReadMemStats(&startMemory)
	var waiting, active, peakWaiting, peakActive, peakHeap, failures atomic.Int64
	peakHeap.Store(int64(startMemory.HeapAlloc))
	var mu sync.Mutex
	var waits, firsts, totals, snapshots []float64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		sample := 0
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				peakWaiting.Store(max(peakWaiting.Load(), waiting.Load()))
				peakActive.Store(max(peakActive.Load(), active.Load()))
				sample++
				if sample%50 == 0 {
					started := time.Now()
					pool.Snapshot().Status()
					mu.Lock()
					snapshots = append(snapshots, milliseconds(time.Since(started)))
					mu.Unlock()
					var memory runtime.MemStats
					runtime.ReadMemStats(&memory)
					peakHeap.Store(max(peakHeap.Load(), int64(memory.HeapAlloc)))
				}
			}
		}
	}()
	started := time.Now()
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		if delay := time.Until(started.Add(time.Duration(i) * time.Minute / time.Duration(rpm))); delay > 0 {
			time.Sleep(delay)
		}
		workers.Go(func() {
			requestStart := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			waiting.Add(1)
			lease, err := pool.AcquireFor(ctx, selection)
			waiting.Add(-1)
			cancel()
			if err != nil {
				failures.Add(1)
				return
			}
			acquired := time.Now()
			active.Add(1)
			time.Sleep(250 * time.Millisecond)
			first := time.Since(requestStart)
			time.Sleep(1750 * time.Millisecond)
			active.Add(-1)
			if err := lease.Release(); err != nil {
				panic(err)
			}
			mu.Lock()
			waits = append(waits, milliseconds(acquired.Sub(requestStart)))
			firsts = append(firsts, milliseconds(first))
			totals = append(totals, milliseconds(time.Since(requestStart)))
			mu.Unlock()
		})
	}
	workers.Wait()
	close(stop)
	<-done
	return map[string]any{
		"shared_catalog": shared, "accounts": len(accounts), "warm_accounts": len(warm), "eligible_warm_accounts": eligible,
		"rpm": rpm, "submitted": count, "completed": len(totals), "queue_timeout": failures.Load(),
		"peak_queued": peakWaiting.Load(), "peak_running": peakActive.Load(), "queue_wait_ms": distribution(waits),
		"first_output_ms": distribution(firsts), "total_ms": distribution(totals), "account_snapshot_ms": distribution(snapshots),
		"initial_heap_mb": float64(startMemory.HeapAlloc) / (1 << 20), "peak_heap_mb": float64(peakHeap.Load()) / (1 << 20),
		"elapsed_seconds": time.Since(started).Seconds(),
	}
}

func main() {
	count := flag.Int("requests", 300, "requests per scenario")
	rpm := flag.Int("rpm", 1000, "offered requests per minute")
	flag.Parse()
	result := map[string]any{"kind": "local scheduling simulation; fixed 250ms first output and 2s lifetime; 4s queue timeout; no upstream network", "before": run(false, *count, *rpm), "after": run(true, *count, *rpm)}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		panic(err)
	}
}
