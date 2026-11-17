package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestCooldownTierEligibility(t *testing.T) {
	model := []aistudio.Model{{ID: "flash", Methods: []string{"generateContent"}}}
	tiers := []aistudio.BenefitTier{aistudio.BenefitTierUltra, aistudio.BenefitTierUltra, aistudio.BenefitTierPro, aistudio.BenefitTierFree, aistudio.BenefitTierFree, aistudio.BenefitTierPro}
	ids := []string{"ultra-cooling", "ultra-ready", "pro-cooling", "free-cooling", "disabled", "bad"}
	var accounts []*aistudio.Account
	for i, id := range ids {
		config := aistudio.DefaultAccountConfig(id)
		config.Enabled = id != "disabled"
		accounts = append(accounts, &aistudio.Account{ID: id, Config: config, State: aistudio.AccountReady, BenefitTier: tiers[i], Models: model})
	}
	pool := aistudio.NewAccountPool(accounts, 5)
	for i, id := range ids {
		if err := pool.SetCatalog(id, tiers[i], model); err != nil {
			t.Fatal(err)
		}
		if id != "ultra-ready" {
			if err := pool.MarkCooldownIfGeneration(id, "flash", pool.ModelAccessGeneration(id), time.Now(), time.Now().Add(time.Hour), "quota"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := pool.MarkAuthRequired("bad", "expired"); err != nil {
		t.Fatal(err)
	}
	for _, allowFree := range []bool{false, true} {
		allowed := []string{"Ultra", "Pro"}
		want := map[string]int{"Ultra": 2, "Pro": 1}
		wantRows, wantTotal := 2, 3
		if allowFree {
			allowed = append(allowed, "Free")
			want["Free"] = 1
			wantRows, wantTotal = 3, 4
		}
		pool.SetModelTiers(map[string][]string{"flash": allowed})
		admin := &runtimeAdmin{pool: pool}
		rows, err := admin.Cooldowns(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != wantRows {
			t.Fatalf("cooldown rows: got %d want %d", len(rows), wantRows)
		}
		for _, row := range rows {
			if row.EligibleAccounts != wantTotal || !reflect.DeepEqual(row.EligibleTiers, want) {
				t.Fatalf("incorrect eligible tiers: %+v", row)
			}
		}
	}
}
