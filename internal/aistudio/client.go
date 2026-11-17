package aistudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

const (
	// MakerSuiteRPCBase 是 AI Studio RPC 根地址
	MakerSuiteRPCBase = "https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService/"
	// appletControlRPCBase 是 Build 应用文件系统 RPC 根地址
	appletControlRPCBase = "https://alkalimakersuite-pa.clients6.google.com/$rpc/google.alkali.boq.makersuite.makersuiteappletcontrol.proto.MakersuiteAppletControlService/"
	// appletAuthRPCBase 是 Build 应用开发环境认证 RPC 根地址
	appletAuthRPCBase = "https://alkalimakersuiteapplets.pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.applets.AppletAuthService/"
	// JSONProtobufContentType 是 MakerSuite 使用的数组协议媒体类型
	JSONProtobufContentType = "application/json+protobuf"
)

// RPCRequest 描述交给认证传输层的单次 MakerSuite 请求
type RPCRequest struct {
	Method    string
	URL       string
	AccountID string
	RequestID string
	Header    http.Header
	Body      []byte
	Streaming bool
}

// RPCResponse 描述认证传输层返回的响应头和实时正文，Proof 为受保护请求写入的 WAA proof
type RPCResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
	Proof      string
}

// RPCTransport 负责认证、账户租约、Cookie 写回和真实网络发送
type RPCTransport interface {
	Do(context.Context, RPCRequest) (*RPCResponse, error)
}

// ProtectedTransport 原子完成 fresh WAA proof、field 5 写入和同 context 发送
type ProtectedTransport interface {
	DoProtected(context.Context, GenerateRequest, RPCRequest) (*RPCResponse, error)
}

// RequestContext 保存账户运行时提供的协议上下文
type RequestContext struct {
	Timezone string
}

// RequestContextProvider 按账户返回当前协议上下文
type RequestContextProvider interface {
	RequestContext(context.Context, string) (RequestContext, error)
}

// ClientOptions 定义协议客户端的窄依赖
type ClientOptions struct {
	Transport       RPCTransport
	Protected       ProtectedTransport
	ContextProvider RequestContextProvider
	Pages           PageCatalogProvider
	Static          StaticFetcher
}

// Client 实现 AI Studio 私有协议核心
type Client struct {
	transport       RPCTransport
	protected       ProtectedTransport
	contextProvider RequestContextProvider
	pages           PageCatalogProvider
	static          StaticFetcher
	catalogMu       sync.RWMutex
	catalogs        map[string]modelCatalog
	tierMu          sync.RWMutex
	tiers           map[string]BenefitTier
	termsMu         sync.Mutex
	termsAccepted   map[string]struct{}
	disclaimers     map[string]struct{}
	turns           *modelTurnHistory
	assistants      *assistantAppletCache
	frontendMu      sync.Mutex
	// frontend 是按地址解析过的官网前端主程序推荐模型
	frontend map[string]frontendModels
}

// NewClient 创建协议客户端
func NewClient(options ClientOptions) *Client {
	return &Client{
		transport:       options.Transport,
		protected:       options.Protected,
		contextProvider: options.ContextProvider,
		pages:           options.Pages,
		static:          options.Static,
		catalogs:        make(map[string]modelCatalog),
		tiers:           make(map[string]BenefitTier),
		termsAccepted:   make(map[string]struct{}),
		disclaimers:     make(map[string]struct{}),
		turns:           newModelTurnHistory(),
		assistants:      newAssistantAppletCache(),
		frontend:        make(map[string]frontendModels),
	}
}

// RPCError 保存上游状态和协议错误码
type RPCError struct {
	ResponseBody json.RawMessage
	Method       string
	StatusCode   int
	Code         int64
	Message      string
	Metadata     map[string]string
	RetryDelay   time.Duration
	// PerUserQuota 表示写代码助手步骤失败带每用户额度标记
	PerUserQuota bool
}

// ContextOverflow 返回错误是否为上游报告的输入 token 超过模型上限
func (e *RPCError) ContextOverflow() bool {
	return e.StatusCode == http.StatusBadRequest && strings.Contains(e.Message, "input token count exceeds the maximum number of tokens allowed")
}

// accessDenial 返回官网对该 403、协议错误码 7 的消息跳转的拒绝原因，其他错误返回 nil
func (e *RPCError) accessDenial() *camoufoxnative.AccessDenial {
	if e.StatusCode != http.StatusForbidden || e.Code != 7 {
		return nil
	}
	return camoufoxnative.DenialForMessage(e.Message)
}

// Error 返回结构化上游错误
func (e *RPCError) Error() string {
	if denial := e.accessDenial(); denial != nil {
		return fmt.Sprintf("AI Studio %s 返回 HTTP %d、协议错误码 %d: %s（%v）", e.Method, e.StatusCode, e.Code, e.Message, denial)
	}
	if e.Code != 0 {
		return fmt.Sprintf("AI Studio %s 返回 HTTP %d、协议错误码 %d: %s", e.Method, e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("AI Studio %s 返回 HTTP %d: %s", e.Method, e.StatusCode, e.Message)
}

// StatusError 以 gRPC 状态名代替 HTTP 状态码写出 401 或 403 的上游方法、协议错误码与原因
func (e *RPCError) StatusError() string {
	status := "PERMISSION_DENIED"
	if e.StatusCode == http.StatusUnauthorized {
		status = "UNAUTHENTICATED"
	}
	if e.Code != 0 {
		return fmt.Sprintf("AI Studio %s 返回 %s、协议错误码 %d: %s", e.Method, status, e.Code, e.Message)
	}
	return fmt.Sprintf("AI Studio %s 返回 %s: %s", e.Method, status, e.Message)
}

// Unwrap 把官网会跳转拒绝页的错误映射为对应的 camoufoxnative.AccessDenial
func (e *RPCError) Unwrap() error {
	if denial := e.accessDenial(); denial != nil {
		return denial
	}
	return nil
}

// HTTPStatus 返回上游 HTTP 状态
func (e *RPCError) HTTPStatus() int {
	return e.StatusCode
}

// RetryAt 返回上游给出的可重试时刻，没有重试时长时返回零值
func (e *RPCError) RetryAt(now time.Time) time.Time {
	if e.RetryDelay <= 0 {
		return time.Time{}
	}
	return now.Add(e.RetryDelay)
}

func (c *Client) do(ctx context.Context, method string, accountID string, requestID string, body []byte, streaming bool) (*RPCResponse, error) {
	return c.send(ctx, newRPCRequest(method, accountID, requestID, body, streaming))
}

// send 发送 RPC 请求并校验响应状态与媒体类型
func (c *Client) send(ctx context.Context, rpc RPCRequest) (*RPCResponse, error) {
	c.applyBenefitTier(rpc.Method, rpc.AccountID, rpc.Header)
	response, err := c.transport.Do(ctx, rpc)
	if err != nil {
		return nil, fmt.Errorf("发送 AI Studio %s: %w", rpc.Method, err)
	}
	return validateRPCResponse(rpc.Method, response)
}

func (c *Client) doProtected(ctx context.Context, request GenerateRequest, body []byte) (*RPCResponse, error) {
	rpc := newRPCRequest("GenerateContent", request.AccountID, request.ID, body, true)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	if request.ImageRoute {
		// 图像请求使用独立于文本预热页的扩展头默认值
		rpc.Header["X-Goog-Ext-519733851-Bin"] = nil
	}
	response, err := c.protected.DoProtected(ctx, request, rpc)
	if err != nil {
		return nil, fmt.Errorf("发送 AI Studio GenerateContent: %w", err)
	}
	return validateRPCResponse("GenerateContent", response)
}

func newRPCRequest(method string, accountID string, requestID string, body []byte, streaming bool) RPCRequest {
	return RPCRequest{
		Method:    method,
		URL:       MakerSuiteRPCBase + method,
		AccountID: accountID,
		RequestID: requestID,
		Header: http.Header{
			"Content-Type": []string{JSONProtobufContentType},
		},
		Body:      body,
		Streaming: streaming,
	}
}

func validateRPCResponse(method string, response *RPCResponse) (*RPCResponse, error) {
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		raw, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return nil, fmt.Errorf("读取 AI Studio %s 错误响应: %w", method, readErr)
		}
		rpcError := DecodeRPCError(method, response.StatusCode, raw)
		if rpcError.RetryDelay <= 0 {
			value := strings.TrimSpace(response.Header.Get("Retry-After"))
			if delay, err := time.ParseDuration(value + "s"); err == nil && delay > 0 {
				rpcError.RetryDelay = delay
			} else if until, err := http.ParseTime(value); err == nil {
				rpcError.RetryDelay = max(0, time.Until(until))
			}
		}
		return nil, rpcError
	}
	contentType := response.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, JSONProtobufContentType) {
		response.Body.Close()
		return nil, fmt.Errorf("AI Studio %s 返回未识别的 Content-Type %q", method, contentType)
	}
	return response, nil
}

// DecodeRPCError 解析独立状态和流式封装中的上游错误
func DecodeRPCError(method string, statusCode int, raw []byte) *RPCError {
	rpcError := &RPCError{
		Method:     method,
		StatusCode: statusCode,
		Message:    http.StatusText(statusCode),
	}
	var response struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &response) == nil && response.Error != nil {
		rpcError.ResponseBody = append(json.RawMessage(nil), raw...)
		if response.Error.Message != "" {
			rpcError.Message = response.Error.Message
		}
		rpcError.RetryDelay = messageRetryDelay(rpcError.Message)
		return rpcError
	}
	value, err := decodeJSONValue(raw)
	if err != nil {
		return rpcError
	}
	root, err := rawArray(value, "$", value)
	if err != nil || len(root) < 2 || isJSONNull(root[1]) {
		return rpcError
	}
	provider, providerPath := root, "$"
	if bytes.HasPrefix(bytes.TrimSpace(root[1]), []byte("[")) {
		provider, err = rawArray(root[1], "$[1]", value)
		if err != nil || len(provider) < 2 {
			return rpcError
		}
		providerPath = "$[1]"
	}
	if code, err := rawInt64(provider[0], providerPath+"[0]", value); err == nil {
		rpcError.Code = code
	}
	if message, err := rawString(provider[1], providerPath+"[1]", value); err == nil && message != "" {
		rpcError.Message = message
	}
	if len(provider) > 2 && !isJSONNull(provider[2]) {
		decodeRPCErrorMetadata(rpcError, provider[2])
	}
	if rpcError.RetryDelay <= 0 {
		rpcError.RetryDelay = messageRetryDelay(rpcError.Message)
	}
	return rpcError
}

var retryTimePrefix = regexp.MustCompile(`(?i)\b(?:retry|resets?)\s+in\s+`)
var retryTimePart = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(milliseconds?|seconds?|minutes?|hours?|days?|ms|s|m|h|d)\b`)

// messageRetryDelay 支持 retry in 1.5s 和 resets in 18 hours, 8 minutes 等相对等待时间。
func messageRetryDelay(message string) time.Duration {
	message = strings.ToLower(message)
	for _, prefix := range retryTimePrefix.FindAllStringIndex(message, -1) {
		rest := message[prefix[1]:]
		value, _, _ := strings.Cut(rest, " ")
		if delay, err := time.ParseDuration(strings.TrimRight(value, ".,;)")); err == nil && delay > 0 {
			return delay
		}
		var delay time.Duration
		for {
			part := retryTimePart.FindStringSubmatch(rest)
			if part == nil {
				break
			}
			unit, scale := part[2][:1], time.Duration(1)
			if strings.HasPrefix(part[2], "millisecond") || part[2] == "ms" {
				unit = "ms"
			} else if unit == "d" {
				unit, scale = "h", 24
			}
			component, err := time.ParseDuration(part[1] + unit)
			if err != nil || component > (1<<63-1)/scale || delay > (1<<63-1)-component*scale {
				return 0
			}
			delay += component * scale
			rest = strings.TrimLeft(rest[len(part[0]):], ", \t\r\n")
			rest = strings.TrimPrefix(rest, "and ")
		}
		if delay > 0 {
			return delay
		}
	}
	return 0
}

func decodeRPCErrorMetadata(rpcError *RPCError, raw json.RawMessage) {
	var details [][]json.RawMessage
	if err := json.Unmarshal(raw, &details); err != nil {
		return
	}
	for _, detail := range details {
		if len(detail) < 2 {
			continue
		}
		var typeURL string
		if err := json.Unmarshal(detail[0], &typeURL); err != nil {
			continue
		}
		if typeURL == "type.googleapis.com/google.rpc.RetryInfo" {
			var info []json.RawMessage
			if json.Unmarshal(detail[1], &info) == nil && len(info) > 0 {
				rpcError.RetryDelay = max(rpcError.RetryDelay, decodeRPCRetryDuration(info[0]))
			}
			continue
		}
		if typeURL != "type.googleapis.com/google.rpc.ErrorInfo" {
			continue
		}
		var info []json.RawMessage
		if err := json.Unmarshal(detail[1], &info); err != nil || len(info) < 3 {
			continue
		}
		var metadata [][]string
		if err := json.Unmarshal(info[2], &metadata); err != nil {
			continue
		}
		for _, pair := range metadata {
			if len(pair) < 2 || pair[0] == "" {
				continue
			}
			if rpcError.Metadata == nil {
				rpcError.Metadata = make(map[string]string)
			}
			rpcError.Metadata[pair[0]] = pair[1]
		}
	}
}

// decodeRPCRetryDuration 读取 RetryInfo 中的秒与纳秒
func decodeRPCRetryDuration(raw json.RawMessage) time.Duration {
	var fields []json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) == 0 {
		return 0
	}
	var seconds json.Number
	if json.Unmarshal(fields[0], &seconds) != nil {
		return 0
	}
	delay, err := time.ParseDuration(seconds.String() + "s")
	if err != nil || delay < 0 {
		return 0
	}
	if len(fields) > 1 && !isJSONNull(fields[1]) {
		var nanos int64
		if json.Unmarshal(fields[1], &nanos) != nil || nanos < 0 || nanos >= int64(time.Second) {
			return 0
		}
		delay += time.Duration(nanos)
	}
	return delay
}
