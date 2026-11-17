package api

import (
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
			if w.Code != 400 || !strings.Contains(w.Body.String(), err.Error()) {
				t.Fatalf("unexpected rejection response: %d %s", w.Code, w.Body.String())
			}
		}
	}
	if statusFromError(errors.New("transport failed")) != http.StatusBadGateway {
		t.Fatal("transport errors must remain 502")
	}
}
