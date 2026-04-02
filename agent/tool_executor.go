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
	context AgentContext,
	config LoopConfig,
	stream *AgentEventStream,
) ([]chonkai.ToolResultMessage, error) {
	contextSnapshot := AgentContext{
		SystemPrompt: context.SystemPrompt,
		Messages:     append([]AgentMessage(nil), context.Messages...),
		Tools:        append([]Tool(nil), context.Tools...),
	}
	if config.ToolExecution == ToolExecutionSequential {
		return executeToolCallsSequential(ctx, assistant, toolCalls, contextSnapshot, config, stream)
	}
	return executeToolCallsParallel(ctx, assistant, toolCalls, contextSnapshot, config, stream)
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
	context AgentContext,
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
	toolSet := context.Tools
	if len(toolSet) == 0 {
		toolSet = config.Tools
	}
	var foundTool *Tool
	for i := range toolSet {
		if toolSet[i].Name == tc.Name {
			foundTool = &toolSet[i]
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
			Context:          context,
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

// executePreparedToolCall 执行工具并返回原始结果（不发 end/message 事件）
func executePreparedToolCall(
	ctx context.Context,
	p preparedCall,
	stream *AgentEventStream,
) (ToolResult, error) {
	tc, tool, args := p.tc, p.tool, p.args
	toolResult, execErr := tool.Execute(ctx, tc.ID, args, func(partial ToolResult) {
		stream.Push(AgentEvent{
			Type:       EventToolExecutionUpdate,
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			ToolResult: &partial,
		})
	})
	return toolResult, execErr
}

// finalizeToolCall 后处理：填充 Meta → afterHook → 构建结果并发事件
func finalizeToolCall(
	ctx context.Context,
	assistant *AssistantMessage,
	p preparedCall,
	toolResult ToolResult,
	execErr error,
	context AgentContext,
	config LoopConfig,
	stream *AgentEventStream,
) chonkai.ToolResultMessage {
	tc, tool, args := p.tc, p.tool, p.args

	if execErr != nil {
		errResult := errorToolResult("Tool execution error: " + execErr.Error())
		r := buildToolResultMessage(tc.ID, tc.Name, errResult, true)
		emitToolEnd(stream, tc.ID, tc.Name, true, &errResult)
		emitToolMessage(stream, r)
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
			Context:          context,
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

	r := buildToolResultMessage(tc.ID, tc.Name, toolResult, isError)
	emitToolEnd(stream, tc.ID, tc.Name, isError, &toolResult)
	emitToolMessage(stream, r)
	return r
}

// executeToolCallsSequential 顺序执行工具
func executeToolCallsSequential(
	ctx context.Context,
	assistant *AssistantMessage,
	toolCalls []chonkai.ToolCall,
	context AgentContext,
	config LoopConfig,
	stream *AgentEventStream,
) ([]chonkai.ToolResultMessage, error) {
	results := make([]chonkai.ToolResultMessage, 0, len(toolCalls))
	for _, tc := range toolCalls {
		prepared, errResult := prepareToolCall(ctx, assistant, tc, context, config, stream)
		if errResult != nil {
			results = append(results, *errResult)
			continue
		}
		toolResult, execErr := executePreparedToolCall(ctx, prepared, stream)
		results = append(results, finalizeToolCall(ctx, assistant, prepared, toolResult, execErr, context, config, stream))
	}
	return results, nil
}

// executeToolCallsParallel 并行执行工具
func executeToolCallsParallel(
	ctx context.Context,
	assistant *AssistantMessage,
	toolCalls []chonkai.ToolCall,
	context AgentContext,
	config LoopConfig,
	stream *AgentEventStream,
) ([]chonkai.ToolResultMessage, error) {
	results := make([]chonkai.ToolResultMessage, len(toolCalls))
	prepared := make([]*preparedCall, len(toolCalls))
	outcomes := make([]struct {
		result ToolResult
		err    error
	}, len(toolCalls))

	for i, tc := range toolCalls {
		p, errResult := prepareToolCall(ctx, assistant, tc, context, config, stream)
		if errResult != nil {
			results[i] = *errResult
			continue
		}
		prepared[i] = &p
	}

	var wg sync.WaitGroup
	for i, p := range prepared {
		if p == nil {
			continue
		}
		idx := i
		call := *p
		wg.Go(func() {
			result, execErr := executePreparedToolCall(ctx, call, stream)
			outcomes[idx] = struct {
				result ToolResult
				err    error
			}{result: result, err: execErr}
		})
	}

	wg.Wait()

	for i, p := range prepared {
		if p == nil {
			continue
		}
		outcome := outcomes[i]
		results[i] = finalizeToolCall(ctx, assistant, *p, outcome.result, outcome.err, context, config, stream)
	}
	return results, nil
}

// fail 创建错误结果并发出 end 事件，供 prepareToolCall 内部使用
func fail(tc chonkai.ToolCall, msg string, stream *AgentEventStream) (preparedCall, *chonkai.ToolResultMessage) {
	errResult := errorToolResult(msg)
	r := buildToolResultMessage(tc.ID, tc.Name, errResult, true)
	emitToolEnd(stream, tc.ID, tc.Name, true, &errResult)
	emitToolMessage(stream, r)
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

// errorToolResult 创建错误工具结果
func errorToolResult(errMsg string) ToolResult {
	return ToolResult{
		Content: []UserContent{
			TextContent{Type: "text", Text: errMsg},
		},
	}
}

func buildToolResultMessage(toolCallID, toolName string, result ToolResult, isError bool) chonkai.ToolResultMessage {
	return chonkai.ToolResultMessage{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Content:    result.Content,
		IsError:    isError,
		Timestamp:  time.Now(),
	}
}

// emitToolEnd 发射工具结束事件
func emitToolEnd(stream *AgentEventStream, toolCallID, toolName string, isError bool, result *ToolResult) {
	stream.Push(AgentEvent{
		Type:        EventToolExecutionEnd,
		ToolCallID:  toolCallID,
		ToolName:    toolName,
		ToolIsError: isError,
		ToolResult:  result,
	})
}

func emitToolMessage(stream *AgentEventStream, msg chonkai.ToolResultMessage) {
	stream.Push(AgentEvent{Type: EventMessageStart, Message: msg})
	stream.Push(AgentEvent{Type: EventMessageEnd, Message: msg})
}
