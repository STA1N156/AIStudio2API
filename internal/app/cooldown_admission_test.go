package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestConfirmedCooldownBypassesOccupiedDispatchQueue(t *testing.T) {
	a := &aistudio.Account{ID: "a", Config: aistudio.DefaultAccountConfig("a@example.invalid"), State: aistudio.AccountReady,
		Models: []aistudio.Model{{ID: "flash", Methods: []string{"generateContent"}}}}
	p := aistudio.NewAccountPool([]*aistudio.Account{a}, 1)
	sel := aistudio.AccountSelection{ModelID: "flash", Method: "generateContent"}
	now := time.Now()
	until := now.Add(time.Hour)
	if err := p.MarkCooldownIfGeneration("a", "flash", p.ModelAccessGeneration("a"), now, until, "quota"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ClassifyCandidates(context.Background(), sel, nil); err != nil {
		t.Fatal(err)
	}
	d := newDispatchQueue()
	head := d.join(dispatchKey(sel))
	defer d.leave(head)
	s := &trackedService{pool: p, workers: &accountWorkerManager{dispatch: d}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := make(chan error, 128)
	for range cap(results) {
		go func() {
			var n int
			var waited time.Duration
			_, err := s.acquireWarmLease(ctx, sel, false, &n, &waited)
			results <- err
		}()
	}
	for range cap(results) {
		var e *aistudio.AllCoolingError
		if err := <-results; !errors.As(err, &e) || e.HTTPStatus() != 429 || !e.Until.Equal(until) {
			t.Fatalf("expected fast 429 with reset time, got %v", err)
		}
	}
	if err := p.ClearCooldown("a", "flash"); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkCooldownIfGeneration("a", "flash", p.ModelAccessGeneration("a"), time.Now(), time.Now().Add(30*time.Second), "quota"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ClassifyCandidates(context.Background(), sel, nil); err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	var failures int
	var waited time.Duration
	if _, err := s.acquireWarmLease(short, sel, false, &failures, &waited); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("short cooldown should still wait its turn: %v", err)
	}
}

func TestAntiTruncationSystemTailOnWire(t *testing.T) {
	for _, tc := range []struct {
		model, toolMode string
		guard           bool
	}{
		{"gemini-4-carbon-high", "none", false}, {"抗截断/gemini-4-carbon-high", "none", true}, {"抗截断/gemini-4-carbon-high", "", true},
	} {
		request := aistudio.GenerateRequest{Model: tc.model, System: []string{"user system", " second system"}, Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "original input"}}}}}
		request.Tools.ToolConfig.Mode = tc.toolMode
		prepareAntiTruncation(&request)
		request.System = append(request.System, " later tool instruction")
		_, body, err := aistudio.EncodeBuildGenerateRequest(request, aistudio.GenerationDefaults{}, false, false)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			SystemInstruction struct{ Parts []struct{ Text string } }
			Contents          json.RawMessage
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		var system string
		for _, part := range wire.SystemInstruction.Parts {
			system += part.Text
		}
		if !strings.HasPrefix(system, "user system second system") || strings.HasSuffix(system, "later tool instruction\n"+antiTruncationSystemTail) != tc.guard ||
			strings.Contains(string(wire.Contents), antiTruncationSystemTail) || !strings.Contains(string(wire.Contents), "original input") {
			t.Fatalf("%s: bad system or contents: %s %s", tc.model, system, wire.Contents)
		}
	}
}
