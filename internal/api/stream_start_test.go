package api

import (
	"context"
	"encoding/json"
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
				if w.Code != 400 || !strings.Contains(w.Body.String(), `"message":"输出为空"`) {
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
	if err == nil || statusFromError(err) != 400 || err.Error() != "输出为空" {
		t.Fatalf("reasoning-only output should fail with 400: %v", err)
	}
}
