# chonk-agent

基于 [chonk-ai](https://github.com/chonk-dev/chonk-ai) 构建的通用 Agent 运行时库。

[![Go Version](https://img.shields.io/badge/go-1.26+-blue.svg)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

**[English](README.md)**

---

## 特性

- **完整的 Agent 运行时** — 状态管理、事件系统、消息队列
- **工具调用系统** — `beforeToolCall` / `afterToolCall` 钩子，完整掌控工具执行
- **流式事件处理** — 基于 channel 和 `iter.Seq` 的现代 Go 设计
- **消息队列** — Steering（中断注入）和 Follow-up（排队等待）支持
- **后台任务管理** — 长时间任务后台执行

---

## 安装

```bash
go get github.com/chonk-dev/chonk-agent/agent
```

**依赖**: Go 1.26+，[chonk-ai](https://github.com/chonk-dev/chonk-ai)

---

## 快速开始

### 1. 基础对话

```go
model := &chonkai.Model{
    ID:       "gpt-4o-mini",
    Api:      chonkai.ApiOpenAICompletions,
    Provider: "openai",
}

a := agent.NewAgent(
    agent.WithModel(model),
    agent.WithSystemPrompt("You are a helpful assistant."),
    agent.WithAPIKey("sk-..."),
)

events := a.Subscribe()
go func() {
    for event := range events.Events() {
        if event.Type == agent.EventMessageUpdate {
            fmt.Print(event.AssistantEvent.Delta)
        }
    }
}()

ctx := context.Background()
a.Prompt(ctx, chonkai.UserMessage{RawText: "Hello!"})
a.WaitIdle(ctx)
```

### 2. 工具调用

```go
type WeatherParams struct {
    City string `json:"city" jsonschema:"城市名称"`
}

weatherTool := agent.NewTool(
    "get_weather",
    "Get current weather for a location",
    func(ctx context.Context, toolCallID string, params map[string]any,
         update agent.ToolUpdate) (agent.ToolResult, error) {

        city := params["city"].(string)
        return agent.ToolResult{
            Content: []chonkai.UserContent{
                chonkai.TextContent{Type: "text", Text: fmt.Sprintf("%s 当前 25°C", city)},
            },
        }, nil
    },
    agent.WithSchema[WeatherParams](),
)

a := agent.NewAgent(
    agent.WithModel(model),
    agent.WithSystemPrompt("Use tools when appropriate."),
    agent.WithTools(weatherTool),
    agent.WithAPIKey("sk-..."),
)

a.Prompt(ctx, chonkai.UserMessage{RawText: "北京现在天气怎么样？"})
a.WaitIdle(ctx)
```

### 3. beforeToolCall 钩子（阻止工具执行）

```go
a := agent.NewAgent(
    agent.WithModel(model),
    agent.WithTools(bashTool),
    agent.WithAPIKey("sk-..."),
    agent.WithBeforeToolCall(func(ctx context.Context,
        bctx agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {

        // 阻止所有 bash 命令
        if bctx.ToolCall.Name == "bash" {
            return &agent.BeforeToolCallResult{
                Block:  true,
                Reason: "bash is disabled for security",
            }, nil
        }
        return nil, nil // 允许执行
    }),
)
```

### 4. afterToolCall 钩子（修改工具结果）

```go
a := agent.NewAgent(
    agent.WithModel(model),
    agent.WithTools(databaseTool),
    agent.WithAPIKey("sk-..."),
    agent.WithAfterToolCall(func(ctx context.Context,
        actx agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {

        // 记录审计日志
        logAudit(actx.ToolCall.Name, actx.Args)

        // 修改返回结果
        return &agent.AfterToolCallResult{
            Details: map[string]any{
                "original":  actx.Result.Details,
                "audited":   true,
                "auditedAt": time.Now().Unix(),
            },
        }, nil
    }),
)
```

### 5. Steering 和 Follow-up

```go
a := agent.NewAgent(
    agent.WithModel(model),
    agent.WithSystemPrompt("You are helpful."),
    agent.WithAPIKey("sk-..."),
)

go a.Prompt(ctx, chonkai.UserMessage{RawText: "读取 config.json"})

// 中途注入（立即打断当前执行）
time.Sleep(500 * time.Millisecond)
a.Steer(chonkai.UserMessage{RawText: "停！不要读它。"})

// 完成后继续（排队等待当前任务结束）
a.FollowUp(chonkai.UserMessage{RawText: "改为检查 package.json"})

a.WaitIdle(ctx)
```

---

## API 参考

### 创建 Agent

```go
a := agent.NewAgent(
    agent.WithModel(model),
    agent.WithSystemPrompt("You are helpful."),
    agent.WithTools(tool1, tool2),
    agent.WithAPIKey("sk-..."),
    agent.WithBeforeToolCall(beforeHook),
    agent.WithAfterToolCall(afterHook),
    agent.WithSteeringMode(agent.ModeOneAtATime),
    agent.WithFollowUpMode(agent.ModeOneAtATime),
)
```

### 状态访问

```go
prompt    := a.State.SystemPrompt
messages  := a.State.Messages
streaming := a.State.IsStreaming

// 修改
a.State.SystemPrompt = "新的提示词"
a.State.Tools = append(a.State.Tools, newTool)
```

### 运行控制

```go
a.Prompt(ctx, chonkai.UserMessage{RawText: "Hello"})  // 开始对话
a.Continue(ctx)                                         // 继续执行
a.WaitIdle(ctx)                                         // 等待空闲
a.Abort()                                               // 中止当前运行
a.Reset()                                               // 重置状态
```

### 消息队列

```go
a.Steer(chonkai.UserMessage{RawText: "换个方向"})     // 中断注入
a.FollowUp(chonkai.UserMessage{RawText: "然后做这个"}) // 排队等待

a.ClearSteeringQueue()
a.ClearFollowUpQueue()
a.ClearAllQueues()

if a.HasQueuedMessages() { ... }
```

### 事件订阅

```go
events := a.Subscribe()
defer events.Close()

for event := range events.Events() {
    switch event.Type {
    case agent.EventAgentStart:       // Agent 开始运行
    case agent.EventAgentEnd:         // Agent 完成
    case agent.EventTurnStart:        // 新 turn 开始
    case agent.EventTurnEnd:          // turn 结束
    case agent.EventMessageUpdate:    // 流式文本增量
        fmt.Print(event.AssistantEvent.Delta)
    case agent.EventToolExecutionStart:
        fmt.Printf("工具: %s 参数: %v\n", event.ToolName, event.ToolArgs)
    case agent.EventToolExecutionUpdate:
        fmt.Printf("进度: %v\n", event.ToolResult)
    case agent.EventToolExecutionEnd:
        fmt.Printf("完成，isError: %v\n", event.ToolIsError)
    }
}
```

---

## 项目结构

```
chonk-agent/
├── agent/
│   ├── agent.go              # Agent 结构体与选项
│   ├── types.go              # Context 和 Result 类型
│   ├── event.go              # AgentEvent 定义
│   ├── event_channel.go      # EventChannel 订阅
│   ├── loop.go               # Agent Loop 核心
│   ├── run.go                # 运行控制（Prompt/Continue/Abort）
│   ├── queue.go              # MessageQueue（Steering/Follow-up）
│   ├── tool.go               # Tool 定义
│   ├── tool_executor.go      # 工具执行（含钩子支持）
│   ├── background_tasks.go   # 后台任务管理
│   ├── stream.go             # StreamFn 定义
│   └── errors.go             # 错误类型
├── examples/
│   └── basic/
│       └── main.go
├── go.mod
└── README.md
```

---

## 许可证

MIT
