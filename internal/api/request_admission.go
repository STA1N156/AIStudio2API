package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// requestAdmissionMiddleware 在解析大请求体之前排队，容量沿用账号池的总并发上限。
func requestAdmissionMiddleware(limits func() (int, time.Duration), next http.Handler) http.Handler {
	if limits == nil {
		return next
	}
	var mu sync.Mutex
	active := 0
	changed := make(chan struct{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		generation := path == "/v1/chat/completions" || path == "/v1/messages" || path == "/v1/responses" ||
			strings.HasSuffix(path, "/interactions") || strings.HasPrefix(path, "/v1beta/models/") ||
			path == "/v1/images/generations" || path == "/v1/audio/speech"
		if r.Method != http.MethodPost || !generation {
			next.ServeHTTP(w, r)
			return
		}
		_, timeout := limits()
		waitCtx := r.Context()
		var cancel context.CancelFunc
		if timeout > 0 {
			waitCtx, cancel = context.WithTimeout(waitCtx, timeout)
			defer cancel()
		}
		for {
			capacity, _ := limits()
			mu.Lock()
			if active < max(1, capacity) {
				active++
				mu.Unlock()
				break
			}
			wait := changed
			mu.Unlock()
			select {
			case <-wait:
			case <-waitCtx.Done():
				err := waitCtx.Err()
				SetAccessLogError(r.Context(), err)
				if shouldWriteRequestError(r, err) {
					switch protocolForRequest(r) {
					case "gemini":
						writeGeminiRequestError(w, err)
					case "anthropic":
						writeAnthropicRequestError(w, err)
					default:
						writeOpenAIRequestError(w, err)
					}
				}
				return
			}
		}
		defer func() {
			mu.Lock()
			active--
			close(changed)
			changed = make(chan struct{})
			mu.Unlock()
		}()
		if cancel != nil {
			cancel()
		}
		// 排队期限不传入生成阶段，保留模型自动续写的独立超时。
		next.ServeHTTP(w, r)
	})
}
