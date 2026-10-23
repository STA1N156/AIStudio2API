package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type emptySettingsService struct{ calls int }

func (*emptySettingsService) Models(context.Context) ([]aistudio.Model, error) { return nil, nil }
func (*emptySettingsService) CountTokens(context.Context, aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	return aistudio.TokenCount{}, nil
}
func (*emptySettingsService) State() string                    { return "RUNNING" }
func (*emptySettingsService) ApplyCapacity(int, int, int, int) {}
func (s *emptySettingsService) Generate(context.Context, aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	s.calls++
	events := make(chan aistudio.Event, 1)
	events <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop"}
	close(events)
	return events, nil
}

func TestEmptyOutputSettingControlsRetry(t *testing.T) {
	for _, model := range []string{"test", "抗截断/test"} {
		for _, allow := range []bool{false, true} {
			service := &emptySettingsService{}
			manager := &runtimeManager{current: &runtimeGeneration{service: service}}
			manager.allowEmptyOutput.Store(allow)
			events, err := manager.Generate(context.Background(), aistudio.GenerateRequest{
				Model: model, Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "hello"}}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			for range events {
			}
			want := 2
			if allow || strings.HasPrefix(model, "抗截断/") {
				want = 1
			}
			if service.calls != want {
				t.Fatalf("%s allow=%t: got %d calls, want %d", model, allow, service.calls, want)
			}
		}
	}
}

func TestLogOutputsAreIndependent(t *testing.T) {
	output := &logOutput{dir: t.TempDir()}
	logger := slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: &output.level}))
	if err := output.set(false, false, 20); err != nil {
		t.Fatal(err)
	}
	if logger.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("disabled sinks still format logs")
	}
	if err := output.set(false, true, 20); err != nil {
		t.Fatal(err)
	}
	logger.Info("file-only test")
	if output.console {
		t.Fatal("file logging enabled console logging")
	}
	if err := output.set(false, false, 20); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output.dir, logFileName))
	if err != nil || !strings.Contains(string(data), "file-only test") {
		t.Fatalf("file logging failed: %v", err)
	}
	if err := output.set(true, false, 20); err != nil {
		t.Fatal(err)
	}
	if !output.console || !logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("console toggle did not apply")
	}
}
