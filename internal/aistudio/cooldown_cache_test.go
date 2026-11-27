package aistudio

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestConfirmedCooldownScopeAndInvalidation(t *testing.T) {
	newAccount := func(id string) *Account {
		return &Account{ID: id, Config: DefaultAccountConfig(id + "@example.invalid"), State: AccountReady,
			Models: []Model{{ID: "flash", Methods: []string{"generateContent"}}, {ID: "other", Methods: []string{"generateContent"}}}}
	}
	a, b := newAccount("a"), newAccount("b")
	p := NewAccountPool([]*Account{a, b}, 2)
	until := time.Now().Add(time.Hour)
	selection := AccountSelection{ModelID: "flash", Method: "generateContent"}
	p.SetAPIKeyAccounts(map[string][]string{"scoped": {"a"}})
	scoped, open := ContextWithAPIKey(context.Background(), "scoped"), context.Background()
	a.runtime.Cooldowns["flash"] = CooldownState{Until: until, Reason: "quota"}
	check := func(ctx context.Context, want bool) {
		t.Helper()
		cd, ok := p.ConfirmedCooldown(ctx, selection)
		if ok != want || ok && !cd.Until.Equal(until) {
			t.Fatalf("confirmation = %v, %v; want %v", cd, ok, want)
		}
	}
	prime := func(ctx context.Context) {
		t.Helper()
		g, err := p.ClassifyCandidates(ctx, selection, nil)
		if err != nil || !g.allCooling() {
			t.Fatalf("classify = %+v, %v", g, err)
		}
	}
	prime(scoped)
	check(scoped, true)
	check(open, false)
	lease, _, err := p.TryAcquireFor(open, AccountSelection{AccountID: "b", ModelID: "other", Method: "generateContent"})
	if err != nil || lease == nil {
		t.Fatalf("test lease: %v", err)
	}
	if err = lease.Release(); err != nil {
		t.Fatal(err)
	}
	check(scoped, true)
	if err := p.ClearCooldown("a", "flash"); err != nil {
		t.Fatal(err)
	}
	check(scoped, false)
	g, err := p.ClassifyCandidates(scoped, selection, nil)
	if err != nil || len(g.StandbyReady) != 1 {
		t.Fatalf("newly available account not selectable: %+v %v", g, err)
	}
	a.runtime.Cooldowns["flash"] = CooldownState{Until: until}
	b.Config.Enabled = false
	prime(open)
	b.Config.Locale, b.Config.Timezone = "en-US", "UTC"
	b.ConfigPath = filepath.Join(t.TempDir(), accountConfigName)
	if _, err := p.SetEnabled("b", true); err != nil {
		t.Fatal(err)
	}
	check(open, false)
	prime(scoped)
	key, _ := coolingScopeOf(p.scopedSelection(scoped, selection))
	entry := p.confirmedCooling[key]
	entry.cooldown.Until = time.Now().Add(-time.Second)
	p.confirmedCooling[key] = entry
	check(scoped, false)
}

func TestConfirmedCooldownSelectionKey(t *testing.T) {
	k := func(ids []string) coolingScope {
		key, _ := coolingScopeOf(AccountSelection{ModelID: "m", AllowedAccountIDs: ids})
		return key
	}
	if k([]string{"b", "a", "a"}) != k([]string{"a", "b"}) || k(nil) == k([]string{}) || k([]string{"a"}) == k([]string{"b"}) {
		t.Fatal("account scopes not isolated or normalized")
	}
}
