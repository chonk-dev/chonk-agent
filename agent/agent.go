package agent

import (
	"context"
	"sync"

	chonkai "github.com/chonk-dev/chonk-ai"
)

// Agent Agent 运行时
type Agent struct {
	// State 公共状态（直接访问）
	State AgentState

	// 配置（创建后通常不变）
	ConvertToLlm     func([]AgentMessage) []chonkai.Message
	TransformContext func(context.Context, []AgentMessage) ([]AgentMessage, error)
	BeforeToolCall   func(context.Context, BeforeToolCallContext) (*BeforeToolCallResult, error)
	AfterToolCall    func(context.Context, AfterToolCallContext) (*AfterToolCallResult, error)
	GetApiKey        func(string) (string, error)
	StreamFn         StreamFn

	// 队列模式
	SteeringMode QueueMode
	FollowUpMode QueueMode

	// 会话配置
	SessionID       string
	ThinkingBudgets chonkai.ThinkingBudgets
	ToolExecution   ToolExecutionMode

	// 内部字段
	mu            sync.RWMutex
	steeringQueue *MessageQueue
	followUpQueue *MessageQueue
	listeners     []*EventChannel
	cbListeners   []*eventListener
	cbSeq         int
	activeRun     *activeRun
}

// AgentState Agent 公共状态
type AgentState struct {
	SystemPrompt      string
	Model             *chonkai.Model
	ThinkingLevel     chonkai.ThinkingLevel
	Tools             []Tool
	Messages          []AgentMessage
	IsStreaming       bool
	StreamingMessage  AgentMessage
	InProgressToolIDs map[string]struct{}
	ErrorMessage      string
}

type eventListener struct {
	id int
	fn func(AgentEvent, context.Context)
}

// activeRun 内部运行状态
type activeRun struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// NewAgent 创建 Agent
func NewAgent(opts ...AgentOption) *Agent {
	a := &Agent{
		State: AgentState{
			Tools:             make([]Tool, 0),
			Messages:          make([]AgentMessage, 0),
			InProgressToolIDs: make(map[string]struct{}),
			Model:             defaultModel(),
			ThinkingLevel:     ThinkingOff,
		},
		steeringQueue: NewMessageQueue(ModeOneAtATime),
		followUpQueue: NewMessageQueue(ModeOneAtATime),
	}

	// 默认配置
	a.ToolExecution = ToolExecutionParallel
	a.SteeringMode = ModeOneAtATime
	a.FollowUpMode = ModeOneAtATime

	for _, opt := range opts {
		opt(a)
	}

	return a
}

func defaultModel() *chonkai.Model {
	return &chonkai.Model{
		ID:       "unknown",
		Name:     "unknown",
		Api:      chonkai.Api("unknown"),
		Provider: "unknown",
	}
}

// AgentOption Agent 配置选项
type AgentOption func(*Agent)

// WithSystemPrompt 设置系统提示
func WithSystemPrompt(prompt string) AgentOption {
	return func(a *Agent) {
		a.State.SystemPrompt = prompt
	}
}

// WithModel 设置模型
func WithModel(model *chonkai.Model) AgentOption {
	return func(a *Agent) {
		a.State.Model = model
	}
}

// WithThinkingLevel 设置思考级别
func WithThinkingLevel(level chonkai.ThinkingLevel) AgentOption {
	return func(a *Agent) {
		a.State.ThinkingLevel = level
	}
}

// WithTools 设置工具列表
func WithTools(tools ...Tool) AgentOption {
	return func(a *Agent) {
		a.State.Tools = append(a.State.Tools, tools...)
	}
}

// WithMessages 设置初始消息
func WithMessages(messages []AgentMessage) AgentOption {
	return func(a *Agent) {
		a.State.Messages = append(a.State.Messages, messages...)
	}
}

// SetTools replaces tools with a copied slice.
func (a *Agent) SetTools(tools []Tool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.State.Tools = append([]Tool(nil), tools...)
}

// SetMessages replaces messages with a copied slice.
func (a *Agent) SetMessages(messages []AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.State.Messages = append([]AgentMessage(nil), messages...)
}

// WithConvertToLlm 设置消息转换函数
func WithConvertToLlm(fn func([]AgentMessage) []chonkai.Message) AgentOption {
	return func(a *Agent) {
		a.ConvertToLlm = fn
	}
}

// WithTransformContext 设置上下文转换函数
func WithTransformContext(fn func(context.Context, []AgentMessage) ([]AgentMessage, error)) AgentOption {
	return func(a *Agent) {
		a.TransformContext = fn
	}
}

// WithBeforeToolCall 设置工具前钩子
func WithBeforeToolCall(fn func(context.Context, BeforeToolCallContext) (*BeforeToolCallResult, error)) AgentOption {
	return func(a *Agent) {
		a.BeforeToolCall = fn
	}
}

// WithAfterToolCall 设置工具后钩子
func WithAfterToolCall(fn func(context.Context, AfterToolCallContext) (*AfterToolCallResult, error)) AgentOption {
	return func(a *Agent) {
		a.AfterToolCall = fn
	}
}

// WithGetApiKey 设置动态 API Key 获取函数
func WithGetApiKey(fn func(string) (string, error)) AgentOption {
	return func(a *Agent) {
		a.GetApiKey = fn
	}
}

// WithAPIKey 设置静态 API Key（简化版）
func WithAPIKey(key string) AgentOption {
	return func(a *Agent) {
		a.GetApiKey = func(provider string) (string, error) {
			return key, nil
		}
	}
}

// WithStreamFn 设置流式函数
func WithStreamFn(fn StreamFn) AgentOption {
	return func(a *Agent) {
		a.StreamFn = fn
	}
}

// WithSessionID 设置会话 ID
func WithSessionID(id string) AgentOption {
	return func(a *Agent) {
		a.SessionID = id
	}
}

// WithThinkingBudgets 设置思考预算
func WithThinkingBudgets(budgets chonkai.ThinkingBudgets) AgentOption {
	return func(a *Agent) {
		a.ThinkingBudgets = budgets
	}
}

// WithToolExecution 设置工具执行模式
func WithToolExecution(mode ToolExecutionMode) AgentOption {
	return func(a *Agent) {
		a.ToolExecution = mode
	}
}

// WithSteeringMode 设置转向队列模式
func WithSteeringMode(mode QueueMode) AgentOption {
	return func(a *Agent) {
		a.SteeringMode = mode
		if a.steeringQueue != nil {
			a.steeringQueue.mode = mode
		}
	}
}

// WithFollowUpMode 设置后续队列模式
func WithFollowUpMode(mode QueueMode) AgentOption {
	return func(a *Agent) {
		a.FollowUpMode = mode
		if a.followUpQueue != nil {
			a.followUpQueue.mode = mode
		}
	}
}
