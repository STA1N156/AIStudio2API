package requestdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/sqlitedb"
)

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
