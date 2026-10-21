package aistudio

import (
	"strings"
	"testing"
)

func TestOutputLimitDefaultsAndMissingCatalog(t *testing.T) {
	for _, test := range []struct {
		name             string
		requested        *int64
		limit, continued int64
		want             any
	}{
		{name: "catalog default", limit: 32768, want: int64(32768)},
		{name: "client zero means default", requested: new(int64(0)), limit: 32768, want: int64(32768)},
		{name: "missing catalog keeps client limit", requested: new(int64(8192)), want: int64(8192)},
		{name: "missing catalog omits default"},
		{name: "missing catalog with zero", requested: new(int64(0))},
		{name: "known limit still clamps", requested: new(int64(64000)), limit: 32768, want: int64(32768)},
		{name: "continuation limit preserved", requested: new(int64(64000)), limit: 32768, continued: 1048576, want: int64(64000)},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire, err := encodeGenerationConfig(GenerationConfig{MaxOutputTokens: test.requested}, GenerationDefaults{MaxOutputTokens: test.limit, ContinuedOutputTokens: test.continued})
			if err != nil {
				t.Fatal(err)
			}
			if wire[3] != test.want {
				t.Fatalf("output limit = %v, want %v", wire[3], test.want)
			}
			build, err := encodeBuildGenerationConfig(GenerationConfig{MaxOutputTokens: test.requested}, GenerationDefaults{MaxOutputTokens: test.limit, ContinuedOutputTokens: test.continued})
			if err != nil {
				t.Fatal(err)
			}
			wantBuild := test.want
			if test.continued > 0 {
				wantBuild = test.limit // Build 不走 Playground 的自动续写。
			}
			if build["maxOutputTokens"] != wantBuild {
				t.Fatalf("Build output limit = %v, want %v", build["maxOutputTokens"], wantBuild)
			}
		})
	}
	_, err := encodeGenerationConfig(GenerationConfig{MaxOutputTokens: new(int64(-1))}, GenerationDefaults{MaxOutputTokens: 32768})
	if err == nil || !strings.Contains(err.Error(), "不能为负数") {
		t.Fatalf("negative value error: %v", err)
	}
}
