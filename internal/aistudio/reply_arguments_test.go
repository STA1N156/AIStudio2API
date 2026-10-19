package aistudio

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildReplyArgumentFragments(t *testing.T) {
	decoder := &BuildStreamDecoder{}
	chunks := []string{
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"output_reply","id":"call1","partialArgs":[{"jsonPath":"$.content","stringValue":"你好","willContinue":true}],"willContinue":true}}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"partialArgs":[{"jsonPath":"$.content","stringValue":"\n世界"}],"willContinue":true}}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"args":{"content":"你好\n世界"}}}]},"finishReason":"STOP"}]}`,
	}
	var deltas strings.Builder
	var completed *FunctionCall
	for index, chunk := range chunks {
		events, err := decoder.Decode([]byte(chunk))
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Kind == EventToolCallDelta {
				deltas.WriteString(event.Text)
			}
			if event.Kind == EventToolCall {
				completed = event.ToolCall
			}
		}
		if index == 0 && deltas.String() != "你好" {
			t.Fatal("first fragment was held until the whole call finished")
		}
	}
	if completed == nil || completed.ID != "call1" || deltas.String() != "你好\n世界" {
		t.Fatalf("deltas=%q call=%+v", deltas.String(), completed)
	}
	var content struct{ Content string }
	if err := json.Unmarshal(completed.Arguments, &content); err != nil || content.Content != deltas.String() {
		t.Fatalf("completed arguments=%s err=%v", completed.Arguments, err)
	}
	if err := decoder.End(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildReplyStreamFlag(t *testing.T) {
	request := GenerateRequest{Model: "gemini-3.8-flash", Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "hello"}}}}, Tools: Tools{
		Functions:  []FunctionDeclaration{{Name: "output_reply", Parameters: json.RawMessage(`{"type":"object","properties":{"content":{"type":"string"}}}`)}},
		ToolConfig: ToolConfig{Mode: "required"}, StreamReplyArguments: true,
	}}
	for _, unary := range []bool{false, true} {
		_, body, err := EncodeBuildGenerateRequest(request, GenerationDefaults{MaxOutputTokens: 1024}, false, unary)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			ToolConfig struct {
				FunctionCallingConfig struct {
					Stream bool `json:"streamFunctionCallArguments"`
				} `json:"functionCallingConfig"`
			} `json:"toolConfig"`
		}
		if err := json.Unmarshal(body, &wire); err != nil || wire.ToolConfig.FunctionCallingConfig.Stream == unary {
			t.Fatalf("unary=%t body=%s err=%v", unary, body, err)
		}
	}
}
