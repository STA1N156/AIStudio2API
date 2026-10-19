package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestFailureClassification(t *testing.T) {
	for _, test := range []struct {
		err  error
		kind string
	}{
		{&aistudio.ModelAccessError{Model: "gemini-4-argon"}, "no_qualification"},
		{fmt.Errorf("wrapped: %w", &aistudio.RPCError{StatusCode: 403}), "forbidden"},
		{&aistudio.RPCError{StatusCode: 429}, "other"},
		{&aistudio.RPCError{StatusCode: 404}, "other"},
		{errors.New("proxy returned HTTP 403"), "other"},
		{errors.New("timeout"), "other"},
	} {
		if got, _ := errorKind(test.err); got != test.kind {
			t.Fatalf("%v: got %s, want %s", test.err, got, test.kind)
		}
	}
}

func TestExplicitAccountSelection(t *testing.T) {
	all := map[string]account{"a@example.com": {}, "b@example.com": {}}
	ids, err := selectIDs(all, "\ufeff# comment\na@example.com\r\na@example.com", false)
	if err != nil || len(ids) != 1 || ids[0] != "a@example.com" {
		t.Fatalf("selection: %v %v", ids, err)
	}
	if _, err := selectIDs(all, "typo@example.com", false); err == nil {
		t.Fatal("unknown account must stop, not expand selection")
	}
	if _, err := selectIDs(all, "", false); err == nil {
		t.Fatal("empty selection must not mean all")
	}
}

func TestBackupValidation(t *testing.T) {
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	f, _ := w.Create("account.json")
	f.Write([]byte(`{"label":"a@example.com","storage_state":{"cookies":[{"name":"test","value":"not-real"}]}}`))
	w.Close()
	if _, err := decodeBackup(data.Bytes(), []string{"a@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeBackup(data.Bytes(), []string{"b@example.com"}); err == nil {
		t.Fatal("backup for a different account must be rejected")
	}
	if _, err := decodeBackup(data.Bytes(), []string{"a@example.com", "b@example.com"}); err == nil {
		t.Fatal("incomplete backup must be rejected")
	}
}

func policyAccount(t *testing.T) (*aistudio.AccountStore, *aistudio.AccountPool, string) {
	t.Helper()
	store := aistudio.NewAccountStore(t.TempDir())
	config := aistudio.DefaultAccountConfig("policy@example.com")
	config.Proxy = "http://127.0.0.1:7890"
	item, published, err := store.Create(config, aistudio.StorageState{Cookies: []aistudio.StateCookie{
		{Name: "SAPISID", Value: "test-only", Domain: ".google.com", Path: "/", Expires: -1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := published.Release(); err != nil {
		t.Fatal(err)
	}
	return store, aistudio.NewAccountPool([]*aistudio.Account{item}, 1), item.ID
}

func TestAutomaticDisableFor403(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		disable bool
	}{
		{"upstream403", &aistudio.RPCError{StatusCode: 403}, true},
		{"upstream429", &aistudio.RPCError{StatusCode: 429}, false},
		{"network", errors.New("proxy returned 403"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, pool, id := policyAccount(t)
			lease, err := pool.AcquireAccount(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if err := lease.MarkRequestFailure(tc.err); err != nil {
				t.Fatal(err)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			loaded, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if pool.Status()[0].Enabled == tc.disable || loaded[0].Config.Enabled == tc.disable || loaded[0].Config.Proxy != "http://127.0.0.1:7890" {
				t.Fatal("disable state was not persisted correctly, or proxy changed")
			}
		})
	}
}

type policyTransport struct{ tier string }

func (transport policyTransport) Do(_ context.Context, request aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
	status, body := 200, transport.tier
	if request.Method != "GetAiStudioBenefitTier" || transport.tier == "failure" {
		status, body = 503, `{"error":{"message":"unavailable"}}`
	}
	return &aistudio.RPCResponse{StatusCode: status, Header: http.Header{
		"Content-Type": {aistudio.JSONProtobufContentType},
	}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestAutomaticDisableOnlyForConfirmedFree(t *testing.T) {
	for _, tier := range []string{"[0]", "[1]", "[2]", "failure"} {
		t.Run(tier, func(t *testing.T) {
			store, pool, id := policyAccount(t)
			client, err := aistudio.NewClient(aistudio.ClientOptions{
				Transport: policyTransport{tier: tier},
				Protected: aistudio.ProtectedTransportFunc(func(context.Context, aistudio.GenerateRequest, aistudio.RPCRequest) (*aistudio.RPCResponse, error) {
					return nil, errors.New("unexpected generation")
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			service, err := aistudio.NewPooledService(pool, client)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.RefreshAccountModels(context.Background(), id)
			if tier == "[0]" && err != nil {
				t.Fatal(err)
			}
			loaded, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if loaded[0].Config.Enabled == (tier == "[0]") {
				t.Fatalf("unexpected enabled state after tier %s", tier)
			}
		})
	}
}
