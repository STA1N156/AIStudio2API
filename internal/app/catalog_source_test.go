package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type singleCatalogStub struct {
	modelCatalogService
	calls  []string
	source string
}

func (s *singleCatalogStub) RefreshAccountModels(_ context.Context, id string, _ bool) ([]aistudio.Model, error) {
	s.calls = append(s.calls, id)
	if id == "bad" {
		return nil, errors.New("unavailable")
	}
	return []aistudio.Model{{ID: "shared-model"}}, nil
}
func (s *singleCatalogStub) UseCatalogAccount(id string) { s.source = id }

func TestSingleCatalogRefreshStopsAtFirstSuccess(t *testing.T) {
	for _, ids := range [][]string{{"first", "unused"}, {"bad", "fallback", "unused"}} {
		ctx, cancel := context.WithCancel(context.Background())
		stub := &singleCatalogStub{}
		service := &trackedService{lifecycle: ctx, catalog: stub, requests: newRequestRegistry(ctx), modelRetries: make(map[string]struct{})}
		for range service.refreshAccountModelCatalogs(ctx, ids, false) {
		}
		cancel()
		want := ids[:len(ids)-1]
		if !reflect.DeepEqual(stub.calls, want) || stub.source != want[len(want)-1] {
			t.Fatalf("unexpected catalog calls: %v, source=%s", stub.calls, stub.source)
		}
	}
}

func TestIncompletePageCatalogSwitchesAccount(t *testing.T) {
	err := errors.Join(errors.New("prepare generation"), aistudio.ErrIncompletePageCatalog)
	if !retryableGenerateAccountError(context.Background(), err) {
		t.Fatal("incomplete page data must allow retrying another account")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if retryableGenerateAccountError(ctx, err) {
		t.Fatal("a canceled request must not retry")
	}
}
