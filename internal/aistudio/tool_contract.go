package aistudio

import (
	"fmt"
	"slices"
	"strings"
)

// prepareToolRequest 校验客户端的工具选择并发送约束，不再因模型未遵守约束丢弃有效输出。
func prepareToolRequest(request GenerateRequest) (GenerateRequest, error) {
	config := request.Tools.ToolConfig
	if config.Mode == "none" {
		return request, nil
	}
	if search := request.Tools.GoogleSearch; search != nil {
		if search.ContextSize != "" {
			request.System += "\nUse " + search.ContextSize + " search context depth."
		}
		if len(search.UserLocation) > 0 {
			request.System += "\nApproximate search user location: " + string(search.UserLocation)
		}
		if len(search.AllowedDomains) > 0 {
			request.System += "\nRestrict web searches to these domains using site: queries: " + strings.Join(search.AllowedDomains, ", ")
		}
	}
	names := make(map[string]bool)
	functions := make([]FunctionDeclaration, 0, len(request.Tools.Functions))
	for _, declaration := range request.Tools.Functions {
		if len(config.AllowedFunctionNames) > 0 && !slices.Contains(config.AllowedFunctionNames, declaration.Name) {
			continue
		}
		if names[declaration.Name] {
			return request, fmt.Errorf("function %q 重复", declaration.Name)
		}
		if config.Mode == "validated" {
			declaration.Strict = true
		}
		names[declaration.Name] = true
		functions = append(functions, declaration)
	}
	for _, name := range config.AllowedFunctionNames {
		if !names[name] {
			return request, fmt.Errorf("tool choice 引用了未声明函数 %q", name)
		}
	}
	serverSide := len(config.AllowedFunctionNames) == 0 && (len(request.Tools.Google) > 0 || request.Tools.GoogleSearch != nil)
	if config.Mode == "required" && len(functions) == 0 && !serverSide {
		return request, fmt.Errorf("required tool choice 需要至少一个工具")
	}
	if len(config.AllowedFunctionNames) > 0 || config.Mode == "validated" {
		request.Tools.Functions = functions
	}
	if config.Mode == "required" {
		selected := make([]string, 0, len(functions))
		for _, declaration := range functions {
			selected = append(selected, declaration.Name)
		}
		if serverSide {
			selected = append(selected, request.Tools.Google...)
			if request.Tools.GoogleSearch != nil && !slices.Contains(selected, "google_search") {
				selected = append(selected, "google_search")
			}
		}
		request.System += "\nUse one of these tools in this response: " + strings.Join(selected, ", ") + ". Use the tool before answering."
	}
	if config.ParallelCalls != nil && !*config.ParallelCalls && len(functions) > 0 {
		request.System += "\nCall at most one function in this response."
	}
	return request, nil
}
