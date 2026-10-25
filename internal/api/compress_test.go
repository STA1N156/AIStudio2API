package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGzipJSONMiddleware(t *testing.T) {
	handler := gzipJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: 1\n\n")
			_ = http.NewResponseController(w).Flush()
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"hello": "world"})
	}))

	request := httptest.NewRequest(http.MethodGet, "/json", nil)
	request.Header.Set("Accept-Encoding", "br, gzip;q=0.8")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("json encoding = %q", recorder.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(recorder.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil || string(body) != "{\"hello\":\"world\"}\n" {
		t.Fatalf("json body = %q, %v", body, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/events", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Header().Get("Content-Encoding") != "" || recorder.Body.String() != "data: 1\n\n" || !recorder.Flushed {
		t.Fatalf("event stream = %q %q flushed=%v", recorder.Header().Get("Content-Encoding"), recorder.Body.String(), recorder.Flushed)
	}

	request = httptest.NewRequest(http.MethodGet, "/json", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Header().Get("Content-Encoding") != "" {
		t.Fatal("compressed without Accept-Encoding")
	}
}
