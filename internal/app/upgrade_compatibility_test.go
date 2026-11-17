package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestUpgradePreservesAntiTruncationAndThinking(t *testing.T) {
	budget := int64(8192)
	request := aistudio.GenerateRequest{
		Model: "抗截断/gemini-4-carbon-high", System: []string{"Keep the original instructions."},
		Config:   aistudio.GenerationConfig{ThinkingBudget: &budget},
		Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "Keep the original input."}}}},
	}
	if !prepareAntiTruncation(&request) || request.System[0] != "Keep the original instructions." {
		t.Fatal("anti-truncation removed original instructions")
	}
	model, cfg, err := aistudio.ApplyModelSuffix(request.Model, request.Config)
	if err != nil || model != "gemini-4-carbon" || cfg.ThinkingBudget == nil || *cfg.ThinkingBudget != budget {
		t.Fatalf("client thinking setting changed: %s %+v %v", model, cfg, err)
	}
	if !request.Tools.ToolConfig.StreamReplyArguments || request.Tools.Functions[0].Name != replyToolName {
		t.Fatal("streaming anti-truncation tool missing")
	}
	for _, suffix := range []string{"-xhigh", "(8192)", "-medium", "-low"} {
		if _, _, err := aistudio.ApplyModelSuffix("gemini-4-carbon"+suffix, aistudio.GenerationConfig{}); err != nil {
			t.Fatalf("thinking suffix %s: %v", suffix, err)
		}
	}
}

func TestUpgradeAntiTruncationStreamsWithoutDuplicateText(t *testing.T) {
	decoder := &aistudio.BuildStreamDecoder{}
	input := make(chan aistudio.Event, 8)
	for _, raw := range []string{
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"output_reply","partialArgs":[{"jsonPath":"$.content","stringValue":"hello "}],"willContinue":true}}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"partialArgs":[{"jsonPath":"$.content","stringValue":"world"}],"willContinue":false}}]},"finishReason":"STOP"}]}`,
	} {
		events, err := decoder.Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			input <- event
		}
	}
	close(input)
	var text strings.Builder
	for event := range antiTruncationEvents(context.Background(), input) {
		if event.Kind == aistudio.EventToolCall || event.Kind == aistudio.EventToolCallDelta {
			t.Fatal("internal reply tool escaped into public output")
		}
		if event.Kind == aistudio.EventText {
			text.WriteString(event.Text)
		}
	}
	if text.String() != "hello world" {
		t.Fatalf("streamed reply changed: %q", text.String())
	}
}
