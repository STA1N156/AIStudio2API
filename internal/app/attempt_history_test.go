package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type attemptHistoryService struct {
	aistudio.Service
	contents []aistudio.Content
}

func (s *attemptHistoryService) Generate(_ context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	s.contents = request.Contents
	events := make(chan aistudio.Event, 1)
	events <- aistudio.Event{Kind: aistudio.EventText, Text: "answer to the new question"}
	close(events)
	return events, nil
}

func TestAttemptPreservesHistoryInMixedCatalog(t *testing.T) {
	const model = "gemini-4-hydrogen"
	dir := t.TempDir()
	storage := filepath.Join(dir, "storage-state.json")
	if err := os.WriteFile(storage, []byte(`{"cookies":[{"name":"SAPISID","value":"test","domain":".google.com","path":"/","secure":true}],"origins":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := aistudio.LoadStorageState(storage)
	if err != nil {
		t.Fatal(err)
	}
	account := &aistudio.Account{ID: "history@example.com", Directory: dir, StoragePath: storage,
		RuntimePath: filepath.Join(dir, "runtime-state.json"), StorageState: state, Config: aistudio.DefaultAccountConfig("history@example.com")}
	pool := aistudio.NewAccountPool([]*aistudio.Account{account}, 1)
	lease, err := pool.AcquireAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	for _, id := range []string{model, antiTruncationPrefix + model} {
		t.Run(id, func(t *testing.T) {
			upstream := &attemptHistoryService{}
			service := &trackedService{service: upstream, models: []aistudio.Model{
				{ID: model, Capabilities: map[string]bool{"chat_model": true, "interaction_task": true}},
			}}
			request := aistudio.GenerateRequest{Model: id, Contents: []aistudio.Content{
				{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "earlier question"}}},
				{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{FunctionCall: &aistudio.FunctionCall{ID: "lookup1", Name: "lookup"}}}},
				{Role: aistudio.RoleTool, Parts: []aistudio.Part{{FunctionResult: &aistudio.FunctionResult{ID: "lookup1", Name: "lookup", Content: json.RawMessage(`{"value":42}`)}}}},
				{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{Text: "earlier answer"}}},
				{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "new question"}}},
			}}
			prepareAntiTruncation(&request)
			if id != model && !hasCurrentInputToolResult(request.Contents) {
				t.Fatal("anti-truncation no longer wraps the latest input")
			}
			attempt := &generateAttempt{lease: lease, activity: &upstreamActivity{}}
			service.runAttempt(t.Context(), request, "", model, "", attempt)
			if attempt.err != nil || !reflect.DeepEqual(upstream.contents, request.Contents) {
				t.Fatalf("history changed before generation: got %d of %d turns; error %v", len(upstream.contents), len(request.Contents), attempt.err)
			}
		})
	}
}
