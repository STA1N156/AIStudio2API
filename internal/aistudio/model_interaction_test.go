package aistudio

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type captureModelRPC func(RPCRequest)

var errCapturedModelRPC = errors.New("captured model request")

func (capture captureModelRPC) DoProtected(_ context.Context, _ GenerateRequest, request RPCRequest) (*RPCResponse, error) {
	capture(request)
	return nil, errCapturedModelRPC
}

func TestCatalogModelInteractionRoutes(t *testing.T) {
	for _, tc := range []struct {
		name             string
		kind             int
		chat, background bool
		wantStream       bool
	}{
		{name: "model with a background flag", kind: 2, chat: true, background: true},
		{name: "chat wins over conflicting agent metadata", kind: 1, chat: true, background: true},
		{name: "background without a type", background: true},
		{name: "streaming media model", kind: 2, wantStream: true},
		{name: "dedicated agent uses model field too", kind: 1, background: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := make([]any, 79)
			row[0], row[3] = "models/test-model", "Test Model"
			row[6] = 1024
			if tc.chat {
				row[64] = []int{1}
			}
			var kind any
			if tc.kind != 0 {
				kind = tc.kind
			}
			row[78] = []any{nil, nil, tc.background, kind, nil, "models/test-base"}
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			entry, err := decodeModelRow(raw, 0)
			if err != nil {
				t.Fatal(err)
			}
			if entry.defaults.InteractionStream != tc.wantStream || entry.model.Capabilities["interaction_stream"] != tc.wantStream {
				t.Fatalf("stream route = %t, catalog flag = %t; want %t", entry.defaults.InteractionStream, entry.model.Capabilities["interaction_stream"], tc.wantStream)
			}
			var rpc RPCRequest
			client := &Client{
				catalogs:  map[string]modelCatalog{"test": {entries: map[string]modelEntry{entry.model.ID: entry}}},
				protected: captureModelRPC(func(request RPCRequest) { rpc = request }),
			}
			_, err = client.Generate(t.Context(), GenerateRequest{
				AccountID: "test", Model: entry.model.ID,
				Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "Hello"}}}},
			})
			if !errors.Is(err, errCapturedModelRPC) {
				t.Fatal(err)
			}
			var body []any
			if err := json.Unmarshal(rpc.Body, &body); err != nil {
				t.Fatal(err)
			}
			if tc.wantStream {
				if rpc.Method != "CreateInteractionStream" {
					t.Fatalf("RPC = %s, want CreateInteractionStream", rpc.Method)
				}
				interaction := body[3].([]any)
				if interaction[17].([]any)[0] != row[0] || interaction[18] != nil {
					t.Fatalf("stream request did not use only the model field: %s", rpc.Body)
				}
			} else if rpc.Method != "GenerateContent" || body[0] != row[0] {
				t.Fatalf("RPC = %s, body = %s; want GenerateContent with model %q", rpc.Method, rpc.Body, row[0])
			}
		})
	}
}
