package aistudio

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestManualModelTiersKeepCooldownSeparate(t *testing.T) {
	models := []Model{{ID: "argon", Methods: []string{"generateContent"}}, {ID: "flash", Methods: []string{"generateContent"}}}
	var accounts []*Account
	for _, id := range []string{"ultra", "pro", "free", "disabled", "auth"} {
		accounts = append(accounts, &Account{ID: id, Config: DefaultAccountConfig(id + "@example.com"), State: AccountReady, Models: models, buildModels: models})
	}
	pool := NewAccountPool(accounts, 2)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground, ChannelBuild})
	pool.byID["ultra"].BenefitTier = BenefitTierUltra
	pool.byID["pro"].BenefitTier = BenefitTierPro
	pool.byID["disabled"].Config.Enabled = false
	pool.byID["auth"].State = AccountAuthRequired
	free := pool.byID["free"]
	free.runtime.ModelAccess["argon"] = ModelAccess{State: "denied"}
	if got := pool.CooldownAccountEligibility([]string{"argon"}); len(got["argon"]) != 3 {
		t.Fatalf("default all tiers or ignored legacy denial: %v", got)
	}
	until := time.Now().Add(time.Hour)
	pool.byID["pro"].runtime.Cooldowns["argon"] = CooldownState{Until: until}
	pool.byID["ultra"].active = 1
	pool.SetModelTiers(map[string][]string{"argon": {"Ultra", "Pro"}})
	got := pool.CooldownAccountEligibility([]string{"argon", "flash"})
	if len(got["argon"]) != 2 || len(got["flash"]) != 3 {
		t.Fatalf("wrong model denominators: %v", got)
	}
	for _, channel := range []Channel{ChannelPlayground, ChannelBuild} {
		if pool.channelSupportsLocked(free, channel, AccountSelection{ModelID: "argon", Method: "generateContent"}) {
			t.Fatalf("Free routed through %s", channel)
		}
	}
	if pool.Status()[2].DeniedModels["argon"] == "" {
		t.Fatal("manual restriction missing from account card")
	}
	pool.SetModelTiers(nil)
	if !pool.channelSupportsLocked(free, ChannelPlayground, AccountSelection{ModelID: "argon"}) {
		t.Fatal("restoring permission must take effect immediately")
	}
	if cooldown, active := accountCooldown(pool.byID["pro"], "argon", time.Now()); !active || !cooldown.Until.Equal(until) {
		t.Fatal("changing eligibility altered cooldown")
	}
	lease := &AccountLease{pool: pool, account: free, checkedAt: time.Now()}
	if err := lease.MarkRequestFailure(&RPCError{Method: "GenerateContent", StatusCode: 400, Code: 9, Message: "not eligible to use this model"}); err != nil {
		t.Fatal(err)
	}
	if len(pool.Status()[2].DeniedModels) != 0 {
		t.Fatal("failure must not create a model eligibility mark")
	}
	pool.SetModelTiers(map[string][]string{"argon": {}})
	if got := pool.CooldownAccountEligibility([]string{"argon"}); len(got["argon"]) != 0 {
		t.Fatal("all unchecked must exclude all accounts")
	}
}

type freeCatalogTransport struct{ methods []string }

func (f *freeCatalogTransport) Do(_ context.Context, request RPCRequest) (*RPCResponse, error) {
	f.methods = append(f.methods, request.Method)
	body := "[[]]"
	if request.Method == "GetAiStudioBenefitTier" {
		body = "[0]"
	}
	return &RPCResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {JSONProtobufContentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestFreeCatalogDoesNotDisableAccount(t *testing.T) {
	account := &Account{ID: "free", Config: DefaultAccountConfig("free@example.com"), State: AccountReady}
	pool := NewAccountPool([]*Account{account}, 1)
	transport := &freeCatalogTransport{}
	client := &Client{transport: transport, tiers: make(map[string]BenefitTier), catalogs: make(map[string]modelCatalog)}
	service, err := NewPooledService(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	lease := &AccountLease{pool: pool, account: account, checkedAt: time.Now()}
	if _, err := service.modelsForLease(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if !account.Config.Enabled || pool.Status()[0].BenefitTier != "Free" || len(transport.methods) != 2 {
		t.Fatalf("Free account was disabled or catalog skipped: %+v", pool.Status()[0])
	}
}
