package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/waa"
)

type sample struct {
	QueueMS float64 `json:"queue_ms"`
	ProofMS float64 `json:"proof_ms"`
	FirstMS float64 `json:"first_ms"`
	TotalMS float64 `json:"total_ms"`
}

func milliseconds(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func distribution(values []float64) map[string]float64 {
	sort.Float64s(values)
	if len(values) == 0 {
		return nil
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return map[string]float64{"mean": sum / float64(len(values)), "p50": values[len(values)/2], "p95": values[(len(values)-1)*95/100], "p99": values[(len(values)-1)*99/100], "max": values[len(values)-1]}
}

func main() {
	rpm := flag.Int("rpm", 1000, "offered requests per minute")
	count := flag.Int("requests", 1000, "number of requests")
	warmCount := flag.Int("warm", 600, "warmed account runtimes")
	output := flag.String("output", "benchmark.json", "result file")
	label := flag.String("label", "", "build identifier")
	flag.Parse()
	runtime.GOMAXPROCS(8)
	debug.SetGCPercent(50)
	ctx := context.Background()
	models := make([]aistudio.Model, 40)
	for i := range models {
		models[i] = aistudio.Model{ID: fmt.Sprintf("model-%02d", i), Methods: []string{"generateContent"}}
	}
	accounts := make([]*aistudio.Account, 5000)
	for i := range accounts {
		id := fmt.Sprintf("account-%04d", i)
		accounts[i] = &aistudio.Account{ID: id, Config: aistudio.DefaultAccountConfig(id + "@example.invalid"), State: aistudio.AccountReady, Models: models, BenefitTier: aistudio.BenefitTierPro}
	}
	pool := aistudio.NewAccountPool(accounts, 5)
	pool.SetUpstreamChannels([]aistudio.Channel{aistudio.ChannelPlayground})
	warm := make([]string, *warmCount)
	vms := make(map[string]*waa.Runtime, *warmCount)
	startup := time.Now()
	const interpreter = `var BotGuard={a:function(program,ready){var state=new Uint8Array(32768);ready(function(done){var n=0;for(var i=0;i<20000;i++){n=(n+i*(i%17+1))|0;state[i%state.length]=n&255;}done("!"+n+":"+document.querySelector("textarea").value.length);},function(){});}};`
	for i := range warm {
		warm[i] = accounts[i].ID
		vm, err := waa.NewRuntime(ctx, waa.Options{Challenge: waa.Challenge{GlobalName: "BotGuard", Program: "local-benchmark", InterpreterHash: "local-benchmark"}, Interpreter: interpreter, Profile: waa.Profile{PageURL: "https://aistudio.google.com/", Locale: "en-US", TimeZone: "UTC"}})
		if err != nil {
			panic(err)
		}
		vms[warm[i]] = vm
	}
	startupMS := milliseconds(time.Since(startup))
	runtime.GC()
	var initial runtime.MemStats
	runtime.ReadMemStats(&initial)
	var peakHeap, peakRSS, peakGoroutines, active, peakActive, failures atomic.Int64
	peakHeap.Store(int64(initial.HeapAlloc))
	stop := make(chan struct{})
	var monitor sync.WaitGroup
	monitor.Add(1)
	go func() {
		defer monitor.Done()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				peakHeap.Store(max(peakHeap.Load(), int64(stats.HeapAlloc)))
				peakGoroutines.Store(max(peakGoroutines.Load(), int64(runtime.NumGoroutine())))
				if data, err := os.ReadFile("/proc/self/status"); err == nil {
					for _, line := range strings.Split(string(data), "\n") {
						var n int64
						if _, err := fmt.Sscanf(line, "VmRSS: %d kB", &n); err == nil {
							peakRSS.Store(max(peakRSS.Load(), n*1024))
						}
					}
				}
			}
		}
	}()
	var mu sync.Mutex
	var samples []sample
	var queries []float64
	var queryWorkers sync.WaitGroup
	for i := 0; i < 4; i++ {
		queryWorkers.Add(1)
		go func() {
			defer queryWorkers.Done()
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					start := time.Now()
					view := pool.Snapshot()
					view.Status()
					view.CooldownAccountEligibility([]string{"model-35", "model-36", "model-37", "model-38", "model-39"})
					elapsed := milliseconds(time.Since(start))
					mu.Lock()
					queries = append(queries, elapsed)
					mu.Unlock()
				}
			}
		}()
	}
	prompt := strings.Repeat("fixed local input ", 1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		current := active.Add(1)
		defer active.Add(-1)
		peakActive.Store(max(peakActive.Load(), current))
		selection := aistudio.AccountSelection{ModelID: "model-39", Method: "generateContent"}
		var lease *aistudio.AccountLease
		for lease == nil {
			groups, err := pool.ClassifyCandidates(r.Context(), selection, warm)
			if err != nil {
				failures.Add(1)
				http.Error(w, err.Error(), 500)
				return
			}
			candidates := append(groups.WarmReady, groups.WarmAvailable...)
			if filter, ok := any(pool).(interface{ WithoutPendingProofs([]string) []string }); ok {
				if free := filter.WithoutPendingProofs(candidates); len(free) > 0 {
					candidates = free
				}
			}
			selection.AllowedAccountIDs = candidates
			lease, _, err = pool.TryAcquireFor(r.Context(), selection)
			if err != nil {
				failures.Add(1)
				http.Error(w, err.Error(), 500)
				return
			}
			if lease == nil {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(time.Millisecond):
				}
			}
		}
		defer lease.Release()
		picked := time.Now()
		_, err := vms[lease.Account().ID].Proof(r.Context(), "fixed-digest", prompt)
		proved := time.Now()
		if done, ok := any(lease).(interface{ ProofDone() }); ok {
			done.ProofDone()
		}
		if err != nil {
			failures.Add(1)
			http.Error(w, err.Error(), 500)
			return
		}
		time.Sleep(250 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		first := time.Now()
		for i := 0; i < 35; i++ {
			time.Sleep(50 * time.Millisecond)
			fmt.Fprint(w, "data: token\n\n")
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		mu.Lock()
		samples = append(samples, sample{milliseconds(picked.Sub(start)), milliseconds(proved.Sub(picked)), milliseconds(first.Sub(start)), milliseconds(time.Since(start))})
		mu.Unlock()
	}))
	client := &http.Client{Transport: &http.Transport{MaxIdleConns: 256, MaxIdleConnsPerHost: 256}, Timeout: 120 * time.Second}
	loadStart := time.Now()
	var requests sync.WaitGroup
	for i := 0; i < *count; i++ {
		target := loadStart.Add(time.Duration(i) * time.Minute / time.Duration(*rpm))
		if delay := time.Until(target); delay > 0 {
			time.Sleep(delay)
		}
		requests.Add(1)
		go func() {
			defer requests.Done()
			resp, err := client.Get(server.URL)
			if err != nil {
				failures.Add(1)
				return
			}
			defer resp.Body.Close()
			var buffer [4096]byte
			for {
				if _, err := resp.Body.Read(buffer[:]); err != nil {
					break
				}
			}
		}()
	}
	requests.Wait()
	elapsed := time.Since(loadStart)
	close(stop)
	monitor.Wait()
	queryWorkers.Wait()
	server.Close()
	client.CloseIdleConnections()
	var final runtime.MemStats
	runtime.ReadMemStats(&final)
	metric := func(get func(sample) float64) map[string]float64 {
		values := make([]float64, len(samples))
		for i, s := range samples {
			values[i] = get(s)
		}
		return distribution(values)
	}
	result := map[string]any{"label": *label, "accounts": 5000, "warm_runtimes": *warmCount, "offered_rpm": *rpm, "offered_requests": *count, "completed": len(samples), "failures": failures.Load(), "duration_seconds": elapsed.Seconds(), "throughput_rpm": float64(len(samples)) * 60 / elapsed.Seconds(), "warm_startup_ms": startupMS, "initial_heap_mib": float64(initial.HeapAlloc) / 1048576, "peak_heap_mib": float64(peakHeap.Load()) / 1048576, "peak_rss_mib": float64(peakRSS.Load()) / 1048576, "peak_goroutines": peakGoroutines.Load(), "peak_inflight": peakActive.Load(), "gc_count": final.NumGC - initial.NumGC, "gc_pause_ms": milliseconds(time.Duration(final.PauseTotalNs - initial.PauseTotalNs)), "queue_ms": metric(func(s sample) float64 { return s.QueueMS }), "proof_ms": metric(func(s sample) float64 { return s.ProofMS }), "first_output_ms": metric(func(s sample) float64 { return s.FirstMS }), "total_ms": metric(func(s sample) float64 { return s.TotalMS }), "dashboard_query_ms": distribution(queries), "go": runtime.Version(), "gomaxprocs": runtime.GOMAXPROCS(0), "synthetic_upstream": "250ms first byte plus 35x50ms output; no external requests", "samples": samples}
	data, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(*output, data, 0600); err != nil {
		panic(err)
	}
	delete(result, "samples")
	data, _ = json.Marshal(result)
	fmt.Println(string(data))
	for _, vm := range vms {
		vm.Close()
	}
}
