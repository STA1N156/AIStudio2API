package aistudio

import (
	"encoding/json"
	"fmt"
	"strings"
)

// decodeReplyArguments 只解析内部 output_reply 的 content，不把未完成的参数当作工具调用执行。
func (d *BuildStreamDecoder) decodeReplyArguments(part buildResponsePart, raw []byte) ([]Event, error) {
	call := part.FunctionCall
	invalid := func(message string) error {
		return &ProtocolEvidenceError{Method: buildProxyStreamedMethod, Path: "$chunk.functionCall", Detail: message, Raw: cloneRaw(raw)}
	}
	if d.replyCall == nil {
		if call.Name != "output_reply" {
			return nil, invalid("收到非内部回复工具的参数分片")
		}
		d.replyIndex++
		id := call.ID
		if id == "" {
			id = fmt.Sprintf("reply_stream_%d", d.replyIndex)
		}
		d.replyCall = &FunctionCall{ID: id, Name: call.Name, ThoughtSignature: part.ThoughtSignature}
		d.replyText.Reset()
	}
	if call.Name != "" && call.Name != d.replyCall.Name || call.ID != "" && call.ID != d.replyCall.ID {
		return nil, invalid("回复工具的参数分片不属于同一次调用")
	}
	var delta strings.Builder
	for _, argument := range call.PartialArgs {
		if argument.JSONPath != "$.content" || argument.StringValue == nil {
			return nil, invalid("回复工具分片必须为 $.content 字符串")
		}
		delta.WriteString(*argument.StringValue)
	}
	d.replyText.WriteString(delta.String())
	if args := strings.TrimSpace(string(call.Args)); args != "" && args != "null" && args != "{}" {
		var full struct {
			Content *string `json:"content"`
		}
		if json.Unmarshal(call.Args, &full) != nil || full.Content == nil || !strings.HasPrefix(*full.Content, d.replyText.String()) {
			return nil, invalid("完整回复工具参数与分片不一致")
		}
		suffix := (*full.Content)[d.replyText.Len():]
		delta.WriteString(suffix)
		d.replyText.WriteString(suffix)
	}
	var events []Event
	if delta.Len() > 0 {
		call := *d.replyCall
		events = append(events, Event{Kind: EventToolCallDelta, Text: delta.String(), ToolCall: &call})
	}
	if !call.WillContinue {
		completed := *d.replyCall
		completed.Arguments, _ = json.Marshal(map[string]string{"content": d.replyText.String()})
		events = append(events, Event{Kind: EventToolCall, ToolCall: &completed, ThoughtSignature: completed.ThoughtSignature})
		d.replyCall = nil
		d.replyText.Reset()
	}
	return events, nil
}
