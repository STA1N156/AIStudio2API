package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestGenerationRejectionsReturn400(t *testing.T) {
	cases := []error{
		aistudio.RequiredToolCallError{},
		&aistudio.ContentPolicyError{Reason: "prohibited_content", Message: "policy detail"},
		&aistudio.PromptFeedbackError{Reason: "safety", Message: "input detail"},
	}
	for _, err := range cases {
		wrapped := fmt.Errorf("generation: %w", err)
		for _, write := range []func(http.ResponseWriter, error){writeOpenAIRequestError, writeAnthropicRequestError, writeGeminiRequestError} {
			w := httptest.NewRecorder()
			write(w, wrapped)
			if w.Code != 400 || !strings.Contains(w.Body.String(), public400Message(400, err.Error())) {
				t.Fatalf("unexpected rejection response: %d %s", w.Code, w.Body.String())
			}
		}
	}
	if statusFromError(errors.New("transport failed")) != http.StatusBadGateway {
		t.Fatal("transport errors must remain 502")
	}
}

func TestPublic400Messages(t *testing.T) {
	rpc := func(message string) *aistudio.RPCError {
		return &aistudio.RPCError{StatusCode: 400, Code: 3, Message: message}
	}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"output policy", &aistudio.ContentPolicyError{Reason: "prohibited_content", Message: "policy detail"}, "输入内容违反了Google内容政策，请切换抗截断模型以解决"},
		{"input policy", &aistudio.PromptFeedbackError{Reason: "prohibited_content", Message: "policy detail"}, "输入内容违反了Google内容政策，请切换抗截断模型以解决"},
		{"argument", rpc("Request contains an invalid argument."), "请求参数错误，请核对客户端请求参数"},
		{"tool order", rpc("Please ensure that function call turn comes immediately after a user turn or after a function response turn."), "工具调用顺序错误，工具轮次需在用户轮次/另一工具轮次之后"},
		{"context limit", rpc("The input token count exceeds the maximum number of tokens allowed 1048576."), "输入内容超出模型最大上下文1048576"},
		{"other policy", &aistudio.ContentPolicyError{Reason: "other", Message: "This request was blocked by Gemini's filters. Please try rephrasing your prompt."}, "请求含有敏感内容，被Google安全过滤器封锁，请切换抗截断模型以解决"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			original := test.err.Error()
			for _, withBody := range []bool{false, true} {
				if upstream, ok := test.err.(*aistudio.RPCError); ok && withBody {
					upstream.ResponseBody, _ = json.Marshal(map[string]any{"error": map[string]any{
						"code": 400, "message": upstream.Message, "status": "INVALID_ARGUMENT",
						"details": []any{map[string]any{"reason": "original-detail"}},
					}})
				}
				for _, write := range []func(http.ResponseWriter, error){writeOpenAIRequestError, writeAnthropicRequestError, writeGeminiRequestError, writeOpenAISSEError} {
					w := httptest.NewRecorder()
					write(w, test.err)
					var body struct {
						Error struct{ Message string }
					}
					if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(w.Body.String(), "data: "))), &body); err != nil {
						t.Fatal(err)
					}
					if body.Error.Message != test.want {
						t.Fatalf("customer message = %q, want %q", body.Error.Message, test.want)
					}
				}
				if got := geminiErrorPayload(test.err, 0)["message"]; got != test.want {
					t.Fatalf("Gemini stream message = %q, want %q", got, test.want)
				}
				if got := interactionErrorEvent(test.err, "event-1")["error"].(map[string]any)["message"]; got != test.want {
					t.Fatalf("Interactions stream message = %q, want %q", got, test.want)
				}
			}
			if test.err.Error() != original || statusFromError(test.err) != 400 {
				t.Fatal("internal error or status changed")
			}
		})
	}
	if got := public400Message(500, "Request contains an invalid argument."); got != "Request contains an invalid argument." {
		t.Fatalf("non-400 error changed: %q", got)
	}
	if got := public400Message(400, "Other invalid input"); got != "Other invalid input" {
		t.Fatalf("unmatched error changed: %q", got)
	}
}
