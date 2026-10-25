package requestdb

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/api"
)

func TestFailureRateExclusionMatchesOnlySpecified400s(t *testing.T) {
	const empty = "模型输出为空，请降低思考强度，并检查是否含有过多敏感内容"
	const blocked = "预设或其他内容被谷歌拦截，并试图路由至降智模型，已帮您拦截降智路由，请自行排查预设或其他内容"
	const upstream = "The input token count exceeds the maximum number of tokens allowed 131072."
	for _, test := range []struct {
		name, message string
		status        int
		want          bool
	}{
		{"empty", empty, 400, true},
		{"wrapped empty", "status_code=400, " + empty, 400, true},
		{"blocked", blocked, 400, true},
		{"historical upstream", "[original: INVALID_ARGUMENT] " + upstream + " (qos=CRITICAL)", 400, true},
		{"other 400", "Request contains an invalid argument.", 400, false},
		{"other input limit", "The input token count exceeds the maximum number of tokens allowed 65536.", 400, false},
		{"unrelated number", "Invalid value 131072", 400, false},
		{"quota", "Resource has been exhausted", 429, false},
		{"not a 400", empty, 500, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := api.ExcludeFromFailureRate(test.status, test.message); got != test.want {
				t.Fatalf("excluded = %v, want %v", got, test.want)
			}
		})
	}
}

func TestUsageRateExclusionsPreserveRecordsAndRollups(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "usage.db"), func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	day := time.Now().UTC().Add(-48 * time.Hour).Truncate(24 * time.Hour)
	at := day.Add(90 * time.Minute)
	rows := []Row{
		{ID: "success", Status: 200, State: "completed"},
		{ID: "empty", Status: 400, State: "failed", Error: "模型输出为空，请降低思考强度，并检查是否含有过多敏感内容"},
		{ID: "blocked", Status: 400, State: "failed", Error: "预设或其他内容被谷歌拦截，并试图路由至降智模型，已帮您拦截降智路由，请自行排查预设或其他内容"},
		{ID: "historical", Status: 400, State: "failed", Error: "The input token count exceeds the maximum number of tokens allowed 131072."},
		{ID: "invalid", Status: 400, State: "failed", Error: "Request contains an invalid argument."},
		{ID: "quota", Status: 429, State: "failed", Error: "Resource has been exhausted"},
		{ID: "cancelled", Status: 499, State: "cancelled"},
	}
	var batch []write
	for i := range rows {
		rows[i].Time, rows[i].Model, rows[i].Account = at, "model", "account"
		rows[i].Protocol, rows[i].Channel = "openai-chat", "playground"
		rows[i].InputTokens, rows[i].ReplyTokens, rows[i].TotalTokens = 4, 6, 10
		batch = append(batch, write{row: &rows[i]})
	}
	if err := store.write(batch); err != nil {
		t.Fatal(err)
	}
	want := func(t *testing.T, stats api.UsageStats) {
		t.Helper()
		if stats.Requests != 7 || stats.Succeeded != 1 || stats.Failed != 5 || stats.Canceled != 1 || stats.RateExcluded != 3 || stats.TotalTokens != 70 {
			t.Fatalf("unexpected stats: %+v", stats)
		}
	}
	ctx := context.Background()
	for _, test := range []struct {
		name     string
		from, to time.Time
		bucket   time.Duration
	}{
		{"raw", at.Add(-time.Minute), at.Add(time.Minute), time.Minute},
		{"hour", day.Add(time.Hour), day.Add(2 * time.Hour), time.Hour},
		{"day", day, day.Add(24 * time.Hour), 24 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			report, err := store.Usage(ctx, api.UsageQuery{From: test.from, To: test.to, Bucket: test.bucket, Stack: "model"})
			if err != nil {
				t.Fatal(err)
			}
			want(t, report.Totals)
			want(t, report.Groups["account"][0].UsageStats)
			want(t, report.Pairs[0].UsageStats)
			skipped, err := store.Usage(ctx, api.UsageQuery{From: test.from, To: test.to, Bucket: test.bucket, Stack: "model", SkipPairs: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(skipped.Pairs) != 0 || skipped.Totals.Requests != report.Totals.Requests {
				t.Fatalf("skip pairs = %d pairs, %d requests", len(skipped.Pairs), skipped.Totals.Requests)
			}
			var excluded int64
			for _, bucket := range report.Buckets {
				excluded += bucket.RateExcluded
			}
			if excluded != 3 {
				t.Fatalf("bucket exclusions = %d", excluded)
			}
		})
	}
	previous, err := store.Usage(ctx, api.UsageQuery{From: day.Add(24 * time.Hour), To: day.Add(48 * time.Hour), Bucket: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	want(t, previous.Previous)
	filtered, err := store.Usage(ctx, api.UsageQuery{From: day, To: day.Add(24 * time.Hour), Bucket: time.Hour, Filters: api.UsageFilters{"state": {"failed"}}})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Totals.Requests != 5 || filtered.Totals.RateExcluded != 3 {
		t.Fatalf("filtered exclusions: %+v", filtered.Totals)
	}
	records, err := store.Records(ctx, api.UsageRecordQuery{From: day, To: day.Add(24 * time.Hour), Limit: 20})
	if err != nil || len(records.Items) != len(rows) {
		t.Fatalf("records changed: count=%d err=%v", len(records.Items), err)
	}
	for _, record := range records.Items {
		if record.ID == "empty" && (record.Status != 400 || record.State != "failed" || record.Error != rows[1].Error) {
			t.Fatalf("original error was changed: %+v", record)
		}
	}
}

func TestRecordingCanPauseAndResumeWithoutLosingHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	store, err := Open(path, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.SetBodyCapture(true)
	record := func(id string) {
		now := time.Now().UTC()
		store.Record(Row{ID: id, Time: now, Status: 200, State: "completed", TotalTokens: 10})
		store.SaveBody(api.RequestBody{ID: id, Time: now, Request: "hello", Response: "OK"})
	}
	record("before")
	store.SetRecording(false)
	if store.Recording() || store.BodyCapture() {
		t.Fatal("recording did not stop")
	}
	record("paused")
	store.SetRecording(true)
	if !store.Recording() || !store.BodyCapture() {
		t.Fatal("recording did not resume")
	}
	record("after")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	var count, paused int
	if err := store.db.QueryRow("SELECT COUNT(*), COUNT(CASE WHEN id='paused' THEN 1 END) FROM requests").Scan(&count, &paused); err != nil {
		t.Fatal(err)
	}
	if count != 2 || paused != 0 {
		t.Fatalf("unexpected request history: count=%d paused=%d", count, paused)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM bodies").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("unexpected saved bodies: %d", count)
	}
}
