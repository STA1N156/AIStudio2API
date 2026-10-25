// Package webui 提供内嵌管理端静态资源
package webui

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

//go:embed dist
var embedded embed.FS

// Files 返回管理端构建产物文件系统
func Files() fs.FS {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return dist
}

// Handler 返回管理端静态文件处理器
func Handler() http.Handler {
	files := Files()
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		panic(err)
	}
	return &spaHandler{
		files:  files,
		static: http.FileServer(http.FS(files)),
		index:  index,
	}
}

type spaHandler struct {
	files  fs.FS
	static http.Handler
	index  []byte
	// gzipped 缓存文本资源压缩后的内容，首次请求时生成
	gzipped sync.Map
}

// compressible 是值得 gzip 压缩的文本资源扩展名
var compressible = map[string]bool{".js": true, ".css": true, ".html": true, ".svg": true, ".json": true}

// gzipFile 返回静态文件压缩后的内容，读取失败时返回 nil
func (handler *spaHandler) gzipFile(name string) []byte {
	if cached, ok := handler.gzipped.Load(name); ok {
		return cached.([]byte)
	}
	data, err := fs.ReadFile(handler.files, name)
	if err != nil {
		return nil
	}
	var buffer bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	_, _ = writer.Write(data)
	_ = writer.Close()
	cached, _ := handler.gzipped.LoadOrStore(name, buffer.Bytes())
	return cached.([]byte)
}

// acceptsGzip 判断客户端是否接受 gzip 编码
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if name, _, _ := strings.Cut(strings.TrimSpace(part), ";"); strings.EqualFold(name, "gzip") {
			return true
		}
	}
	return false
}

// ServeHTTP 服务静态资源并为普通页面路径返回 SPA 入口
func (handler *spaHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestPath := path.Clean(request.URL.Path)
	if reservedPath(requestPath) {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.NotFound(writer, request)
		return
	}

	name := strings.TrimPrefix(requestPath, "/")
	if name == "." {
		name = "index.html"
	}
	if _, err := fs.Stat(handler.files, name); err == nil {
		// 构建产物文件名带内容哈希，可长期缓存；入口页每次校验以拿到新版本
		if strings.HasPrefix(name, "assets/") {
			writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			writer.Header().Set("Cache-Control", "no-cache")
		}
		if compressible[path.Ext(name)] && acceptsGzip(request) {
			if data := handler.gzipFile(name); data != nil {
				writer.Header().Set("Content-Encoding", "gzip")
				writer.Header().Add("Vary", "Accept-Encoding")
				http.ServeContent(writer, request, name, time.Time{}, bytes.NewReader(data))
				return
			}
		}
		handler.static.ServeHTTP(writer, request)
		return
	}
	if request.Method == http.MethodHead {
		http.NotFound(writer, request)
		return
	}

	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(handler.index)
}

// reservedPath 判断路径是否属于服务 API
func reservedPath(requestPath string) bool {
	for _, prefix := range []string{"/api", "/v1", "/v1beta", "/health", "/media"} {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return false
}
