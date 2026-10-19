package aistudio

import "testing"

func TestModelThinkingSuffix(t *testing.T) {
	budget := int64(8192)
	for _, test := range []struct {
		input, model, effort string
		keepBudget           bool
	}{
		{"gemini-3.8-flash-low", "gemini-3.8-flash", "low", false},
		{"gemini-3.8-flash-medium", "gemini-3.8-flash", "medium", false},
		{"gemini-3.8-flash-HIGH", "gemini-3.8-flash", "high", false},
		{"gemini-3.8-flash-build-high", "gemini-3.8-flash-build", "high", false},
		{"gemini-3.8-flash(high)", "gemini-3.8-flash", "high", false},
		{"gemini-3.8-flash", "gemini-3.8-flash", "low", true},
		{"gemini-3.1-pro-preview", "gemini-3.1-pro-preview", "low", true},
	} {
		t.Run(test.input, func(t *testing.T) {
			model, config, err := ApplyModelSuffix(test.input, GenerationConfig{ReasoningEffort: "low", ThinkingBudget: &budget})
			if err != nil || model != test.model || config.ReasoningEffort != test.effort || (config.ThinkingBudget != nil) != test.keepBudget {
				t.Fatalf("model=%q config=%+v err=%v", model, config, err)
			}
		})
	}
}
