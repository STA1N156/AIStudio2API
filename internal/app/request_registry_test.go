package app

import (
	"strconv"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

func TestRequestSnapshotOnConnectAndBacklog(t *testing.T) {
	registry := newRequestRegistry(t.Context())
	backlogged := registry.subscribe(t.Context())
	registry.start(aistudio.GenerateRequest{ID: "finished"}, func() {})
	if event, ok := backlogged.next(); !ok || event.Type != "request" {
		t.Fatal("expected the first delta to reach the subscriber")
	}
	registry.finish("finished", "completed")
	for i := range adminRequestCompactAt {
		registry.start(aistudio.GenerateRequest{ID: strconv.Itoa(i)}, func() {})
	}
	checkSnapshot := func(event api.AdminEvent, want int) {
		t.Helper()
		requests, ok := event.Data.([]api.AdminRequest)
		if event.Type != "requests" || !ok || len(requests) != want {
			t.Fatalf("expected a %d-request snapshot, got %q with %d requests", want, event.Type, len(requests))
		}
		for _, request := range requests {
			if request.ID == "finished" {
				t.Fatal("finished request remained in the active snapshot")
			}
		}
	}
	event, ok := backlogged.next()
	if !ok {
		t.Fatal("expected a snapshot after compaction")
	}
	checkSnapshot(event, adminRequestCompactAt-1)
	event, ok = backlogged.next()
	if !ok || event.Type != "request" || event.Data.(api.AdminRequest).ID != strconv.Itoa(adminRequestCompactAt-1) {
		t.Fatal("expected the next delta after the snapshot")
	}

	fresh := registry.subscribe(t.Context())
	events := registry.activateSubscriber(fresh, nil, nil)
	select {
	case event := <-events:
		checkSnapshot(event, adminRequestCompactAt)
	case <-time.After(time.Second):
		t.Fatal("expected the full snapshot on connect")
	}
}
