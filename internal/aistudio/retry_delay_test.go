package aistudio

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMessageRetryDelay(t *testing.T) {
	for _, test := range []struct {
		message string
		want    time.Duration
	}{
		{"You've reached your usage limit for this model (resets in 18 hours, 8 minutes). Retry with an alternative Gemini model.", 18*time.Hour + 8*time.Minute},
		{"Resets in 1 day, 2 hours and 3 minutes.", 26*time.Hour + 3*time.Minute},
		{"reset in 1 hour", time.Hour},
		{"retry in 1.5s.", 1500 * time.Millisecond},
		{"retry in 1m30s", 90 * time.Second},
		{"retry in 2 seconds, 500 milliseconds", 2500 * time.Millisecond},
		{"resets in 0 hours, 8 minutes)", 8 * time.Minute},
		{"Resets in 12 minutes and 5 seconds", 12*time.Minute + 5*time.Second},
		{"resets in 999999999999999999 days", 0},
		{"resets in -5 minutes", 0},
		{"retry in 0s", 0},
		{"resets in 2 months", 0},
		{"You've reached your usage limit.", 0},
	} {
		t.Run(test.message, func(t *testing.T) {
			if got := messageRetryDelay(test.message); got != test.want {
				t.Fatalf("delay=%s, want %s", got, test.want)
			}
		})
	}
}

func TestResetMessageControlsCooldown(t *testing.T) {
	message := "You've reached your usage limit for this model (resets in 18 hours, 8 minutes)."
	jsonBody, _ := json.Marshal(map[string]any{"error": map[string]string{"message": message}})
	rpcBody, _ := json.Marshal([]any{8, message, nil})
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for _, raw := range [][]byte{jsonBody, rpcBody} {
		err := DecodeRPCError("GenerateContent", 429, raw)
		cooldown, ok := QuotaCooldownForError(err, now)
		if !ok || cooldown.Escalate || !cooldown.Until.Equal(now.Add(18*time.Hour+8*time.Minute)) {
			t.Fatalf("unexpected cooldown: %+v", cooldown)
		}
		if err.Message != message || err.StatusCode != 429 {
			t.Fatal("upstream error changed")
		}
	}
	metadataBody, _ := json.Marshal([]any{8, message, []any{
		[]any{"type.googleapis.com/google.rpc.RetryInfo", []any{[]any{90, 0}}},
	}})
	if err := DecodeRPCError("GenerateContent", 429, metadataBody); err.RetryDelay != 90*time.Second {
		t.Fatalf("structured retry time lost priority: %s", err.RetryDelay)
	}
}
