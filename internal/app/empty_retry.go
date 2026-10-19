package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/google/uuid"
)

const inputToolName = "read_user_input"

// hasCurrentInputToolResult 只识别最新一轮中编号配对、来源正确的输入包装。
func hasCurrentInputToolResult(contents []aistudio.Content) bool {
	pending := make(map[string]bool)
	for index := len(contents) - 1; index >= 0; index-- {
		content := contents[index]
		if content.Role == aistudio.RoleAssistant {
			for _, part := range content.Parts {
				if call := part.FunctionCall; call != nil && call.Name == inputToolName && pending[call.ID] {
					return true
				}
			}
			continue
		}
		if content.Role != aistudio.RoleUser && content.Role != aistudio.RoleTool {
			continue
		}
		hasResult := false
		for _, part := range content.Parts {
			result := part.FunctionResult
			if result == nil {
				continue
			}
			hasResult = true
			if result.ID == "" || result.Name != "" && result.Name != inputToolName {
				continue
			}
			var data map[string]any
			if json.Unmarshal(result.Content, &data) != nil {
				continue
			}
			// OpenAI/Anthropic 的字符串工具结果会放在 result 中。
			if _, ok := data["source"]; !ok {
				for _, key := range []string{"result", "output"} {
					if text, ok := data[key].(string); ok {
						if json.Unmarshal([]byte(text), &data) != nil {
							data = nil
						}
						break
					}
				}
			}
			if _, ok := data["text"].(string); ok && data["source"] == "user_input" {
				pending[result.ID] = true
			}
		}
		if !hasResult {
			return false
		}
	}
	return false
}

func latestUserInput(contents []aistudio.Content) int {
	for index := len(contents) - 1; index >= 0; index-- {
		content := contents[index]
		if content.Role != aistudio.RoleUser && content.Role != aistudio.RoleTool {
			continue
		}
		if content.Role == aistudio.RoleUser && len(content.Parts) == 0 {
			return index
		}
		for _, part := range content.Parts {
			if part.FunctionResult == nil {
				return index
			}
		}
	}
	return -1
}

// wrapLatestUserInput 保留历史、多媒体和抗截断说明，只把最新原文放入工具结果。
func wrapLatestUserInput(request *aistudio.GenerateRequest) bool {
	if hasCurrentInputToolResult(request.Contents) {
		return false
	}
	index := latestUserInput(request.Contents)
	if index < 0 {
		return false
	}
	var text strings.Builder
	parts := make([]aistudio.Part, 0, len(request.Contents[index].Parts))
	for _, part := range request.Contents[index].Parts {
		if part.Text != "" && !part.Thought && part.Text != replyToolInstruction+"\n" {
			if text.Len() == 0 {
				parts = append(parts, aistudio.Part{Text: "读取 read_user_input 的返回内容，作为本轮用户输入继续回复。"})
			}
			text.WriteString(part.Text)
		} else {
			parts = append(parts, part)
		}
	}
	if text.Len() == 0 {
		return false
	}
	content, _ := json.Marshal(map[string]string{"source": "user_input", "text": text.String()})
	callID := "call_" + uuid.NewString()
	contents := slices.Clone(request.Contents)
	contents[index].Parts = parts
	request.Contents = slices.Insert(contents, index+1,
		aistudio.Content{Role: aistudio.RoleAssistant, Parts: []aistudio.Part{{FunctionCall: &aistudio.FunctionCall{
			ID: callID, Name: inputToolName, Arguments: json.RawMessage(`{}`),
		}}}},
		aistudio.Content{Role: aistudio.RoleTool, Parts: []aistudio.Part{{FunctionResult: &aistudio.FunctionResult{
			ID: callID, Name: inputToolName, Content: content,
		}}}},
	)
	return true
}

// generateWithEmptyRetry 即时转发内容；仅正常结束且没有正文/工具/媒体时包装重试一次。
func generateWithEmptyRetry(ctx context.Context, request aistudio.GenerateRequest, generate func(context.Context, aistudio.GenerateRequest) (<-chan aistudio.Event, error)) (<-chan aistudio.Event, error) {
	ctx, cancel := context.WithCancel(ctx)
	source, err := generate(ctx, request)
	if err != nil {
		cancel()
		return nil, err
	}
	out := make(chan aistudio.Event, 8)
	go func() {
		defer close(out)
		defer cancel()
		send := func(event aistudio.Event) bool {
			select {
			case out <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		var previous *aistudio.Usage
		for attempt := 0; attempt < 2; attempt++ {
			var usage aistudio.Event
			var finish *aistudio.Event
			var whitespace []aistudio.Event
			hasOutput := false
			for {
				var event aistudio.Event
				var ok bool
				select {
				case <-ctx.Done():
					return
				case event, ok = <-source:
				}
				if !ok {
					break
				}
				if event.Kind == aistudio.EventUsage {
					usage = event
					continue
				}
				if event.Kind == aistudio.EventFinish {
					finish = &event
					continue
				}
				hasOutput = hasOutput || event.HasOutput()
				if !hasOutput && event.Kind == aistudio.EventText {
					whitespace = append(whitespace, event)
					continue
				}
				if hasOutput {
					for _, pending := range whitespace {
						if !send(pending) {
							return
						}
					}
					whitespace = nil
				}
				if !send(event) || event.Kind == aistudio.EventError {
					return
				}
			}
			if usage.Usage != nil {
				value := *usage.Usage
				if previous != nil {
					value.InputTokens += previous.InputTokens
					value.CachedTokens += previous.CachedTokens
					value.OutputTokens += previous.OutputTokens
					value.ReasoningTokens += previous.ReasoningTokens
					value.ToolTokens += previous.ToolTokens
					value.TotalTokens += previous.TotalTokens
				}
				usage.Usage = &value
				previous = &value
			} else if previous != nil {
				usage = aistudio.Event{Kind: aistudio.EventUsage, Usage: previous}
			}
			if finish != nil && !hasOutput && attempt == 0 && wrapLatestUserInput(&request) {
				source, err = generate(ctx, request)
				if err != nil {
					if usage.Usage != nil {
						send(usage)
					}
					send(aistudio.Event{Kind: aistudio.EventError, Err: err})
					return
				}
				continue
			}
			if usage.Usage != nil && !send(usage) {
				return
			}
			if finish != nil {
				send(*finish)
			}
			return
		}
	}()
	return out, nil
}
