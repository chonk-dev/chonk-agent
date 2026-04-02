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

// executeToolCallsSequential 顺序执行工具（带钩子支持）
func executeToolCallsSequential(
	ctx context.Context,
	assistant *AssistantMessage,
	toolCalls []chonkai.ToolCall,
	config LoopConfig,
	stream *AgentEventStream,
) ([]chonkai.ToolResultMessage, error) {
	results := make([]chonkai.ToolResultMessage, 0, len(toolCalls))

	for _, tc := range toolCalls {
		stream.Push(AgentEvent{
			Type:       EventToolExecutionStart,
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			ToolArgs:   parseToolArgs(tc.Arguments),
		})

		// 解析工具参数
		args := parseToolArgs(tc.Arguments)

		// 查找工具定义
		var foundTool *Tool
		for _, t := range config.Tools {
			if t.Name == tc.Name {
				foundTool = &t
				break
			}
		}

		if foundTool == nil {
			result := createErrorToolResult(tc.ID, tc.Name, "Tool not found: "+tc.Name)
			results = append(results, result)
			emitToolEnd(stream, tc.ID, tc.Name, result.IsError)
			continue
		}

		// 工具参数预处理
		if foundTool.PrepareArguments != nil {
			var err error
			args, err = foundTool.PrepareArguments(args)
			if err != nil {
				result := createErrorToolResult(tc.ID, tc.Name, "Argument preparation error: "+err.Error())
				results = append(results, result)
				emitToolEnd(stream, tc.ID, tc.Name, result.IsError)
				continue
			}
		}

		// 输入验证
		if foundTool.ValidateInput != nil {
			if err := foundTool.ValidateInput(ctx, args); err != nil {
				result := createErrorToolResult(tc.ID, tc.Name, "Validation error: "+err.Error())
				results = append(results, result)
				emitToolEnd(stream, tc.ID, tc.Name, result.IsError)
				continue
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
				result := createErrorToolResult(tc.ID, tc.Name, "BeforeToolCall error: "+err.Error())
				results = append(results, result)
				emitToolEnd(stream, tc.ID, tc.Name, result.IsError)
				continue
			}
			if beforeResult != nil && beforeResult.Block {
				reason := beforeResult.Reason
				if reason == "" {
					reason = "Tool execution was blocked by beforeToolCall hook"
				}
				result := createErrorToolResult(tc.ID, tc.Name, reason)
				results = append(results, result)
				emitToolEnd(stream, tc.ID, tc.Name, result.IsError)
				continue
			}
		}

		// 执行工具
		toolResult, err := foundTool.Execute(ctx, tc.ID, args, func(partial ToolResult) {
			stream.Push(AgentEvent{
				Type:       EventToolExecutionUpdate,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				ToolResult: &partial,
			})
		})

		if err != nil {
			result := createErrorToolResult(tc.ID, tc.Name, "Tool execution error: "+err.Error())
			results = append(results, result)
			emitToolEnd(stream, tc.ID, tc.Name, true)
			continue
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

		result := chonkai.ToolResultMessage{
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			Content:    toolResult.Content,
			IsError:    isError,
			Timestamp:  time.Now(),
		}
		results = append(results, result)
		emitToolEnd(stream, tc.ID, tc.Name, isError)
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
			// 查找工具
			var foundTool *Tool
			for _, tool := range config.Tools {
				if tool.Name == tc.Name {
					foundTool = &tool
					break
				}
			}
			if foundTool == nil {
				results[i] = createErrorToolResult(tc.ID, tc.Name, "Tool not found: "+tc.Name)
				emitToolEnd(stream, tc.ID, tc.Name, true)
				return
			}

			// 解析参数
			args := parseToolArgs(tc.Arguments)

			stream.Push(AgentEvent{
				Type:       EventToolExecutionStart,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				ToolArgs:   args,
			})
			if foundTool.PrepareArguments != nil {
				var err error
				args, err = foundTool.PrepareArguments(args)
				if err != nil {
					results[i] = createErrorToolResult(tc.ID, tc.Name, "PrepareArguments error: "+err.Error())
					emitToolEnd(stream, tc.ID, tc.Name, true)
					return
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
					results[i] = createErrorToolResult(tc.ID, tc.Name, "BeforeToolCall error: "+err.Error())
					emitToolEnd(stream, tc.ID, tc.Name, true)
					return
				}
				if beforeResult != nil && beforeResult.Block {
					reason := beforeResult.Reason
					if reason == "" {
						reason = "Tool execution was blocked by beforeToolCall hook"
					}
					results[i] = createErrorToolResult(tc.ID, tc.Name, reason)
					emitToolEnd(stream, tc.ID, tc.Name, true)
					return
				}
			}

			// 执行工具
			toolResult, err := foundTool.Execute(ctx, tc.ID, args, func(partial ToolResult) {
				stream.Push(AgentEvent{
					Type:       EventToolExecutionUpdate,
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					ToolResult: &partial,
				})
			})

			if err != nil {
				results[i] = createErrorToolResult(tc.ID, tc.Name, "Tool execution error: "+err.Error())
				emitToolEnd(stream, tc.ID, tc.Name, true)
				return
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

			results[i] = chonkai.ToolResultMessage{
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Content:    toolResult.Content,
				IsError:    isError,
				Timestamp:  time.Now(),
			}
			emitToolEnd(stream, tc.ID, tc.Name, isError)
		})
	}

	wg.Wait()
	return results, nil
}

// parseToolArgs 解析工具参数
func parseToolArgs(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return make(map[string]any)
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return make(map[string]any)
	}
	return args
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
