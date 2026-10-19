package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type httpStatusError interface {
	HTTPStatus() int
}

type statusCodeError interface {
	StatusCode() int
}

type errorCodeProvider interface {
	ErrorCode() string
}

type emptyOutputError struct{}

func (emptyOutputError) Error() string {
	return "模型输出为空，请降低思考强度，并检查是否含有过多敏感内容"
}
func (emptyOutputError) HTTPStatus() int { return http.StatusBadRequest }

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request body must contain one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// streamHeaders 写入并刷新流式响应头
func streamHeaders(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	return http.NewResponseController(w).Flush()
}

// writeSSE 写入具名事件并传播缓冲刷新错误
func writeSSE(w http.ResponseWriter, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	frame := make([]byte, 0, len(event)+len(data)+16)
	if event != "" {
		frame = append(append(append(frame, "event: "...), event...), '\n')
	}
	frame = append(append(append(frame, "data: "...), data...), "\n\n"...)
	if _, err := w.Write(frame); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

// writeSSEText 写入文本事件并刷新网络缓冲
func writeSSEText(w http.ResponseWriter, data string) error {
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

// writeGeminiHeartbeat 发送不触发事件的空行并传播连接错误
func writeGeminiHeartbeat(w http.ResponseWriter) error {
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

// writeSSEHeartbeat 发送心跳并传播连接错误
func writeSSEHeartbeat(w http.ResponseWriter) error {
	if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

func statusFromError(err error) int {
	var rpcError *aistudio.RPCError
	if errors.As(err, &rpcError) && rpcError.StatusCode >= 400 && rpcError.StatusCode <= 599 {
		return rpcError.StatusCode
	}
	if errors.Is(err, context.Canceled) {
		return http.StatusServiceUnavailable
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	var notReady *aistudio.AccountsNotReadyError
	if errors.As(err, &notReady) {
		return http.StatusServiceUnavailable
	}
	if errors.Is(err, aistudio.ErrNoEligibleAccount) {
		return http.StatusBadRequest
	}
	if errors.Is(err, aistudio.ErrInvalidArgument) {
		return http.StatusBadRequest
	}
	if errors.Is(err, aistudio.ErrModelNotFound) {
		return http.StatusNotFound
	}
	if isUnverifiedProtocolError(err) {
		return http.StatusBadRequest
	}
	var statusErr httpStatusError
	if errors.As(err, &statusErr) {
		return validHTTPStatus(statusErr.HTTPStatus())
	}
	var codeErr statusCodeError
	if errors.As(err, &codeErr) {
		return validHTTPStatus(codeErr.StatusCode())
	}
	return http.StatusBadGateway
}

// upstreamErrorPayload 保留上游错误对象，仅替换指定 400 的提示。
func upstreamErrorPayload(err error) (map[string]any, bool) {
	var empty emptyOutputError
	if errors.As(err, &empty) {
		return map[string]any{"error": map[string]any{
			"code": http.StatusBadRequest, "message": empty.Error(), "status": "EMPTY_MODEL_OUTPUT",
		}}, true
	}
	var rpc *aistudio.RPCError
	if !errors.As(err, &rpc) {
		return nil, false
	}
	message := publicErrorMessage(err)
	var payload map[string]any
	for _, raw := range []json.RawMessage{rpc.ResponseBody, json.RawMessage(rpc.Message)} {
		if json.Unmarshal(raw, &payload) == nil && payload["error"] != nil {
			if detail, ok := payload["error"].(map[string]any); ok && message != rpc.Message && rpc.Message != "" {
				detail["message"] = message
			}
			return payload, true
		}
	}
	details := make([]any, 0, 2)
	if len(rpc.Metadata) > 0 {
		details = append(details, map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": rpc.Metadata})
	}
	if rpc.RetryDelay > 0 {
		details = append(details, map[string]any{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": strconv.FormatFloat(rpc.RetryDelay.Seconds(), 'f', -1, 64) + "s"})
	}
	providerError := map[string]any{"code": statusFromError(err), "message": message, "status": geminiErrorStatus(err)}
	if len(details) > 0 {
		providerError["details"] = details
	}
	return map[string]any{"error": providerError}, true
}

func publicErrorMessage(err error) string {
	var rpc *aistudio.RPCError
	if errors.As(err, &rpc) && rpc.Message != "" {
		if rpc.StatusCode == http.StatusBadRequest && strings.Contains(rpc.Message, "131072") {
			return "预设或其他内容被谷歌拦截，并试图路由至降智模型，已帮您拦截降智路由，请自行排查预设或其他内容"
		}
		return rpc.Message
	}
	return err.Error()
}

func writeUpstreamError(w http.ResponseWriter, err error) bool {
	payload, ok := upstreamErrorPayload(err)
	if !ok {
		return false
	}
	setRetryAfter(w, err)
	setAccessLogResponseError(w, publicErrorMessage(err))
	writeJSON(w, statusFromError(err), payload)
	return true
}

// shouldWriteRequestError 判断仍在线的客户端是否需要收到结构化错误
func shouldWriteRequestError(r *http.Request, err error) bool {
	return err != nil && (!errors.Is(err, context.Canceled) || r.Context().Err() == nil)
}

func isUnverifiedProtocolError(err error) bool {
	var unverified *aistudio.UnverifiedProtocolError
	return errors.As(err, &unverified)
}

func validHTTPStatus(status int) int {
	if status >= 400 && status <= 599 {
		return status
	}
	return http.StatusBadGateway
}

func codeFromError(err error, defaultCode string) string {
	var codeErr errorCodeProvider
	if errors.As(err, &codeErr) && codeErr.ErrorCode() != "" {
		return codeErr.ErrorCode()
	}
	return defaultCode
}

func writeAuthError(w http.ResponseWriter, r *http.Request) {
	switch protocolForRequest(r) {
	case "anthropic":
		writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "Invalid API Key")
	case "gemini":
		writeGeminiError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Invalid API Key")
	case "admin":
		writeAdminError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API Key")
	default:
		writeOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API Key")
	}
}

func protocolForRequest(r *http.Request) string {
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1beta/"), r.URL.Path == "/v1/interactions":
		return "gemini"
	case strings.HasPrefix(r.URL.Path, "/v1/messages"), r.Header.Get("Anthropic-Version") != "":
		return "anthropic"
	case strings.HasPrefix(r.URL.Path, "/api/"):
		return "admin"
	default:
		return "openai"
	}
}

func writeOpenAIError(w http.ResponseWriter, status int, code string, message string) {
	setAccessLogResponseError(w, message)
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"message": message,
		"type":    openAIErrorType(status, code),
		"param":   nil,
		"code":    code,
	}})
}

func openAIErrorType(status int, code string) string {
	if status == http.StatusBadRequest || status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound || code == "invalid_request" || code == "invalid_api_key" || code == "model_not_found" {
		return "invalid_request_error"
	}
	if code == "rate_limit_exceeded" {
		return "requests"
	}
	return "server_error"
}

func writeAnthropicError(w http.ResponseWriter, status int, errorType string, message string) {
	setAccessLogResponseError(w, message)
	writeJSON(w, status, map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    errorType,
			"message": message,
		},
	})
}

func writeGeminiError(w http.ResponseWriter, status int, statusName string, message string) {
	setAccessLogResponseError(w, message)
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code":    status,
		"message": message,
		"status":  statusName,
	}})
}

// writeOpenAIRequestError 以 OpenAI 错误体写出请求失败，可重试时带 Retry-After
func writeOpenAIRequestError(w http.ResponseWriter, err error) {
	if writeUpstreamError(w, err) {
		return
	}
	setRetryAfter(w, err)
	writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
}

// writeAnthropicRequestError 以 Anthropic 错误体写出请求失败，可重试时带 Retry-After
func writeAnthropicRequestError(w http.ResponseWriter, err error) {
	if writeUpstreamError(w, err) {
		return
	}
	setRetryAfter(w, err)
	writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), publicErrorMessage(err))
}

// contextOverflow 判断错误是否为上游的上下文超限
func contextOverflow(err error) bool {
	var rpc *aistudio.RPCError
	return errors.As(err, &rpc) && rpc.ContextOverflow()
}

// writeGeminiRequestError 以 Gemini 错误体写出请求失败，可重试时带 Retry-After 与 RetryInfo
func writeGeminiRequestError(w http.ResponseWriter, err error) {
	if writeUpstreamError(w, err) {
		return
	}
	status := statusFromError(err)
	payload := map[string]any{"code": status, "message": err.Error(), "status": geminiErrorStatus(err)}
	if seconds := setRetryAfter(w, err); seconds > 0 {
		payload["details"] = []any{map[string]any{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": strconv.FormatInt(seconds, 10) + "s"}}
	}
	setAccessLogResponseError(w, err.Error())
	writeJSON(w, status, map[string]any{"error": payload})
}

// setRetryAfter 在错误给出可重试时刻时写入 Retry-After 秒数并返回它，否则返回 0
func setRetryAfter(w http.ResponseWriter, err error) int64 {
	var retry interface{ RetryAt(time.Time) time.Time }
	if !errors.As(err, &retry) {
		return 0
	}
	now := time.Now()
	at := retry.RetryAt(now)
	if !at.After(now) {
		return 0
	}
	seconds := int64(math.Ceil(at.Sub(now).Seconds()))
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	return seconds
}

func writeAdminError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{
		"code":    code,
		"message": message,
	}})
}

func openAIErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "request_canceled"
	}
	if errors.Is(err, aistudio.ErrModelNotFound) {
		return "model_not_found"
	}
	var notReady *aistudio.AccountsNotReadyError
	if errors.As(err, &notReady) {
		return "account_unavailable"
	}
	if errors.Is(err, aistudio.ErrNoEligibleAccount) {
		return "account_required"
	}
	if errors.Is(err, aistudio.ErrInvalidArgument) {
		return "invalid_request"
	}
	if isUnverifiedProtocolError(err) {
		return "invalid_request"
	}
	if contextOverflow(err) {
		return "context_length_exceeded"
	}
	switch statusFromError(err) {
	case http.StatusNotFound:
		return codeFromError(err, "model_not_found")
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	default:
		return codeFromError(err, "upstream_error")
	}
}

func anthropicErrorType(err error) string {
	if isUnverifiedProtocolError(err) {
		return "invalid_request_error"
	}
	return anthropicStatusErrorType(statusFromError(err))
}

// anthropicStatusErrorType 返回 HTTP 状态对应的 Anthropic 错误类型
func anthropicStatusErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusGatewayTimeout:
		return "timeout_error"
	case 529:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

func geminiErrorStatus(err error) string {
	switch statusFromError(err) {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	default:
		return "INTERNAL"
	}
}
