package agent

import (
	"context"
	"iter"
	"sync"

	chonkai "github.com/chonk-dev/chonk-ai"
)

// LoopConfig 循环配置
type LoopConfig struct {
	Model            *chonkai.Model
	ConvertToLlm     func([]AgentMessage) []chonkai.Message
	TransformContext func(context.Context, []AgentMessage) ([]AgentMessage, error)
	BeforeToolCall   func(context.Context, BeforeToolCallContext) (*BeforeToolCallResult, error)
	AfterToolCall    func(context.Context, AfterToolCallContext) (*AfterToolCallResult, error)
	GetApiKey        func(string) (string, error)
	GetSteering      func() ([]AgentMessage, error)
	GetFollowUp      func() ([]AgentMessage, error)
	ToolExecution    ToolExecutionMode
	SessionID        string
	ThinkingBudgets  chonkai.ThinkingBudgets
	StreamFn         StreamFn
	Tools            []Tool
}

// AgentEventStream Agent 事件流
type AgentEventStream struct {
	ch     chan AgentEvent
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	result []AgentMessage
}

// NewAgentEventStream 创建 Agent 事件流
func NewAgentEventStream(bufSize int) *AgentEventStream {
	return &AgentEventStream{
		ch:   make(chan AgentEvent, bufSize),
		done: make(chan struct{}),
	}
}

// Push 发送事件
func (s *AgentEventStream) Push(event AgentEvent) {
	select {
	case s.ch <- event:
	case <-s.done:
	}
}

// Close 关闭流
func (s *AgentEventStream) Close(result []AgentMessage) {
	s.once.Do(func() {
		s.mu.Lock()
		s.result = result
		s.mu.Unlock()
		close(s.ch)
		close(s.done)
	})
}

// Events 返回事件迭代器
func (s *AgentEventStream) Events() iter.Seq[AgentEvent] {
	return func(yield func(AgentEvent) bool) {
		for event := range s.ch {
			if !yield(event) {
				return
			}
		}
	}
}

// Result 返回最终结果
func (s *AgentEventStream) Result() []AgentMessage {
	for {
		select {
		case _, ok := <-s.ch:
			if !ok {
				s.mu.Lock()
				defer s.mu.Unlock()
				return s.result
			}
		case <-s.done:
			for range s.ch {
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.result
		}
	}
}

// RunAgentLoop 运行 Agent 循环
func RunAgentLoop(
	ctx context.Context,
	prompts []AgentMessage,
	context AgentContext,
	config LoopConfig,
) *AgentEventStream {
	stream := NewAgentEventStream(64)

	go func() {
		defer stream.Close(nil)

		messages, err := runLoop(ctx, prompts, context, config, stream)
		if err != nil {
			return
		}
		stream.Close(messages)
	}()

	return stream
}

// RunAgentLoopContinue 从现有上下文继续
func RunAgentLoopContinue(
	ctx context.Context,
	context AgentContext,
	config LoopConfig,
) *AgentEventStream {
	stream := NewAgentEventStream(64)

	go func() {
		defer stream.Close(nil)

		if len(context.Messages) == 0 {
			return
		}

		lastMsg := context.Messages[len(context.Messages)-1]
		if getMessageRole(lastMsg) == "assistant" {
			return
		}

		messages, err := runLoop(ctx, nil, context, config, stream)
		if err != nil {
			return
		}
		stream.Close(messages)
	}()

	return stream
}

// runLoop 内部循环
func runLoop(
	ctx context.Context,
	prompts []AgentMessage,
	context AgentContext,
	config LoopConfig,
	stream *AgentEventStream,
) ([]AgentMessage, error) {
	newMessages := make([]AgentMessage, 0)
	context.Messages = append(context.Messages, prompts...)

	// 发射初始事件
	stream.Push(AgentEvent{Type: EventAgentStart})
	stream.Push(AgentEvent{Type: EventTurnStart})

	for _, p := range prompts {
		stream.Push(AgentEvent{Type: EventMessageStart, Message: p})
		stream.Push(AgentEvent{Type: EventMessageEnd, Message: p})
	}

	firstTurn := true
	pendingMessages, _ := config.GetSteering()

	for {
		hasMoreToolCalls := true
		var toolResults []chonkai.ToolResultMessage

		// 内层循环：处理工具调用和 steering 消息
		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if !firstTurn {
				stream.Push(AgentEvent{Type: EventTurnStart})
			} else {
				firstTurn = false
			}

			// 注入 pending 消息
			for _, p := range pendingMessages {
				stream.Push(AgentEvent{Type: EventMessageStart, Message: p})
				stream.Push(AgentEvent{Type: EventMessageEnd, Message: p})
				context.Messages = append(context.Messages, p)
				newMessages = append(newMessages, p)
			}

			// 流式响应（每次都会读取最新的 context.Messages）
			msg, err := streamAssistantResponse(ctx, &context, config, stream)
			if err != nil {
				return nil, err
			}
			newMessages = append(newMessages, msg)
			// 注意：msg 已经在 streamAssistantResponse 中添加到 context.Messages

			// 类型断言获取 AssistantMessage
			assistantMsg, ok := msg.(AssistantMessage)
			if !ok {
				return nil, nil
			}

			if assistantMsg.StopReason == chonkai.StopReasonError || assistantMsg.StopReason == chonkai.StopReasonAborted {
				stream.Push(AgentEvent{Type: EventTurnEnd, Message: msg})
				stream.Push(AgentEvent{Type: EventAgentEnd, Messages: newMessages})
				return newMessages, nil
			}

			// 检测工具调用
			toolCalls := extractToolCalls(&assistantMsg)
			hasMoreToolCalls = len(toolCalls) > 0

			if hasMoreToolCalls {
				results, err := executeToolCalls(ctx, &assistantMsg, toolCalls, config, stream)
				if err != nil {
					return nil, err
				}
				toolResults = results
				for _, r := range results {
					context.Messages = append(context.Messages, r)
					newMessages = append(newMessages, r)
				}
				// 工具执行完成后，设为 false 让内层循环退出，外层循环会继续
				hasMoreToolCalls = false
			}

			stream.Push(AgentEvent{Type: EventTurnEnd, Message: msg, ToolResults: toolResults})

			// 轮询转向消息
			pendingMessages, _ = config.GetSteering()
		}

		// 内层循环退出：无工具、无转向
		// 检查 follow-up
		followUp, _ := config.GetFollowUp()
		if len(followUp) > 0 {
			pendingMessages = followUp
			continue
		}

		// 如果有工具结果，继续外层循环让模型基于工具结果生成响应
		if len(toolResults) > 0 {
			toolResults = nil // 重置
			continue
		}

		break
	}

	stream.Push(AgentEvent{Type: EventAgentEnd, Messages: newMessages})
	return newMessages, nil
}

// streamAssistantResponse 流式助手响应
func streamAssistantResponse(
	ctx context.Context,
	context *AgentContext,
	config LoopConfig,
	stream *AgentEventStream,
) (AgentMessage, error) {
	// 转换为 LLM 消息（直接使用 context.Messages，确保读取最新数据）
	var llmMessages []chonkai.Message
	if config.ConvertToLlm != nil {
		llmMessages = config.ConvertToLlm(context.Messages)
	} else {
		llmMessages = convertToLLM(context.Messages)
	}

	// 构建 LLM 上下文
	llmContext := &chonkai.Context{
		SystemPrompt: context.SystemPrompt,
		Messages:     llmMessages,
	}

	// 添加工具
	if len(context.Tools) > 0 {
		llmTools := make([]chonkai.Tool, 0, len(context.Tools))
		for _, t := range context.Tools {
			llmTools = append(llmTools, t.ToChonkai())
		}
		llmContext.Tools = llmTools
	}

	// 解析 API Key
	var apiKey string
	if config.GetApiKey != nil {
		key, _ := config.GetApiKey(config.Model.Provider)
		apiKey = key
	}

	// 构建选项
	opts := &chonkai.SimpleStreamOptions{
		StreamOptions: chonkai.StreamOptions{
			SessionID: config.SessionID,
			APIKey:    apiKey,
		},
	}
	if config.Model.Reasoning {
		opts.Reasoning = chonkai.ThinkingMedium
	}
	if config.ThinkingBudgets.Minimal > 0 || config.ThinkingBudgets.Low > 0 ||
		config.ThinkingBudgets.Medium > 0 || config.ThinkingBudgets.High > 0 {
		opts.ThinkingBudgets = &config.ThinkingBudgets
	}

	// 调用流式函数
	streamFn := config.StreamFn
	if streamFn == nil {
		streamFn = StreamSimple
	}

	response := streamFn(ctx, config.Model, llmContext, opts)

	var partial *chonkai.AssistantMessage
	hasPartial := false

	for event := range response.Events() {
		switch event.Type {
		case chonkai.EventStart:
			partial = event.Partial
			context.Messages = append(context.Messages, *partial)
			hasPartial = true
			stream.Push(AgentEvent{Type: EventMessageStart, Message: *partial})

		case chonkai.EventTextDelta, chonkai.EventThinkingDelta, chonkai.EventToolCallDelta:
			if hasPartial {
				partial = event.Partial
				context.Messages[len(context.Messages)-1] = *partial
				stream.Push(AgentEvent{
					Type:           EventMessageUpdate,
					Message:        *partial,
					AssistantEvent: &event,
				})
			}

		case chonkai.EventDone, chonkai.EventError:
			final := response.Result()
			if !hasPartial {
				context.Messages = append(context.Messages, final)
				stream.Push(AgentEvent{Type: EventMessageStart, Message: final})
			} else {
				context.Messages[len(context.Messages)-1] = final
			}
			stream.Push(AgentEvent{Type: EventMessageEnd, Message: final})
			return final, nil
		}
	}

	final := response.Result()
	if !hasPartial {
		context.Messages = append(context.Messages, final)
		stream.Push(AgentEvent{Type: EventMessageStart, Message: final})
	} else {
		context.Messages[len(context.Messages)-1] = final
	}
	stream.Push(AgentEvent{Type: EventMessageEnd, Message: final})
	return final, nil
}

// extractToolCalls 提取工具调用
func extractToolCalls(msg *AssistantMessage) []chonkai.ToolCall {
	toolCalls := make([]chonkai.ToolCall, 0)
	for _, block := range msg.Content {
		if tc, ok := block.(chonkai.ToolCall); ok {
			toolCalls = append(toolCalls, tc)
		}
	}
	return toolCalls
}

// convertToLLM 转换为 LLM 消息（默认实现）
func convertToLLM(messages []AgentMessage) []chonkai.Message {
	result := make([]chonkai.Message, 0, len(messages))
	for _, m := range messages {
		switch msg := m.(type) {
		case chonkai.UserMessage:
			result = append(result, msg)
		case chonkai.AssistantMessage:
			result = append(result, msg)
		case chonkai.ToolResultMessage:
			result = append(result, msg)
		}
	}
	return result
}
