package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestRelayBackpressurePreservesOutput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		source, output := make(chan aistudio.Event), make(chan aistudio.Event, 16)
		var sent atomic.Int64
		go func() {
			defer close(source)
			for range 100 {
				source <- aistudio.Event{Kind: aistudio.EventText, Text: "text"}
				sent.Add(1)
			}
		}()
		go relayEvents(ctx, source, output)
		synctest.Wait()
		if sent.Load() > 17 {
			t.Fatalf("unbounded producer: %d", sent.Load())
		}
		count := 0
		for range output {
			count++
		}
		if count != 100 {
			t.Fatalf("lost events: %d", count)
		}
		source, output = make(chan aistudio.Event), make(chan aistudio.Event)
		go relayEvents(ctx, source, output)
		cancel()
		if _, open := <-output; open {
			t.Fatal("cancelled relay remained open")
		}
	})
}

func TestCompactChatPreservesReasoningToolsAndUsage(t *testing.T) {
	events := make(chan aistudio.Event, 6)
	input := []aistudio.Event{
		{Kind: aistudio.EventReasoning, Text: "thinking"},
		{Kind: aistudio.EventText, Text: "hello"},
		{Kind: aistudio.EventToolCall, ToolCall: &aistudio.FunctionCall{Name: "tool", Arguments: []byte(`{}`)}},
		{Kind: aistudio.EventUsage, Usage: &aistudio.Usage{InputTokens: 7, OutputTokens: 2, TotalTokens: 9}},
		{Kind: aistudio.EventFinish, FinishReason: "stop"},
	}
	for _, event := range input {
		events <- event
	}
	close(events)
	emitted := 0
	result, err := consumeChatStreamEvents(context.Background(), events, func(aistudio.Event) error { emitted++; return nil }, nil)
	if err != nil || emitted != len(input) || len(result.toolCalls) != 1 || result.usage.TotalTokens != 9 {
		t.Fatalf("lost stream data: %+v, %v", result, err)
	}
	if result.text.Len() != 0 || result.reasoning.Len() != 0 {
		t.Fatal("stream duplicated text in builders")
	}
	text, _ := renderedContent(result.events)
	if text != "hello" {
		t.Fatalf("citation text changed: %q", text)
	}
}

func TestAdmissionWaitsBeforeHandlerAndCancels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}, 1), make(chan struct{})
		var calls atomic.Int64
		handler := requestAdmissionMiddleware(func() (int, time.Duration) { return 1, time.Minute }, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			calls.Add(1)
			entered <- struct{}{}
			<-release
		}))
		firstDone := make(chan struct{})
		go func() {
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", nil))
			close(firstDone)
		}()
		<-entered
		ctx, cancel := context.WithCancel(context.Background())
		secondDone := make(chan struct{})
		go func() {
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx))
			close(secondDone)
		}()
		time.Sleep(time.Millisecond)
		if calls.Load() != 1 {
			t.Fatal("waiting request reached body parsing")
		}
		cancel()
		<-secondDone
		close(release)
		<-firstDone
	})
}

func TestAdmissionDoesNotShortenGenerationDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		handler := requestAdmissionMiddleware(func() (int, time.Duration) { return 1, time.Minute }, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if r.Context() != ctx {
				t.Fatal("queue deadline replaced the generation context")
			}
			time.Sleep(2 * time.Minute)
			if err := r.Context().Err(); err != nil {
				t.Fatalf("generation was cut short by the queue timeout: %v", err)
			}
		}))
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx))
	})
}
