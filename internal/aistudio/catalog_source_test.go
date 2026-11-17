package aistudio

import (
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
	service.UseCatalogAccount("b")
	if got := service.CachedModels(); len(got) != 1 || got[0].ID != "source-b" {
		t.Fatalf("source switch failed: %+v", got)
	}
	accounts[1].Config.Enabled = false
	if got := service.CachedModels(); len(got) != 1 || got[0].ID != "source-a" {
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
