package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

type antiTruncationTestService struct {
	managedService
	request aistudio.GenerateRequest
	output  []aistudio.Event
	calls   int
}

func (service *antiTruncationTestService) Generate(_ context.Context, request aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	service.request = request
	service.calls++
	events := make(chan aistudio.Event, len(service.output))
	for _, event := range service.output {
		events <- event
	}
	close(events)
	return events, nil
}

func TestAntiTruncationProtocols(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, protocol := range []string{"chat", "responses", "anthropic", "gemini"} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				service := &antiTruncationTestService{output: []aistudio.Event{
					{Kind: aistudio.EventReasoning, Text: "真实思考"},
					{Kind: aistudio.EventToolCall, ToolCall: &aistudio.FunctionCall{ID: "reply", Name: replyToolName, Arguments: json.RawMessage(`{"content":"抗截断验证成功\n第二行"}`)}},
					{Kind: aistudio.EventUsage, Usage: &aistudio.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30}},
					{Kind: aistudio.EventFinish, FinishReason: "stop"},
				}}
				manager := &runtimeManager{current: &runtimeGeneration{service: service}}
				handler := api.NewHandler(manager, api.Config{})
				path := "/v1/chat/completions"
				body := fmt.Sprintf(`{"model":"抗截断/gemini-3.8-flash","messages":[{"role":"user","content":"你好"}],"stream":%t}`, stream)
				switch protocol {
				case "responses":
					path = "/v1/responses"
					body = fmt.Sprintf(`{"model":"抗截断/gemini-3.8-flash","input":"你好","stream":%t}`, stream)
				case "anthropic":
					path = "/v1/messages"
					body = fmt.Sprintf(`{"model":"抗截断/gemini-3.8-flash","max_tokens":1024,"messages":[{"role":"user","content":"你好"}],"stream":%t}`, stream)
				case "gemini":
					action := "generateContent"
					if stream {
						action = "streamGenerateContent"
					}
					path = "/v1beta/models/" + url.PathEscape("抗截断/gemini-3.8-flash") + ":" + action
					body = `{"contents":[{"role":"user","parts":[{"text":"你好"}]}]}`
				}
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				output := response.Body.String()
				if response.Code != http.StatusOK || !strings.Contains(output, "抗截断验证成功") || strings.Contains(output, replyToolName) {
					t.Fatalf("status=%d body=%s", response.Code, output)
				}
				if service.request.Model != "gemini-3.8-flash" || service.request.Tools.ToolConfig.Mode != "required" || len(service.request.Contents) != 3 {
					t.Fatalf("upstream request=%+v", service.request)
				}
			})
		}
	}
}

func TestAntiTruncationPreparation(t *testing.T) {
	original := aistudio.GenerateRequest{ID: "test", Model: "gemini-3.8-flash", System: "原系统提示词", Contents: []aistudio.Content{
		{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "历史"}}},
		{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{Text: "历史回复"}}},
		{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "最新输入"}, {InlineData: &aistudio.Blob{MIME: "image/png", Data: []byte{1}}}}},
	}}
	before, _ := json.Marshal(original)
	plain := original
	if prepareAntiTruncation(&plain) || !reflect.DeepEqual(plain, original) {
		t.Fatal("ordinary request changed")
	}
	request := original
	request.Model = antiTruncationPrefix + original.Model
	if !prepareAntiTruncation(&request) {
		t.Fatal("anti-truncation not enabled")
	}
	after, _ := json.Marshal(original)
	if string(before) != string(after) || len(request.Contents) != 5 || request.Contents[2].Parts[1].InlineData == nil {
		t.Fatal("history, attachment or caller-owned request changed")
	}
	call := request.Contents[3].Parts[0].FunctionCall
	result := request.Contents[4].Parts[0].FunctionResult
	if call.ID != result.ID || !strings.Contains(string(result.Content), `"text":"最新输入"`) {
		t.Fatalf("input tool pair=%+v %+v", call, result)
	}
	for _, mode := range []string{"none", "required"} {
		request = original
		request.Model = antiTruncationPrefix + original.Model
		request.Tools.ToolConfig.Mode = mode
		if prepareAntiTruncation(&request) || request.Model != original.Model || !reflect.DeepEqual(request.Contents, original.Contents) {
			t.Fatalf("client tool choice %q overwritten", mode)
		}
	}
}

func TestAntiTruncationEmptyAndUpstreamError(t *testing.T) {
	upstreamError := fmt.Errorf("upstream disconnected")
	for _, test := range []struct {
		name   string
		output []aistudio.Event
		status int
		text   string
	}{
		{"empty", []aistudio.Event{{Kind: aistudio.EventFinish, FinishReason: "stop"}}, 400, "模型输出为空"},
		{"upstream", []aistudio.Event{{Kind: aistudio.EventError, Err: upstreamError}}, 502, upstreamError.Error()},
		{"plain", []aistudio.Event{{Kind: aistudio.EventText, Text: "普通正文"}, {Kind: aistudio.EventFinish, FinishReason: "stop"}}, 200, "普通正文"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &antiTruncationTestService{output: test.output}
			manager := &runtimeManager{current: &runtimeGeneration{service: service}}
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"抗截断/gemini-3.8-flash","messages":[{"role":"user","content":"你好"}]}`))
			response := httptest.NewRecorder()
			api.NewHandler(manager, api.Config{}).ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.text) || service.calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, service.calls, response.Body.String())
			}
		})
	}
}

func TestAntiTruncationImmediateForwardAndCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	source := make(chan aistudio.Event, 1)
	source <- aistudio.Event{Kind: aistudio.EventReasoning, Text: "立即转发"}
	events := antiTruncationEvents(ctx, cancel, source)
	if event := <-events; event.Text != "立即转发" {
		t.Fatalf("event=%+v", event)
	}
	cancel()
	if _, open := <-events; open {
		t.Fatal("cancelled stream did not close")
	}
}

func TestAntiTruncationToolFragments(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	source := make(chan aistudio.Event, 4)
	events := antiTruncationEvents(ctx, cancel, source)
	for _, text := range []string{"第一段", "\n第二段"} {
		source <- aistudio.Event{Kind: aistudio.EventToolCallDelta, Text: text, ToolCall: &aistudio.FunctionCall{ID: "reply", Name: replyToolName}}
		if event := <-events; event.Kind != aistudio.EventText || event.Text != text {
			t.Fatalf("fragment not forwarded immediately: %+v", event)
		}
	}
	source <- aistudio.Event{Kind: aistudio.EventToolCall, ToolCall: &aistudio.FunctionCall{ID: "reply", Name: replyToolName, Arguments: json.RawMessage(`{"content":"第一段\n第二段"}`)}}
	source <- aistudio.Event{Kind: aistudio.EventUsage, Usage: &aistudio.Usage{TotalTokens: 23}}
	source <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop"}
	close(source)
	count := 0
	for event := range events {
		if event.Kind == aistudio.EventText || event.Kind == aistudio.EventToolCall || event.Kind == aistudio.EventToolCallDelta {
			t.Fatalf("completed reply was sent twice: %+v", event)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("usage or finish was lost: %d events", count)
	}
}
