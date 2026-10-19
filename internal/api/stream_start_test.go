package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type delayedStreamService struct {
	event aistudio.Event
	tail  []aistudio.Event
}

func (s delayedStreamService) Models(context.Context) ([]aistudio.Model, error) {
	return nil, nil
}

func (s delayedStreamService) CountTokens(context.Context, aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	return aistudio.TokenCount{}, nil
}

func (s delayedStreamService) Generate(ctx context.Context, _ aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	events := make(chan aistudio.Event, 4)
	go func() {
		defer close(events)
		events <- aistudio.Event{Kind: aistudio.EventText}
		events <- aistudio.Event{Kind: aistudio.EventUsage, Usage: &aistudio.Usage{InputTokens: 7, TotalTokens: 7}}
		time.Sleep(12 * time.Second)
		select {
		case events <- s.event:
		case <-ctx.Done():
			return
		}
		for _, event := range s.tail {
			events <- event
		}
		events <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop"}
	}()
	return events, nil
}

type timedStreamRecorder struct {
	*httptest.ResponseRecorder
	firstHeader time.Time
}

func (w *timedStreamRecorder) WriteHeader(status int) {
	if w.firstHeader.IsZero() {
		w.firstHeader = time.Now()
	}
	w.ResponseRecorder.WriteHeader(status)
}

func TestStreamStartDelayedErrorKeepsHTTPStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &server{thoughtSignatures: newThoughtSignatureStore(), service: delayedStreamService{event: aistudio.Event{
			Kind: aistudio.EventError, Err: &aistudio.RPCError{StatusCode: 403, Message: "permission denied"},
		}}}
		w := &timedStreamRecorder{ResponseRecorder: httptest.NewRecorder()}
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","stream":true,"messages":[{"role":"user","content":"hello"}]}`))
		started := time.Now()
		s.handleChatCompletions(w, r)
		if w.Code != http.StatusForbidden || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("expected plain HTTP 403, got %d %s: %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
		}
		if w.firstHeader.Sub(started) != 12*time.Second {
			t.Fatalf("response started early: %s", w.firstHeader.Sub(started))
		}
	})
}

func TestStreamStartFirstChunkContainsOutput(t *testing.T) {
	for _, item := range []struct {
		kind  aistudio.EventKind
		field string
	}{{aistudio.EventText, "content"}, {aistudio.EventReasoning, "reasoning_content"}} {
		t.Run(item.field, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &server{thoughtSignatures: newThoughtSignatureStore(), service: delayedStreamService{
					event: aistudio.Event{Kind: item.kind, Text: "hello"},
					tail:  []aistudio.Event{{Kind: aistudio.EventText, Text: "reply"}},
				}}
				w := &timedStreamRecorder{ResponseRecorder: httptest.NewRecorder()}
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hello"}]}`))
				started := time.Now()
				s.handleChatCompletions(w, r)
				if w.firstHeader.Sub(started) != 12*time.Second {
					t.Fatalf("response started early: %s", w.firstHeader.Sub(started))
				}
				line, _, _ := strings.Cut(w.Body.String(), "\n")
				var chunk struct {
					Choices []struct{ Delta map[string]any }
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
					t.Fatal(err)
				}
				if len(chunk.Choices) != 1 || chunk.Choices[0].Delta[item.field] != "hello" || chunk.Choices[0].Delta["role"] != "assistant" {
					t.Fatalf("first chunk is not actual output: %s", line)
				}
				if !strings.Contains(w.Body.String(), `"prompt_tokens":7`) {
					t.Fatal("prefetched usage was lost")
				}
			})
		})
	}
}

func TestEmptyOutputReturns400(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &server{thoughtSignatures: newThoughtSignatureStore(), service: delayedStreamService{
					event: aistudio.Event{Kind: aistudio.EventText, Text: " \n\t"},
				}}
				w := httptest.NewRecorder()
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(fmt.Sprintf(
					`{"model":"test","stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream)))
				s.handleChatCompletions(w, r)
				if w.Code != 400 || !strings.Contains(w.Body.String(), `"message":"模型输出为空，请降低思考强度，并检查是否含有过多敏感内容"`) || !strings.Contains(w.Body.String(), `"status":"EMPTY_MODEL_OUTPUT"`) {
					t.Fatalf("expected empty-output HTTP 400, got %d: %s", w.Code, w.Body.String())
				}
			})
		})
	}
}

func TestEmptyOutputDoesNotRejectToolsOrMedia(t *testing.T) {
	for _, event := range []aistudio.Event{
		{Kind: aistudio.EventText, Text: "reply"},
		{Kind: aistudio.EventToolCall, ToolCall: &aistudio.FunctionCall{Name: "tool", Arguments: json.RawMessage(`{}`)}},
		{Kind: aistudio.EventMedia, Media: &aistudio.Media{MIME: "image/png", Data: []byte{1}}},
		{Kind: aistudio.EventExecutableCode, ExecutableCode: &aistudio.ExecutableCode{Code: "print(1)"}},
	} {
		var result generationResult
		if err := result.apply(event); err != nil {
			t.Fatal(err)
		}
		if err := result.apply(aistudio.Event{Kind: aistudio.EventFinish}); err != nil {
			t.Fatalf("valid %s was rejected: %v", event.Kind, err)
		}
	}
	var result generationResult
	_ = result.apply(aistudio.Event{Kind: aistudio.EventReasoning, Text: "thinking only"})
	err := result.apply(aistudio.Event{Kind: aistudio.EventFinish})
	if err == nil || statusFromError(err) != 400 || err.Error() != "模型输出为空，请降低思考强度，并检查是否含有过多敏感内容" {
		t.Fatalf("reasoning-only output should fail with 400: %v", err)
	}
}

func TestUpstreamErrorsKeepStatusAndMessage(t *testing.T) {
	writers := []struct {
		name  string
		write func(http.ResponseWriter, error)
	}{
		{"OpenAI", writeOpenAIRequestError},
		{"Anthropic", writeAnthropicRequestError},
		{"Gemini", writeGeminiRequestError},
	}
	for _, status := range []int{400, 401, 403, 404, 429, 500, 503, 529} {
		for _, writer := range writers {
			t.Run(fmt.Sprintf("%s/%d", writer.name, status), func(t *testing.T) {
				err := errors.Join(&aistudio.RPCError{StatusCode: status, Message: "original upstream message"}, context.DeadlineExceeded)
				w := httptest.NewRecorder()
				writer.write(w, err)
				var payload struct {
					Error struct {
						Code    int
						Message string
						Status  string
					}
				}
				if decodeErr := json.Unmarshal(w.Body.Bytes(), &payload); decodeErr != nil {
					t.Fatal(decodeErr)
				}
				if w.Code != status || payload.Error.Code != status || payload.Error.Message != "original upstream message" || payload.Error.Status == "" {
					t.Fatalf("upstream error was changed: HTTP %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
	for _, writer := range writers {
		w := httptest.NewRecorder()
		writer.write(w, emptyOutputError{})
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"status":"EMPTY_MODEL_OUTPUT"`) || !strings.Contains(w.Body.String(), (emptyOutputError{}).Error()) {
			t.Fatalf("%s empty error not aligned: %s", writer.name, w.Body.String())
		}
	}
}

func TestJSONUpstreamErrorIsPreserved(t *testing.T) {
	raw := []byte(`{"error":{"code":404,"message":"missing model","status":"NOT_FOUND","details":[{"reason":"MODEL_NOT_FOUND"}]},"requestId":"test-id"}`)
	err := aistudio.DecodeRPCError("GenerateContent", 404, raw)
	w := httptest.NewRecorder()
	writeOpenAIRequestError(w, err)
	var expected, actual any
	_ = json.Unmarshal(raw, &expected)
	_ = json.Unmarshal(w.Body.Bytes(), &actual)
	expectedJSON, _ := json.Marshal(expected)
	actualJSON, _ := json.Marshal(actual)
	if w.Code != 404 || string(expectedJSON) != string(actualJSON) || err.Message != "missing model" {
		t.Fatalf("upstream JSON was changed: HTTP %d %s", w.Code, w.Body.String())
	}
}

func TestInputTokenLimitErrorMessage(t *testing.T) {
	const message = "The input token count exceeds the maximum number of tokens allowed 131072."
	const notice = "预设或其他内容被谷歌拦截，并试图路由至降智模型，已帮您拦截降智路由，请自行排查预设或其他内容"
	for _, test := range []struct {
		name, message, want string
		status              int
		jsonBody            bool
	}{
		{"exact", message, notice, 400, false},
		{"wrapped", "[original: beyond::dependency::INVALID_ARGUMENT] " + message + " (qos=CRITICAL)", notice, 400, false},
		{"json", message, notice, 400, true},
		{"other 400", "invalid argument", "invalid argument", 400, false},
		{"other limit", "The input token count exceeds the maximum number of tokens allowed 65536.", "The input token count exceeds the maximum number of tokens allowed 65536.", 400, false},
		{"other status", message, message, 429, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := &aistudio.RPCError{StatusCode: test.status, Code: 3, Message: test.message}
			if test.jsonBody {
				raw, _ := json.Marshal(map[string]any{"error": map[string]any{"code": test.status, "message": test.message, "status": "INVALID_ARGUMENT"}, "requestId": "test-id"})
				err = aistudio.DecodeRPCError("GenerateContent", test.status, raw)
			}
			w := httptest.NewRecorder()
			writeOpenAIRequestError(w, err)
			var payload struct {
				Error struct {
					Code    int
					Message string
				}
				RequestID string `json:"requestId"`
			}
			if decodeErr := json.Unmarshal(w.Body.Bytes(), &payload); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if w.Code != test.status || payload.Error.Code != test.status || payload.Error.Message != test.want {
				t.Fatalf("unexpected error response: HTTP %d %s", w.Code, w.Body.String())
			}
			if err.Message != test.message || test.jsonBody && payload.RequestID != "test-id" {
				t.Fatal("original error or other response fields were changed")
			}
		})
	}
}

func TestStreamErrorKeepsUpstreamCode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &server{thoughtSignatures: newThoughtSignatureStore(), service: delayedStreamService{
			event: aistudio.Event{Kind: aistudio.EventText, Text: "partial response"},
			tail:  []aistudio.Event{{Kind: aistudio.EventError, Err: &aistudio.RPCError{StatusCode: 404, Message: "missing model"}}},
		}}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","stream":true,"messages":[{"role":"user","content":"hello"}]}`))
		s.handleChatCompletions(w, r)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `"code":404`) || !strings.Contains(body, `"message":"missing model"`) || !strings.Contains(body, "data: [DONE]") {
			t.Fatalf("stream error was changed or not terminated: HTTP %d %s", w.Code, body)
		}
	})
}
