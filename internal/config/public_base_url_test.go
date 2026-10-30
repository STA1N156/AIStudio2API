package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicBaseURL(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", " https://ai.example.com/ ")
	path := filepath.Join(t.TempDir(), ".env")
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PublicBaseURL != "https://ai.example.com" || loaded.Validate() != nil {
		t.Fatalf("public base url = %q", loaded.PublicBaseURL)
	}
	if err := loaded.Save(path); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(saved), "PUBLIC_BASE_URL=https://ai.example.com\n") {
		t.Fatalf("saved env = %s", saved)
	}
	data, _ := json.Marshal(loaded)
	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.PublicBaseURL != loaded.PublicBaseURL {
		t.Fatalf("JSON round trip = %q, %v", decoded.PublicBaseURL, err)
	}

	for _, value := range []string{"", "http://127.0.0.1:2048", "https://ai.example.com"} {
		if err := validatePublicBaseURL(value); err != nil {
			t.Fatalf("%q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"ai.example.com", "ftp://ai.example.com", "https://ai.example.com/v1", "https://", "https://user@ai.example.com"} {
		if validatePublicBaseURL(value) == nil {
			t.Fatalf("%q accepted", value)
		}
	}
}
