package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestOutputSettingsDefaultsAndRoundTrip(t *testing.T) {
	cfg := Default()
	if !cfg.EmptyOutputError || cfg.LogConsole {
		t.Fatal("unexpected output defaults")
	}
	t.Setenv("EMPTY_OUTPUT_ERROR", "false")
	t.Setenv("LOG_CONSOLE", "true")
	path := filepath.Join(t.TempDir(), ".env")
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.EmptyOutputError || !loaded.LogConsole {
		t.Fatal("environment switches ignored")
	}
	if err := loaded.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.EmptyOutputError || !decoded.LogConsole {
		t.Fatal("JSON round trip lost switches")
	}
}
