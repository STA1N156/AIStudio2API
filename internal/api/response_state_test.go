package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type gatedStateStore struct {
	mu               sync.Mutex
	states           map[string]SavedState
	writes           [][]string
	blocked          atomic.Bool
	entered, release chan struct{}
}

func (disk *gatedStateStore) Load() []SavedState {
	disk.mu.Lock()
	defer disk.mu.Unlock()
	var states []SavedState
	for _, state := range disk.states {
		states = append(states, state)
	}
	return states
}

func (disk *gatedStateStore) Save(saved []SavedState, removed []string) {
	if disk.blocked.CompareAndSwap(false, true) {
		close(disk.entered)
		<-disk.release
	}
	disk.mu.Lock()
	defer disk.mu.Unlock()
	var ids []string
	for _, state := range saved {
		disk.states[state.ID] = state
		ids = append(ids, state.ID)
	}
	for _, id := range removed {
		delete(disk.states, id)
	}
	disk.writes = append(disk.writes, ids)
}

func TestResponseStateReadsDuringOrderedPersistence(t *testing.T) {
	disk := &gatedStateStore{states: map[string]SavedState{}, entered: make(chan struct{}), release: make(chan struct{})}
	store := newResponseStateStore(disk)
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	state := func(id string) responseState {
		return responseState{
			Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: id}}}},
			Record: &responseRecord{
				Shell: json.RawMessage(fmt.Sprintf(`{"id":%q,"status":"completed"}`, id)),
				Input: []storedItem{{ID: id + "-input", Raw: json.RawMessage(`{"role":"user","content":"hello"}`)}},
			},
		}
	}
	go func() { store.Store("first", responseHistory{}, generationResult{}, state("first")); close(firstDone) }()
	<-disk.entered
	released := false
	defer func() {
		if !released {
			close(disk.release)
		}
	}()
	read := make(chan error, 1)
	go func() {
		if history, ok := store.Load("first"); !ok || len(history.Contents) != 1 {
			read <- fmt.Errorf("missing history during persistence")
			return
		}
		if _, _, ok := store.Response("first", nil); !ok {
			read <- fmt.Errorf("missing response during persistence")
			return
		}
		if items, ok := store.InputItems("first"); !ok || len(items) != 1 {
			read <- fmt.Errorf("missing input during persistence")
			return
		}
		_, err := store.ResolveItems(splitResponsesInput(json.RawMessage(`[{"type":"item_reference","id":"first-input"}]`)))
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reads waited for the blocked disk write")
	}
	go func() {
		store.Store("second", responseHistory{ID: "first"}, generationResult{}, state("second"))
		close(secondDone)
	}()
	close(disk.release)
	released = true
	<-firstDone
	<-secondDone
	if len(disk.writes) != 2 || !slices.Equal(disk.writes[0], []string{"first"}) || !slices.Equal(disk.writes[1], []string{"second"}) {
		t.Fatalf("writes out of order: %v", disk.writes)
	}
	before, _ := store.snapshot()
	if !store.Delete("first", false) {
		t.Fatal("parent deletion failed")
	}
	if before["second"].ParentID != "first" || len(before["second"].Contents) != 1 {
		t.Fatal("parent deletion changed the previous snapshot")
	}
	for _, current := range []*responseStateStore{store, newResponseStateStore(disk)} {
		history, ok := current.Load("second")
		if !ok || len(history.Contents) != 2 || history.Contents[0].Parts[0].Text != "first" || history.Contents[1].Parts[0].Text != "second" {
			t.Fatalf("continuation lost after deletion/reload: %+v", history)
		}
		if _, ok := current.Load("first"); ok {
			t.Fatal("deleted parent restored")
		}
	}
}

func TestResponseStateConcurrentSnapshots(t *testing.T) {
	store := newResponseStateStore(nil)
	store.Store("anchor", responseHistory{}, generationResult{}, responseState{
		Contents: []aistudio.Content{{Role: aistudio.RoleUser, Parts: []aistudio.Part{{Text: "anchor"}}}},
		Record:   &responseRecord{Shell: json.RawMessage(`{"status":"completed"}`)},
	})
	before, _ := store.snapshot()
	start := make(chan struct{})
	var workers sync.WaitGroup
	for reader := range 4 {
		workers.Go(func() {
			<-start
			for range 80 {
				if history, ok := store.Load("anchor"); !ok || history.Contents[0].Parts[0].Text != "anchor" {
					t.Errorf("reader %d lost anchor", reader)
				}
				store.Response("anchor", nil)
				store.InputItems("anchor")
			}
		})
	}
	for writer := range 2 {
		workers.Go(func() {
			<-start
			for i := range 80 {
				id := fmt.Sprintf("%d-%d", writer, i%4)
				store.Store(id, responseHistory{ID: "anchor"}, generationResult{}, responseState{})
				if i%2 == 0 {
					store.Delete(id, false)
				}
			}
		})
	}
	close(start)
	workers.Wait()
	if len(before) != 1 {
		t.Fatal("previous snapshot mutated")
	}
}
