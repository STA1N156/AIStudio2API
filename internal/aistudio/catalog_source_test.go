package aistudio

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSingleCatalogSourceAndFailover(t *testing.T) {
	accounts := []*Account{
		{ID: "a", Config: DefaultAccountConfig("a@example.invalid"), State: AccountReady, Models: []Model{{ID: "source-a", Capabilities: map[string]bool{"chat_model": true}}}},
		{ID: "b", Config: DefaultAccountConfig("b@example.invalid"), State: AccountReady, Models: []Model{{ID: "source-b"}}},
	}
	pool := NewAccountPool(accounts, 2)
	service := NewPooledService(pool, NewClient(ClientOptions{}))
	got := service.CachedModels()
	if len(got) != 1 || got[0].ID != "source-a" {
		t.Fatalf("catalogs were merged: %+v", got)
	}
	got[0].Capabilities["chat_model"] = false
	if !accounts[0].Models[0].Capabilities["chat_model"] {
		t.Fatal("public catalog shares mutable output")
	}
	accounts[1].Models = []Model{{ID: "source-b"}}
	service.UseCatalogAccount("b")
	if got := service.CachedModels(); len(got) != 1 || got[0].ID != "source-b" {
		t.Fatalf("source switch failed: %+v", got)
	}
	accounts[1].Config.Enabled = false
	if got := service.CachedModels(); len(got) != 1 || got[0].ID != "source-b" {
		t.Fatalf("disabled source retained: %+v", got)
	}
	until := time.Now().Add(time.Hour)
	accounts[0].runtime.Cooldowns["source-a"] = CooldownState{Until: until}
	service.UseCatalogAccount("a")
	service.CachedModels()
	if !accounts[0].runtime.Cooldowns["source-a"].Until.Equal(until) {
		t.Fatal("catalog changed cooldown")
	}
}

func TestSharedCatalogRestoresPoolWithoutChangingAccountState(t *testing.T) {
	accounts := make([]*Account, 5000)
	for i := range accounts {
		id := fmt.Sprintf("account-%04d", i)
		config := DefaultAccountConfig(id + "@example.invalid")
		config.Proxy = fmt.Sprintf("http://127.0.0.1:%d", 10000+i)
		accounts[i] = &Account{ID: id, Config: config, State: AccountReady, BenefitTier: BenefitTierPro}
	}
	accounts[0].Models = []Model{{ID: "flash", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}}
	accounts[1].Config.Enabled = false
	accounts[2].BenefitTier = BenefitTierFree
	pool := NewAccountPool(accounts, 5)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground})
	pool.SetModelTiers(map[string][]string{"flash": {"Ultra", "Pro"}})
	until := time.Now().Add(time.Hour)
	accounts[3].runtime.Cooldowns["flash"] = CooldownState{Until: until}
	service := NewPooledService(pool, NewClient(ClientOptions{}))
	selection := AccountSelection{ModelID: "flash", Method: "generateContent"}
	before, err := pool.ClassifyCandidates(context.Background(), selection, nil)
	if err != nil || len(before.StandbyReady) != 1 {
		t.Fatalf("expected one usable source before sharing: %d, %v", len(before.StandbyReady), err)
	}
	service.UseCatalogAccount(accounts[0].ID)
	after, err := pool.ClassifyCandidates(context.Background(), selection, nil)
	if err != nil || len(after.StandbyReady) != 4997 {
		t.Fatalf("shared catalog eligibility: %d, %v", len(after.StandbyReady), err)
	}
	for i, account := range accounts {
		if len(account.Models) != 1 || account.Config.Proxy != fmt.Sprintf("http://127.0.0.1:%d", 10000+i) {
			t.Fatalf("catalog or proxy changed unexpectedly: %d", i)
		}
	}
	if accounts[1].Config.Enabled || accounts[2].BenefitTier != BenefitTierFree || !accounts[3].runtime.Cooldowns["flash"].Until.Equal(until) {
		t.Fatal("sharing catalog changed account eligibility or cooldown")
	}
}
