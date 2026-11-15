package aistudio

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

func cooldownLoadPool() *AccountPool {
	models := make([]Model, 40)
	for i := range models {
		models[i] = Model{ID: fmt.Sprintf("model-%d", i), Methods: []string{"generateContent"}}
	}
	accounts := make([]*Account, 5738)
	for i := range accounts {
		accounts[i] = &Account{ID: fmt.Sprint(i), Config: DefaultAccountConfig("load@example.com"),
			State: AccountReady, Models: models, BenefitTier: BenefitTierPro}
	}
	p := NewAccountPool(accounts, 5)
	for i, account := range p.accounts {
		account.runtime.CatalogFingerprint = "load"
		if i%8 == 0 {
			account.runtime.Cooldowns["model-39"] = CooldownState{Until: time.Now().Add(time.Hour)}
		}
	}
	return p
}

// Mix four dashboard readers with sixteen account selectors, without network or disk I/O.
func BenchmarkCooldownContention(b *testing.B) {
	benchmarkCooldownContention(b, func(p *AccountPool) {
		p.Status()
		p.CooldownAccountEligibility([]string{"model-35", "model-36", "model-37", "model-38", "model-39"})
	})
}

func benchmarkCooldownContention(b *testing.B, query func(*AccountPool)) {
	p := cooldownLoadPool()
	warm := make([]string, 600)
	for i := range warm {
		warm[i] = p.accounts[i].ID
	}
	var mu sync.Mutex
	var selections, queries []int64
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		var wg sync.WaitGroup
		for reader := 0; reader < 20; reader++ {
			wg.Add(1)
			go func(isQuery bool) {
				defer wg.Done()
				count := 80
				if isQuery {
					count = 8
				}
				for i := 0; i < count; i++ {
					start := time.Now()
					if isQuery {
						query(p)
					} else {
						if _, err := p.ClassifyCandidates(context.Background(), AccountSelection{ModelID: "model-39", Method: "generateContent"}, warm); err != nil {
							b.Error(err)
						}
					}
					elapsed := time.Since(start).Nanoseconds()
					mu.Lock()
					if isQuery {
						queries = append(queries, elapsed)
					} else {
						selections = append(selections, elapsed)
					}
					mu.Unlock()
				}
			}(reader < 4)
		}
		wg.Wait()
	}
	b.StopTimer()
	for name, samples := range map[string][]int64{"select": selections, "query": queries} {
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		b.ReportMetric(float64(samples[len(samples)/2])/1e6, name+"-p50-ms")
		b.ReportMetric(float64(samples[(len(samples)-1)*95/100])/1e6, name+"-p95-ms")
	}
}
