package requestdb

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/sqlitedb"
)

func TestUpgradeRemovesCostIndexAndPreservesUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	log := func(string, string, ...any) {}
	store, err := Open(path, log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE INDEX requests_cost ON requests(time, model, input_tokens, cached_tokens, reasoning_tokens, reply_tokens); CREATE INDEX keep_custom_index ON requests(model, account)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store.Record(Row{ID: "old", Time: now, Model: "gemini-3.8-flash", State: "completed", Status: 200, InputTokens: 1000000, ReplyTokens: 1000, TotalTokens: 1001000})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, log)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for name, want := range map[string]bool{"requests_cost": false, "keep_custom_index": true} {
		var exists bool
		if err := store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='index' AND name=?)`, name).Scan(&exists); err != nil || exists != want {
			t.Fatalf("index %s: exists=%v err=%v", name, exists, err)
		}
	}
	report, err := store.Usage(context.Background(), api.UsageQuery{From: now.Add(-time.Hour), To: now.Add(time.Second), Bucket: time.Hour, Location: time.UTC, Stack: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Requests != 1 || report.Totals.TotalTokens != 1001000 || report.Cost != 0 || report.PreviousCost != 0 || len(report.UnpricedModels) != 0 {
		t.Fatalf("unexpected usage: %+v", report)
	}
	for _, bucket := range report.Buckets {
		if bucket.Cost != 0 {
			t.Fatal("bucket cost was calculated")
		}
	}
}

func TestUpgradePreservesOldBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	db, err := sqlitedb.Open(path, `CREATE TABLE bodies (seq INTEGER PRIMARY KEY, id TEXT UNIQUE, time INTEGER, request TEXT, request_size INTEGER, response TEXT, response_size INTEGER); CREATE TABLE custom_metadata (value TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bodies VALUES (1,'old',1000,'original input',14,'original output',15); INSERT INTO custom_metadata VALUES ('preserved')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path, func(string, string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	body, err := store.RequestBody(context.Background(), "old")
	if err != nil || body.Request != "original input" || body.Response != "original output" || body.RequestSize != 14 || body.ResponseSize != 15 {
		t.Fatalf("old body changed: %+v %v", body, err)
	}
	var value string
	if err := store.db.QueryRow(`SELECT value FROM custom_metadata`).Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("unrelated table changed: %q %v", value, err)
	}
	if err := store.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestBody(context.Background(), "old"); err == nil {
		t.Fatal("clear retained body")
	}
}
