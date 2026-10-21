package aistudio

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestGeneratedOutputPartsBoundariesAndUsage(t *testing.T) {
	call := &FunctionCall{Name: "reply", Arguments: json.RawMessage(`{"content":"正文"}`)}
	code := &ExecutableCode{Language: "PYTHON", Code: "print(1)"}
	result := &CodeExecutionResult{Outcome: "OK", Output: "1"}
	events := []Event{
		{Kind: EventText, Text: "你", ThoughtSignature: "a"},
		{Kind: EventReasoning, Text: "思", ThoughtSignature: "r"},
		{Kind: EventText, Text: "好🙂", ThoughtSignature: "a"},
		{Kind: EventReasoning, Text: "考", ThoughtSignature: "r"},
		{Kind: EventText, ThoughtSignature: "ignored"},
		{Kind: EventText, Text: "新", ThoughtSignature: "b"},
		{Kind: EventText, Text: "签名", ThoughtSignature: "b"},
		{Kind: EventReasoning, Text: "另一", ThoughtSignature: "s"},
		{Kind: EventReasoning, Text: "段", ThoughtSignature: "s"},
		{Kind: EventToolCall, ToolCall: call},
		{Kind: EventText, Text: "工"}, {Kind: EventText, Text: "具"},
		{Kind: EventExecutableCode, ExecutableCode: code},
		{Kind: EventText, Text: "代"}, {Kind: EventText, Text: "码"},
		{Kind: EventCodeExecutionResult, CodeExecutionResult: result},
		{Kind: EventText, Text: "结"}, {Kind: EventText, Text: "果"},
		{Kind: EventMedia, Media: &Media{MIME: "image/png", Data: []byte{1}}},
		{Kind: EventMedia, Media: &Media{MIME: "image/png", Data: []byte{2}}},
		{Kind: EventText, Text: "图"}, {Kind: EventText, Text: "片"},
		{Kind: EventMedia, Media: &Media{URL: "https://example.com/image", Name: "image", MIME: "image/png"}},
		{Kind: EventText, Text: "结"}, {Kind: EventText, Text: "尾"},
	}
	var output generatedOutputParts
	for _, event := range events {
		output.observe(event)
	}
	want := generatedOutputParts{
		visible: []Part{
			{Text: "你好🙂", ThoughtSignature: "a"}, {Text: "新签名", ThoughtSignature: "b"},
			{FunctionCall: call}, {Text: "工具"}, {ExecutableCode: code}, {Text: "代码"},
			{CodeExecutionResult: result}, {Text: "结果"},
			{InlineData: &Blob{MIME: "image/png", Data: []byte{1, 2}}}, {Text: "图片"},
			{File: &FileRef{ID: "https://example.com/image", Name: "image", MIME: "image/png"}}, {Text: "结尾"},
		},
		reasoning: []Part{{Text: "思考", Thought: true, ThoughtSignature: "r"}, {Text: "另一段", Thought: true, ThoughtSignature: "s"}},
	}
	if !reflect.DeepEqual(output.visible, want.visible) || !reflect.DeepEqual(output.reasoning, want.reasoning) {
		t.Fatalf("parts changed: visible=%+v reasoning=%+v", output.visible, output.reasoning)
	}
	request := GenerateRequest{System: "system", Contents: []Content{{Role: RoleUser, Parts: []Part{{Text: "用户输入"}}}}}
	if got, expected := localCompleteUsage(request, output), localCompleteUsage(request, want); !reflect.DeepEqual(got, expected) {
		t.Fatalf("usage changed: got %+v want %+v", got, expected)
	}
	count := TokenCount{InputTokens: 42}
	counts := make(chan tokenCountResult, 1)
	counts <- tokenCountResult{count: count}
	var forwarded []Event
	finishAtStopSequence(context.Background(), request, output, counts, "STOP", func(event Event) error {
		forwarded = append(forwarded, event)
		return nil
	})
	if len(forwarded) != 2 || forwarded[0].Kind != EventUsage ||
		!reflect.DeepEqual(forwarded[0].Usage, countedCompleteUsage(request, want, count)) ||
		forwarded[1].Kind != EventFinish || forwarded[1].StopSequence != "STOP" {
		t.Fatalf("stop sequence events changed: %+v", forwarded)
	}
}

func TestGeneratedOutputTextSnapshots(t *testing.T) {
	var output generatedOutputParts
	output.observe(Event{Kind: EventText, Text: "第"})
	output.observe(Event{Kind: EventText, Text: "一句"})
	snapshot := output.visible[0].Text
	output.observe(Event{Kind: EventText, Text: "的后半句"})
	output.observe(Event{Kind: EventText, Text: "第二句", ThoughtSignature: "new"})
	output.observe(Event{Kind: EventText, Text: "。", ThoughtSignature: "new"})
	if snapshot != "第一句" || output.visible[0].Text != "第一句的后半句" || output.visible[1].Text != "第二句。" {
		t.Fatalf("earlier text was overwritten: snapshot=%q parts=%+v", snapshot, output.visible)
	}
}
