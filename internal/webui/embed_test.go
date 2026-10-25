package webui

import (
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"
)

func TestHandlerCompressesAndCachesAssets(t *testing.T) {
	scripts, err := fs.Glob(Files(), "assets/*.js")
	if err != nil || len(scripts) == 0 {
		t.Skip("管理端尚未构建")
	}
	handler := Handler()
	want, err := fs.ReadFile(Files(), scripts[0])
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/"+scripts[0], nil)
	request.Header.Set("Accept-Encoding", "gzip")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Encoding") != "gzip" ||
		recorder.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset = %d %v", recorder.Code, recorder.Header())
	}
	reader, err := gzip.NewReader(recorder.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil || string(body) != string(want) {
		t.Fatalf("asset body differs: %v", err)
	}

	request = httptest.NewRequest(http.MethodGet, "/"+path.Base(scripts[0]), nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Header().Get("Cache-Control") != "no-cache" || recorder.Header().Get("Content-Encoding") != "" {
		t.Fatalf("spa fallback = %v", recorder.Header())
	}
}
