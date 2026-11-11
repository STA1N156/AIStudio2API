package aistudio

import (
	"reflect"
	"testing"
)

func TestModelUnionPreservesOrderAndSource(t *testing.T) {
	a := Model{ID: "a", Methods: []string{"generateContent"}, InputTokenLimit: 100, Capabilities: map[string]bool{"chat_model": true}}
	lists := [][]Model{{a, {ID: "c"}}, {{ID: "a", Methods: []string{"countTokens"}, InputTokenLimit: 50, Capabilities: map[string]bool{"thinking": true}}, {ID: "b"}, {ID: "c"}}, {{ID: "d"}, {ID: "b"}}}
	got := mergeModelLists(lists...)
	wantIDs := []string{"a", "d", "b", "c"}
	ids := make([]string, len(got))
	for i := range got {
		ids[i] = got[i].ID
	}
	if !reflect.DeepEqual(ids, wantIDs) || got[0].InputTokenLimit != 50 || !got[0].Capabilities["thinking"] ||
		!reflect.DeepEqual(got[0].Methods, []string{"countTokens", "generateContent"}) {
		t.Fatalf("unexpected model union: %+v", got)
	}
	got[0].Capabilities["chat_model"] = false
	if !a.Capabilities["chat_model"] || a.Capabilities["thinking"] || len(a.Methods) != 1 {
		t.Fatal("model union mutated the account catalog")
	}
}
