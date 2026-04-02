package agent

import (
	"context"
	"encoding/json"
	"time"

	chonkai "github.com/chonk-dev/chonk-ai"
)

// AgentMessage 是 Agent 系统的消息类型（允许自定义消息结构）
type AgentMessage = any

// AssistantMessage 助手消息（使用具体类型）
type AssistantMessage = chonkai.AssistantMessage

// UserMessage 用户消息
type UserMessage = chonkai.UserMessage

// ToolResultMessage 工具结果消息
type ToolResultMessage = chonkai.ToolResultMessage

// UserContent 用户内容
type UserContent = chonkai.UserContent

// TextContent 文本内容
type TextContent = chonkai.TextContent

// ImageContent 图片内容
type ImageContent = chonkai.ImageContent

// ToolCall 工具调用
type ToolCall = chonkai.ToolCall

// ToolProgressType 工具进度类型
type ToolProgressType string

const (
	ProgressBash      ToolProgressType = "bash"
	ProgressWebSearch ToolProgressType = "web_search"
	ProgressFileOp    ToolProgressType = "file_op"
	ProgressAgentTool ToolProgressType = "agent_tool"
)

// ToolProgress 工具进度更新
type ToolProgress struct {
	Type ToolProgressType `json:"type"`
	Data any              `json:"data"`
}

// BashProgress Bash 工具进度数据
type BashProgress struct {
	Output       string `json:"output"`
	FullOutput   string `json:"fullOutput"`
	ElapsedSecs  int    `json:"elapsedTimeSeconds"`
	TotalLines   int    `json:"totalLines"`
	TotalBytes   int    `json:"totalBytes"`
	IsIncomplete bool   `json:"isIncomplete"`
}

// ToolResult 工具执行结果
type ToolResult struct {
	Content []UserContent  `json:"content"`
	Details any            `json:"details,omitempty"`
	Meta    ToolResultMeta `json:"meta"`
}

// ToolResultMeta 工具结果元数据
type ToolResultMeta struct {
	MCPMeta      *MCPMeta `json:"mcpMeta,omitempty"`
	IsTruncated  bool     `json:"isTruncated"`
	Summary      string   `json:"summary,omitempty"`
	ActivityDesc string   `json:"activityDesc,omitempty"`
}

// MCPMeta MCP 协议元数据
type MCPMeta struct {
	Meta              map[string]any `json:"_meta,omitempty"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
}

// ToolExecute 工具执行函数签名
type ToolExecute func(
	ctx context.Context,
	toolCallID string,
	params map[string]any,
	update ToolUpdate,
) (ToolResult, error)

// ToolUpdate 工具流式更新回调
type ToolUpdate func(partial ToolResult)

// Tool 工具定义
type Tool struct {
	// 基础信息
	Name        string          `json:"name"`
	Label       string          `json:"label,omitempty"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`

	// 执行函数
	Execute ToolExecute `json:"-"`

	// 可选钩子
	PrepareArguments func(map[string]any) (map[string]any, error)                    `json:"-"`
	ValidateInput    func(context.Context, map[string]any) error                     `json:"-"`
	CheckPermission  func(context.Context, map[string]any) (PermissionResult, error) `json:"-"`

	// 元数据
	IsConcurrencySafe bool              `json:"isConcurrencySafe"`
	IsReadOnly        bool              `json:"isReadOnly"`
	IsDestructive     bool              `json:"isDestructive"`
	InterruptBehavior InterruptBehavior `json:"interruptBehavior"`
	ProgressType      ToolProgressType  `json:"progressType,omitempty"`

	// UI 辅助（可选）
	GetSummary        func(map[string]any) string `json:"-"`
	GetActivityDesc   func(map[string]any) string `json:"-"`
	IsResultTruncated func(ToolResult) bool       `json:"-"`
}

// InterruptBehavior 中断行为
type InterruptBehavior string

const (
	InterruptCancel InterruptBehavior = "cancel"
	InterruptBlock  InterruptBehavior = "block"
)

// PermissionResult 权限检查结果
type PermissionResult struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
	Prompt  bool   `json:"prompt,omitempty"`
}

// ToolExecutionMode 工具执行模式
type ToolExecutionMode string

const (
	ToolExecutionParallel   ToolExecutionMode = "parallel"
	ToolExecutionSequential ToolExecutionMode = "sequential"
)

// ThinkingOff disables reasoning effort.
const ThinkingOff chonkai.ThinkingLevel = "off"

// QueueMode 队列模式
type QueueMode string

const (
	ModeOneAtATime QueueMode = "one-at-a-time"
	ModeAll        QueueMode = "all"
)

// BeforeToolCallContext beforeToolCall 钩子的上下文
type BeforeToolCallContext struct {
	AssistantMessage AssistantMessage
	ToolCall         ToolCall
	Args             map[string]any
	Context          AgentContext
}

// BeforeToolCallResult beforeToolCall 钩子的返回值
type BeforeToolCallResult struct {
	Block  bool   `json:"block"`
	Reason string `json:"reason,omitempty"`
}

// AfterToolCallContext afterToolCall 钩子的上下文
type AfterToolCallContext struct {
	AssistantMessage AssistantMessage
	ToolCall         ToolCall
	Args             map[string]any
	Result           ToolResult
	IsError          bool
	Context          AgentContext
}

// AfterToolCallResult afterToolCall 钩子的返回值
type AfterToolCallResult struct {
	Content []UserContent `json:"content,omitempty"`
	Details any           `json:"details,omitempty"`
	IsError *bool         `json:"isError,omitempty"`
}

// AgentContext Agent 上下文
type AgentContext struct {
	SystemPrompt string         `json:"systemPrompt"`
	Messages     []AgentMessage `json:"messages"`
	Tools        []Tool         `json:"tools,omitempty"`
}

// NewUserMessage 创建用户消息（便捷函数）
func NewUserMessage(content ...UserContent) UserMessage {
	return UserMessage{
		Content:   content,
		Timestamp: time.Now(),
	}
}

// NewTextContent 创建文本内容（便捷函数）
func NewTextContent(text string) TextContent {
	return TextContent{
		Type: "text",
		Text: text,
	}
}

// NewImageContent 创建图片内容（便捷函数）
func NewImageContent(data, mimeType string) ImageContent {
	return ImageContent{
		Type:     "image",
		Data:     data,
		MimeType: mimeType,
	}
}
