package aistudio

import (
	"testing"
	"time"
)

func BenchmarkSharedSnapshotContention(b *testing.B) {
	benchmarkCooldownContention(b, func(p *AccountPool) {
		snapshot := p.Snapshot()
		snapshot.Status()
		snapshot.CooldownAccountEligibility([]string{"model-35", "model-36", "model-37", "model-38", "model-39"})
	})
}

func TestStatusSnapshotDoesNotShareMutableState(t *testing.T) {
	p := cooldownLoadPool()
	snapshot := p.Snapshot()
	until := snapshot.pool.accounts[0].runtime.Cooldowns["model-39"].Until
	p.mu.Lock()
	p.accounts[0].Config.Enabled = false
	p.accounts[0].runtime.Cooldowns["model-39"] = CooldownState{Until: until.Add(time.Hour)}
	p.mu.Unlock()
	p.SetModelTiers(map[string][]string{"model-39": {}})
	if !snapshot.pool.accounts[0].Config.Enabled || !snapshot.pool.accounts[0].runtime.Cooldowns["model-39"].Until.Equal(until) {
		t.Fatal("live mutations changed the detached snapshot")
	}
	if len(snapshot.CooldownAccountEligibility([]string{"model-39"})["model-39"]) != 5738 {
		t.Fatal("new tier rules changed an existing snapshot")
	}
	if len(p.CooldownAccountEligibility([]string{"model-39"})["model-39"]) != 0 {
		t.Fatal("fresh eligibility ignored new tier rules")
	}
	statuses := p.Status()
	delete(statuses[0].Cooldowns, "model-39")
	if _, exists := p.Status()[0].Cooldowns["model-39"]; !exists {
		t.Fatal("a returned status changed live cooldowns")
	}
	if len(snapshot.Status()) != 5738 {
		t.Fatal("snapshot lost accounts")
	}
}
