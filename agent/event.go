package agent

import (
	chonkai "github.com/chonk-dev/chonk-ai"
)

// AgentEventType Agent 事件类型
type AgentEventType string

const (
	// Agent 生命周期
	EventAgentStart AgentEventType = "agent_start"
	EventAgentEnd   AgentEventType = "agent_end"

	// Turn 生命周期
	EventTurnStart AgentEventType = "turn_start"
	EventTurnEnd   AgentEventType = "turn_end"

	// 消息生命周期
	EventMessageStart  AgentEventType = "message_start"
	EventMessageUpdate AgentEventType = "message_update"
	EventMessageEnd    AgentEventType = "message_end"

	// 工具执行生命周期
	EventToolExecutionStart  AgentEventType = "tool_execution_start"
	EventToolExecutionUpdate AgentEventType = "tool_execution_update"
	EventToolExecutionEnd    AgentEventType = "tool_execution_end"
)

// AgentEvent Agent 事件
type AgentEvent struct {
	Type AgentEventType `json:"type"`

	// 消息相关（使用接口值，不是指针）
	Message        AgentMessage   `json:"message,omitempty"`
	AssistantEvent *chonkai.Event `json:"assistantEvent,omitempty"`

	// 工具相关
	ToolCallID  string         `json:"toolCallId,omitempty"`
	ToolName    string         `json:"toolName,omitempty"`
	ToolArgs    map[string]any `json:"toolArgs,omitempty"`
	ToolResult  *ToolResult    `json:"toolResult,omitempty"`
	ToolIsError bool           `json:"toolIsError,omitempty"`

	// 聚合数据
	Messages    []AgentMessage              `json:"messages,omitempty"`
	ToolResults []chonkai.ToolResultMessage `json:"toolResults,omitempty"`
}
