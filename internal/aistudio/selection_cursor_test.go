package aistudio

import (
	"reflect"
	"testing"
	"time"
)

func TestSelectionCursorPreservesOrderAndLiveChecks(t *testing.T) {
	models := []Model{{ID: "flash", Methods: []string{"generateContent"}}}
	var accounts []*Account
	for _, id := range []string{"c", "a", "b"} {
		accounts = append(accounts, &Account{ID: id, Config: DefaultAccountConfig(id), State: AccountReady, Models: models, buildModels: models})
	}
	p := NewAccountPool(accounts, 5)
	p.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
	p.SetRoutingStrategy("round-robin")
	selection := AccountSelection{ModelID: "flash", Method: "generateContent"}
	var got []string
	for range 7 {
		lease, _, _, err := p.tryAcquireLocked(selection, time.Now())
		if err != nil || lease == nil {
			t.Fatalf("acquire: %v", err)
		}
		got = append(got, lease.account.ID+":"+string(lease.channel))
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"a:playground", "a:build", "b:playground", "b:build", "c:playground", "c:build", "a:playground"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order: %v", got)
	}
	p.byID["a"].Config.Enabled = false
	p.byID["b"].runtime.Cooldowns["*"] = CooldownState{Until: time.Now().Add(time.Hour)}
	lease, _, _, err := p.tryAcquireLocked(selection, time.Now())
	if err != nil || lease == nil || lease.account.ID != "c" {
		t.Fatalf("did not skip disabled/cooling accounts: %v %v", lease, err)
	}
	lease.Release()
	p.byID["c"].active = 5
	if lease, _, _, _ := p.tryAcquireLocked(selection, time.Now()); lease != nil {
		t.Fatal("selected an account with no remaining slot")
	}
	p.byID["c"].active = 0
	p.SetModelTiers(map[string][]string{"flash": {}})
	if lease, _, _, _ := p.tryAcquireLocked(selection, time.Now()); lease != nil {
		t.Fatal("ignored updated model eligibility")
	}
	p.SetModelTiers(nil)
	selection.AllowedAccountIDs = []string{}
	if lease, _, _, _ := p.tryAcquireLocked(selection, time.Now()); lease != nil {
		t.Fatal("ignored empty allowlist")
	}
	selection.AllowedAccountIDs = []string{"c"}
	selection.AccountID = "b"
	if lease, _, _, _ := p.tryAcquireLocked(selection, time.Now()); lease != nil {
		t.Fatal("ignored fixed-account restriction")
	}
}

func BenchmarkAcquire5738(b *testing.B) {
	p := cooldownLoadPool()
	p.SetRoutingStrategy("round-robin")
	selection := AccountSelection{ModelID: "model-39", Method: "generateContent"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p.mu.Lock()
		lease, _, _, err := p.tryAcquireLocked(selection, time.Now())
		p.mu.Unlock()
		if err != nil || lease == nil {
			b.Fatalf("acquire: %v", err)
		}
		if err := lease.Release(); err != nil {
			b.Fatal(err)
		}
	}
}
