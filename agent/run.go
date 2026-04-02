package agent

import (
	"context"

	chonkai "github.com/chonk-dev/chonk-ai"
)

// Prompt 开始新对话
func (a *Agent) Prompt(ctx context.Context, messages ...AgentMessage) error {
	a.mu.Lock()
	if a.activeRun != nil {
		a.mu.Unlock()
		return ErrAgentBusy
	}

	runCtx, cancel := context.WithCancel(ctx)
	a.activeRun = &activeRun{
		ctx:    runCtx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		if a.activeRun != nil {
			close(a.activeRun.done)
		}
		a.activeRun = nil
		a.mu.Unlock()
	}()

	err := a.runPrompt(runCtx, messages)

	a.mu.Lock()
	if a.activeRun != nil {
		a.activeRun.err = err
	}
	a.mu.Unlock()

	return err
}

// Continue 从现有上下文继续
func (a *Agent) Continue(ctx context.Context) error {
	a.mu.Lock()
	if a.activeRun != nil {
		a.mu.Unlock()
		return ErrAgentBusy
	}

	if len(a.State.Messages) == 0 {
		a.mu.Unlock()
		return ErrNoMessages
	}

	lastMsg := a.State.Messages[len(a.State.Messages)-1]
	if getMessageRole(lastMsg) == "assistant" {
		// 检查是否有 steering 消息
		if msgs := a.steeringQueue.Drain(); len(msgs) > 0 {
			a.mu.Unlock()
			return a.runPrompt(ctx, msgs)
		}

		// 检查是否有 follow-up 消息
		if msgs := a.followUpQueue.Drain(); len(msgs) > 0 {
			a.mu.Unlock()
			return a.runPrompt(ctx, msgs)
		}

		a.mu.Unlock()
		return ErrCannotContinueFromAssistant
	}

	runCtx, cancel := context.WithCancel(ctx)
	a.activeRun = &activeRun{
		ctx:    runCtx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		if a.activeRun != nil {
			close(a.activeRun.done)
		}
		a.activeRun = nil
		a.mu.Unlock()
	}()

	err := a.runContinuation(runCtx)

	a.mu.Lock()
	if a.activeRun != nil {
		a.activeRun.err = err
	}
	a.mu.Unlock()

	return err
}

// Abort 中止当前运行
func (a *Agent) Abort() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.activeRun != nil {
		a.activeRun.cancel()
	}
}

// WaitIdle 等待空闲
func (a *Agent) WaitIdle(ctx context.Context) error {
	a.mu.Lock()
	run := a.activeRun
	a.mu.Unlock()

	if run == nil {
		return nil
	}

	select {
	case <-run.done:
		return run.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Reset 重置状态
func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.State.Messages = nil
	a.State.IsStreaming = false
	a.State.StreamingMessage = nil
	a.State.InProgressToolIDs = nil
	a.State.ErrorMessage = ""

	a.steeringQueue.Clear()
	a.followUpQueue.Clear()
}

// runPrompt 运行提示
func (a *Agent) runPrompt(ctx context.Context, messages []AgentMessage) error {
	a.mu.Lock()
	a.State.IsStreaming = true
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.State.IsStreaming = false
		a.State.StreamingMessage = nil
		a.mu.Unlock()
	}()

	stream := RunAgentLoop(ctx, messages, a.createContext(), a.createLoopConfig())
	return a.consumeStream(stream)
}

// runContinuation 运行续聊
func (a *Agent) runContinuation(ctx context.Context) error {
	a.mu.Lock()
	a.State.IsStreaming = true
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.State.IsStreaming = false
		a.State.StreamingMessage = nil
		a.mu.Unlock()
	}()

	stream := RunAgentLoopContinue(ctx, a.createContext(), a.createLoopConfig())
	return a.consumeStream(stream)
}

// consumeStream 消费事件流
func (a *Agent) consumeStream(stream *AgentEventStream) error {
	for event := range stream.Events() {
		a.processEvent(event)
	}

	// 不需要从 result 添加消息，因为 processEvent 已经在处理过程中添加了
	_ = stream.Result()
	return nil
}

// processEvent 处理单个事件
func (a *Agent) processEvent(event AgentEvent) {
	a.mu.Lock()

	switch event.Type {
	case EventMessageStart, EventMessageUpdate:
		a.State.StreamingMessage = event.Message
	case EventMessageEnd:
		a.State.StreamingMessage = nil
		if event.Message != nil {
			a.State.Messages = append(a.State.Messages, event.Message)
		}
	case EventToolExecutionStart:
		a.State.InProgressToolIDs[event.ToolCallID] = struct{}{}
	case EventToolExecutionEnd:
		delete(a.State.InProgressToolIDs, event.ToolCallID)
	case EventTurnEnd:
		if msg, ok := event.Message.(AssistantMessage); ok && msg.ErrorMsg != "" {
			a.State.ErrorMessage = msg.ErrorMsg
		}
	case EventAgentEnd:
		a.State.StreamingMessage = nil
	}

	a.mu.Unlock()

	// 广播给订阅者
	a.emit(event)
}

// createContext 创建上下文快照
func (a *Agent) createContext() AgentContext {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return AgentContext{
		SystemPrompt: a.State.SystemPrompt,
		Messages:     append([]AgentMessage(nil), a.State.Messages...),
		Tools:        append([]Tool(nil), a.State.Tools...),
	}
}

// createLoopConfig 创建循环配置
func (a *Agent) createLoopConfig() LoopConfig {
	return LoopConfig{
		Model:            a.State.Model,
		ConvertToLlm:     a.ConvertToLlm,
		TransformContext: a.TransformContext,
		BeforeToolCall:   a.BeforeToolCall,
		AfterToolCall:    a.AfterToolCall,
		GetApiKey:        a.GetApiKey,
		GetSteering:      a.getSteeringMessages,
		GetFollowUp:      a.getFollowUpMessages,
		ToolExecution:    a.ToolExecution,
		SessionID:        a.SessionID,
		ThinkingBudgets:  a.ThinkingBudgets,
		StreamFn:         a.StreamFn,
		Tools:            a.State.Tools,
	}
}

// getSteeringMessages 获取转向消息
func (a *Agent) getSteeringMessages() ([]AgentMessage, error) {
	return a.steeringQueue.Drain(), nil
}

// getFollowUpMessages 获取后续消息
func (a *Agent) getFollowUpMessages() ([]AgentMessage, error) {
	return a.followUpQueue.Drain(), nil
}

// emit 向所有订阅者广播事件
func (a *Agent) emit(event AgentEvent) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, ec := range a.listeners {
		ec.Send(event)
	}
}

// Subscribe 订阅事件
func (a *Agent) Subscribe() *EventChannel {
	ec := NewEventChannel(64)

	a.mu.Lock()
	a.listeners = append(a.listeners, ec)
	a.mu.Unlock()

	return ec
}

// Steer 注入转向消息
func (a *Agent) Steer(msg AgentMessage) {
	a.steeringQueue.Enqueue(msg)
}

// FollowUp 注入后续消息
func (a *Agent) FollowUp(msg AgentMessage) {
	a.followUpQueue.Enqueue(msg)
}

// ClearSteeringQueue 清除转向队列
func (a *Agent) ClearSteeringQueue() {
	a.steeringQueue.Clear()
}

// ClearFollowUpQueue 清除后续队列
func (a *Agent) ClearFollowUpQueue() {
	a.followUpQueue.Clear()
}

// ClearAllQueues 清除所有队列
func (a *Agent) ClearAllQueues() {
	a.ClearSteeringQueue()
	a.ClearFollowUpQueue()
}

// HasQueuedMessages 检查是否有队列消息
func (a *Agent) HasQueuedMessages() bool {
	return a.steeringQueue.HasItems() || a.followUpQueue.HasItems()
}

// getMessageRole 获取消息角色
func getMessageRole(msg AgentMessage) string {
	switch msg.(type) {
	case chonkai.UserMessage:
		return "user"
	case chonkai.AssistantMessage:
		return "assistant"
	case chonkai.ToolResultMessage:
		return "toolResult"
	default:
		return "unknown"
	}
}
