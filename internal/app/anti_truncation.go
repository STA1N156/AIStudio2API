package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

const antiTruncationPrefix = "抗截断/"
const replyToolName = "output_reply"
const replyToolInstruction = "（本次回复的全部内容必须通过 output_reply 工具的 content 字段提交，保留原有格式，不要在普通消息中重复输出，不要在正文提及这个工具。）"

// antiTruncationModels 只为支持函数调用的聊天模型提供独立入口，不改动上游目录。
func antiTruncationModels(models []aistudio.Model) []aistudio.Model {
	result := slices.Clone(models)
	for _, model := range models {
		if !model.Capabilities["chat_model"] || !model.Capabilities["function_declarations"] || strings.HasPrefix(model.ID, antiTruncationPrefix) {
			continue
		}
		model.ID = antiTruncationPrefix + model.ID
		model.Name = antiTruncationPrefix + model.Name
		result = append(result, model)
	}
	return result
}

// prepareAntiTruncation 在本次请求中加入回复工具，客户端已有的工具选择优先。
func prepareAntiTruncation(request *aistudio.GenerateRequest) bool {
	model := strings.TrimPrefix(request.Model, "models/")
	if !strings.HasPrefix(model, antiTruncationPrefix) {
		return false
	}
	request.Model = strings.TrimPrefix(model, antiTruncationPrefix)
	// 客户端拥有的回复工具仍原样返回，但最新输入也需要包装。
	mode := request.Tools.ToolConfig.Mode
	if mode == "none" || mode == "required" || len(request.Tools.ToolConfig.AllowedFunctionNames) > 0 || slices.ContainsFunc(request.Tools.Functions, func(function aistudio.FunctionDeclaration) bool {
		return function.Name == replyToolName
	}) {
		wrapLatestUserInput(request)
		return false
	}
	request.Tools.Functions = append(slices.Clone(request.Tools.Functions), aistudio.FunctionDeclaration{
		Name:        replyToolName,
		Description: "将本次回复交给聊天界面显示。遵守现有输出规则，正文及需要附带的全部内容等全部放入 content。",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"content":{"type":"string","description":"本次回复的原文，保留原有格式；作为 JSON 字符串正确转义。"}},"required":["content"]}`),
	})
	request.Tools.ToolConfig.Mode = "required"
	if len(request.Tools.Functions) == 1 {
		request.Tools.ToolConfig.AllowedFunctionNames = []string{replyToolName}
		request.Tools.StreamReplyArguments = !request.Unary && (strings.HasPrefix(request.Model, "gemini-3") || strings.HasPrefix(request.Model, "gemini-4"))
	}
	if !strings.Contains(request.System, replyToolInstruction) {
		request.System += "\n\n" + replyToolInstruction
	}
	if last := latestUserInput(request.Contents); last >= 0 {
		request.Contents = slices.Clone(request.Contents)
		request.Contents[last].Parts = append([]aistudio.Part{{Text: replyToolInstruction + "\n"}}, request.Contents[last].Parts...)
	}
	wrapLatestUserInput(request)
	return true
}

// antiTruncationEvents 将内部回复工具还原为正文，真实思考、客户端工具、用量和错误原样转发。
func antiTruncationEvents(ctx context.Context, source <-chan aistudio.Event) <-chan aistudio.Event {
	out := make(chan aistudio.Event, 8)
	go func() {
		defer close(out)
		streamed := make(map[string]*strings.Builder)
		for {
			var event aistudio.Event
			select {
			case <-ctx.Done():
				return
			case next, ok := <-source:
				if !ok {
					return
				}
				event = next
			}
			if event.Kind == aistudio.EventToolCallDelta && event.ToolCall != nil && event.ToolCall.Name == replyToolName {
				id := event.ToolCall.ID
				if streamed[id] == nil {
					streamed[id] = &strings.Builder{}
				}
				streamed[id].WriteString(event.Text)
				event.Kind, event.ToolCall = aistudio.EventText, nil
			}
			if event.Kind == aistudio.EventToolCall && event.ToolCall != nil && event.ToolCall.Name == replyToolName {
				var reply struct {
					Content *string `json:"content"`
				}
				if json.Unmarshal(event.ToolCall.Arguments, &reply) != nil || reply.Content == nil || strings.TrimSpace(*reply.Content) == "" {
					continue
				}
				text := *reply.Content
				if previous := streamed[event.ToolCall.ID]; previous != nil {
					delete(streamed, event.ToolCall.ID)
					if !strings.HasPrefix(text, previous.String()) {
						// 已发送的分片无法撤回，保留它们，不再重复输出冲突正文。
						continue
					}
					text = text[previous.Len():]
					if text == "" {
						continue
					}
				}
				event.Kind, event.Text, event.ToolCall = aistudio.EventText, text, nil
				event.ThoughtSignature = ""
			}
			select {
			case <-ctx.Done():
				return
			case out <- event:
			}
			if event.Kind == aistudio.EventError {
				return
			}
		}
	}()
	return out
}
