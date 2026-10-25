package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// gzipWriters 复用压缩器，避免每个响应重新分配压缩状态
var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

// acceptsGzip 判断客户端是否接受 gzip 编码
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if name, _, _ := strings.Cut(strings.TrimSpace(part), ";"); strings.EqualFold(name, "gzip") {
			return true
		}
	}
	return false
}

// gzipJSONMiddleware 为接受 gzip 的客户端压缩 JSON 响应；事件流、下载文件等其他类型原样透传
func gzipJSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		writer := &gzipJSONWriter{ResponseWriter: w}
		defer writer.close()
		next.ServeHTTP(writer, r)
	})
}

// gzipJSONWriter 在写出响应头时按 Content-Type 决定是否压缩
type gzipJSONWriter struct {
	http.ResponseWriter
	gzip    *gzip.Writer
	started bool
}

func (writer *gzipJSONWriter) WriteHeader(status int) {
	if writer.started {
		return
	}
	writer.started = true
	header := writer.Header()
	if status != http.StatusNoContent && status != http.StatusNotModified &&
		strings.HasPrefix(header.Get("Content-Type"), "application/json") && header.Get("Content-Encoding") == "" {
		header.Del("Content-Length")
		header.Set("Content-Encoding", "gzip")
		header.Add("Vary", "Accept-Encoding")
		writer.gzip = gzipWriters.Get().(*gzip.Writer)
		writer.gzip.Reset(writer.ResponseWriter)
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *gzipJSONWriter) Write(data []byte) (int, error) {
	if !writer.started {
		writer.WriteHeader(http.StatusOK)
	}
	if writer.gzip != nil {
		return writer.gzip.Write(data)
	}
	return writer.ResponseWriter.Write(data)
}

// Unwrap 让 http.ResponseController 直接刷新底层连接，事件流不经过压缩
func (writer *gzipJSONWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *gzipJSONWriter) close() {
	if writer.gzip == nil {
		return
	}
	_ = writer.gzip.Close()
	writer.gzip.Reset(io.Discard)
	gzipWriters.Put(writer.gzip)
}
