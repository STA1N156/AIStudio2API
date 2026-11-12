package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestModelTiersPersistIncludingEmptySelection(t *testing.T) {
	cfg := Default()
	cfg.ModelTiers = map[string][]string{"argon": {"Ultra", "Pro"}, "blocked": {}}
	path := filepath.Join(t.TempDir(), ".env")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || !reflect.DeepEqual(loaded.ModelTiers, cfg.ModelTiers) {
		t.Fatalf("tier rules did not survive reload: %v, %v", loaded.ModelTiers, err)
	}
}
