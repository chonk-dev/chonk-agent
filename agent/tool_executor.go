package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	chonkai "github.com/chonk-dev/chonk-ai"
)

// executeToolCalls 执行工具调用
func executeToolCalls(
	ctx context.Context,
	assistant *AssistantMessage,
	toolCalls []chonkai.ToolCall,
	config LoopConfig,
	stream *AgentEventStream,
) ([]chonkai.ToolResultMessage, error) {
	if config.ToolExecution == ToolExecutionSequential {
		return executeToolCallsSequential(ctx, assistant, toolCalls, config, stream)
	}
	return executeToolCallsParallel(ctx, assistant, toolCalls, config, stream)
}

// preparedCall 工具准备阶段的成功结果
type preparedCall struct {
	tc   chonkai.ToolCall
	tool *Tool
	args map[string]any
}

// prepareToolCall 准备阶段：解析参数 → 查找工具 → 预处理 → 验证 → 权限 → beforeHook
// 返回 (prepared, nil) 或 (_, &errorResult)。
// 调用方负责：在收到 errorResult 时 EventToolExecutionEnd 已由本函数发出，无需重复。
func prepareToolCall(
	ctx context.Context,
	assistant *AssistantMessage,
	tc chonkai.ToolCall,
	config LoopConfig,
	stream *AgentEventStream,
) (preparedCall, *chonkai.ToolResultMessage) {
	// 解析参数（只解析一次，结果用于 start 事件和后续步骤）
	args, parseErr := parseToolArgs(tc.Arguments)

	stream.Push(AgentEvent{
		Type:       EventToolExecutionStart,
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		ToolArgs:   args,
	})

	if parseErr != nil {
		return fail(tc, "Invalid tool arguments: "+parseErr.Error(), stream)
	}

	// 查找工具（通过索引取指针，避免 loop-variable 逃逸）
	var foundTool *Tool
	for i := range config.Tools {
		if config.Tools[i].Name == tc.Name {
			foundTool = &config.Tools[i]
			break
		}
	}
	if foundTool == nil {
		return fail(tc, "Tool not found: "+tc.Name, stream)
	}

	// 参数预处理
	if foundTool.PrepareArguments != nil {
		var err error
		args, err = foundTool.PrepareArguments(args)
		if err != nil {
			return fail(tc, "Argument preparation error: "+err.Error(), stream)
		}
	}

	// 输入验证
	if foundTool.ValidateInput != nil {
		if err := foundTool.ValidateInput(ctx, args); err != nil {
			return fail(tc, "Validation error: "+err.Error(), stream)
		}
	}

	// 权限检查
	if foundTool.CheckPermission != nil {
		perm, err := foundTool.CheckPermission(ctx, args)
		if err != nil {
			return fail(tc, "Permission check error: "+err.Error(), stream)
		}
		if !perm.Allowed {
			reason := perm.Reason
			if reason == "" {
				reason = "Tool execution not permitted"
			}
			return fail(tc, reason, stream)
		}
	}

	// beforeToolCall 钩子
	if config.BeforeToolCall != nil {
		beforeResult, err := config.BeforeToolCall(ctx, BeforeToolCallContext{
			AssistantMessage: *assistant,
			ToolCall:         tc,
			Args:             args,
		})
		if err != nil {
			return fail(tc, "BeforeToolCall error: "+err.Error(), stream)
		}
		if beforeResult != nil && beforeResult.Block {
			reason := beforeResult.Reason
			if reason == "" {
				reason = "Tool execution was blocked by beforeToolCall hook"
			}
			return fail(tc, reason, stream)
		}
	}

	return preparedCall{tc: tc, tool: foundTool, args: args}, nil
}

// runAndFinalizeToolCall 执行阶段 + 后处理：execute → 填充 Meta → afterHook → 构建结果
func runAndFinalizeToolCall(
	ctx context.Context,
	assistant *AssistantMessage,
	p preparedCall,
	config LoopConfig,
	stream *AgentEventStream,
) chonkai.ToolResultMessage {
	tc, tool, args := p.tc, p.tool, p.args

	// 执行工具
	toolResult, execErr := tool.Execute(ctx, tc.ID, args, func(partial ToolResult) {
		stream.Push(AgentEvent{
			Type:       EventToolExecutionUpdate,
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			ToolResult: &partial,
		})
	})

	if execErr != nil {
		r := createErrorToolResult(tc.ID, tc.Name, "Tool execution error: "+execErr.Error())
		emitToolEnd(stream, tc.ID, tc.Name, true)
		return r
	}

	// 填充 Meta 字段
	if tool.GetActivityDesc != nil {
		toolResult.Meta.ActivityDesc = tool.GetActivityDesc(args)
	}
	if tool.GetSummary != nil {
		toolResult.Meta.Summary = tool.GetSummary(args)
	}
	if tool.IsResultTruncated != nil {
		toolResult.Meta.IsTruncated = tool.IsResultTruncated(toolResult)
	}

	// afterToolCall 钩子
	isError := false
	if config.AfterToolCall != nil {
		afterResult, err := config.AfterToolCall(ctx, AfterToolCallContext{
			AssistantMessage: *assistant,
			ToolCall:         tc,
			Args:             args,
			Result:           toolResult,
			IsError:          isError,
		})
		if err == nil && afterResult != nil {
			if afterResult.Content != nil {
				toolResult.Content = afterResult.Content
			}
			if afterResult.Details != nil {
				toolResult.Details = afterResult.Details
			}
			if afterResult.IsError != nil {
				isError = *afterResult.IsError
			}
		}
	}

	r := chonkai.ToolResultMessage{
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		Content:    toolResult.Content,
		IsError:    isError,
		Timestamp:  time.Now(),
	}
	emitToolEnd(stream, tc.ID, tc.Name, isError)
	return r
}

// executeToolCallsSequential 顺序执行工具
func executeToolCallsSequential(
	ctx context.Context,
	assistant *AssistantMessage,
	toolCalls []chonkai.ToolCall,
	config LoopConfig,
	stream *AgentEventStream,
) ([]chonkai.ToolResultMessage, error) {
	results := make([]chonkai.ToolResultMessage, 0, len(toolCalls))
	for _, tc := range toolCalls {
		prepared, errResult := prepareToolCall(ctx, assistant, tc, config, stream)
		if errResult != nil {
			results = append(results, *errResult)
			continue
		}
		results = append(results, runAndFinalizeToolCall(ctx, assistant, prepared, config, stream))
	}
	return results, nil
}

// executeToolCallsParallel 并行执行工具
func executeToolCallsParallel(
	ctx context.Context,
	assistant *AssistantMessage,
	toolCalls []chonkai.ToolCall,
	config LoopConfig,
	stream *AgentEventStream,
) ([]chonkai.ToolResultMessage, error) {
	results := make([]chonkai.ToolResultMessage, len(toolCalls))
	var wg sync.WaitGroup

	for i, tc := range toolCalls {
		wg.Go(func() {
			prepared, errResult := prepareToolCall(ctx, assistant, tc, config, stream)
			if errResult != nil {
				results[i] = *errResult
				return
			}
			results[i] = runAndFinalizeToolCall(ctx, assistant, prepared, config, stream)
		})
	}

	wg.Wait()
	return results, nil
}

// fail 创建错误结果并发出 end 事件，供 prepareToolCall 内部使用
func fail(tc chonkai.ToolCall, msg string, stream *AgentEventStream) (preparedCall, *chonkai.ToolResultMessage) {
	r := createErrorToolResult(tc.ID, tc.Name, msg)
	emitToolEnd(stream, tc.ID, tc.Name, true)
	return preparedCall{}, &r
}

// parseToolArgs 解析工具参数
func parseToolArgs(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return make(map[string]any), nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	return args, nil
}

// createErrorToolResult 创建错误工具结果
func createErrorToolResult(toolCallID, toolName, errMsg string) chonkai.ToolResultMessage {
	return chonkai.ToolResultMessage{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Content: []UserContent{
			TextContent{Type: "text", Text: errMsg},
		},
		IsError:   true,
		Timestamp: time.Now(),
	}
}

// emitToolEnd 发射工具结束事件
func emitToolEnd(stream *AgentEventStream, toolCallID, toolName string, isError bool) {
	stream.Push(AgentEvent{Type: EventToolExecutionEnd, ToolCallID: toolCallID, ToolName: toolName, ToolIsError: isError})
}
