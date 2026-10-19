package app

import (
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/config"
)

func TestMappedThinkingSuffix(t *testing.T) {
	service := &trackedService{modelMapping: []config.ModelAlias{{Alias: "fast", Model: "gemini-3.8-flash(low)"}}}
	for input, expected := range map[string]string{
		"fast":                 "gemini-3.8-flash(low)",
		"fast-medium":          "gemini-3.8-flash-medium",
		"models/fast-high":     "gemini-3.8-flash-high",
		"fast(high)":           "gemini-3.8-flash(high)",
		"gemini-3.8-flash":     "gemini-3.8-flash",
		"gemini-3.8-flash-low": "gemini-3.8-flash-low",
	} {
		if result := service.mappedModel(input); result != expected {
			t.Errorf("mappedModel(%q)=%q, want %q", input, result, expected)
		}
	}
}
