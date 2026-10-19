package requestdb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

func TestClearUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	store, err := Open(path, func(level, message string) { t.Errorf("%s %s", level, message) })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	now := time.Now().UTC()
	store.Record(Row{ID: "before", Time: now, Model: "flash", State: "completed", TotalTokens: 20})
	store.SaveBody(api.RequestBody{ID: "before", Time: now, Request: "input", Response: "output"})
	request := func(handler http.Handler) int {
		r := httptest.NewRequest(http.MethodDelete, "http://localhost/api/usage", nil)
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Origin", "http://localhost")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	protected := api.NewHandler(nil, api.Config{Ledger: store, AdminAuthEnabled: true, AdminUsername: "admin", AdminPassword: "test"})
	if code := request(protected); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated clear status=%d", code)
	}
	if code := request(api.NewHandler(nil, api.Config{Ledger: store})); code != http.StatusNoContent {
		t.Fatalf("clear status=%d", code)
	}
	for _, table := range []string{"requests", "hours", "days", "bodies"} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	store.Record(Row{ID: "after", Time: now, Model: "flash", State: "completed", TotalTokens: 30})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, func(level, message string) { t.Errorf("%s %s", level, message) })
	if err != nil {
		t.Fatal(err)
	}
	report, err := store.Usage(t.Context(), api.UsageQuery{From: now.Add(-time.Hour), To: now.Add(time.Hour), Bucket: time.Hour, Location: time.UTC, Stack: "model"})
	if err != nil || report.Totals.Requests != 1 || report.Totals.TotalTokens != 30 {
		t.Fatalf("new requests after clear: %+v err=%v", report.Totals, err)
	}
}

// TestLedgerUsageAndBodies 验证关闭前排队的记录落盘、当前与上一周期分开汇总、正文只保留最近条数
func TestLedgerUsageAndBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	logs := func(level, message string) { t.Errorf("%s %s", level, message) }
	store, err := Open(path, logs)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store.Record(Row{ID: "a", Time: now.Add(-time.Hour), Model: "flash", State: "completed", Duration: 2 * time.Second, InputTokens: 10, TotalTokens: 30})
	store.Record(Row{ID: "b", Time: now.Add(-2 * time.Hour), Model: "flash", State: "failed", Status: 429, Duration: 4 * time.Second})
	store.Record(Row{ID: "c", Time: now.Add(-3 * time.Hour), Model: "pro", State: "completed", Duration: 6 * time.Second, TotalTokens: 5})
	store.Record(Row{ID: "old", Time: now.Add(-30 * time.Hour), Model: "pro", State: "completed", Duration: time.Second})
	for index := range bodyRetention + 2 {
		store.SaveBody(api.RequestBody{ID: fmt.Sprintf("body-%d", index), Time: now, Request: "in", Response: "out"})
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, logs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err := store.Usage(context.Background(), api.UsageQuery{
		From: now.Add(-24 * time.Hour), To: now, Bucket: time.Hour, Location: time.UTC, Stack: "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	totals := report.Totals
	if totals.Requests != 3 || totals.Failed != 1 || totals.RateLimited != 1 || totals.TotalTokens != 35 || report.Previous.Requests != 1 {
		t.Fatalf("totals=%+v previous=%+v", totals, report.Previous)
	}
	if len(report.Buckets) != 25 || math.Abs(totals.Duration.P95MS-6000) > 60 || totals.Duration.AvgMS != 4000 {
		t.Fatalf("buckets=%d duration=%+v", len(report.Buckets), totals.Duration)
	}
	bucketed := int64(0)
	for _, bucket := range report.Buckets {
		bucketed += bucket.Requests
	}
	models := report.Groups["model"]
	if bucketed != 3 || len(models) != 2 || models[0].Key != "flash" || models[0].Requests != 2 {
		t.Fatalf("bucketed=%d models=%+v", bucketed, models)
	}
	if _, err := store.RequestBody(context.Background(), "body-1"); !errors.Is(err, api.ErrRequestBodyNotFound) {
		t.Fatalf("超出保留条数的正文仍存在: %v", err)
	}
	body, err := store.RequestBody(context.Background(), fmt.Sprintf("body-%d", bodyRetention+1))
	if err != nil || body.Request != "in" || body.Response != "out" {
		t.Fatalf("body=%+v err=%v", body, err)
	}
}
