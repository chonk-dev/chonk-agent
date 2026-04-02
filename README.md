# chonk-agent

A general-purpose Agent runtime library built on [chonk-ai](https://github.com/chonk-dev/chonk-ai).

[![Go Version](https://img.shields.io/badge/go-1.26+-blue.svg)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

**[中文文档](README_zh.md)**

---

## Features

- **Complete Agent runtime** — state management, event system, message queue
- **Tool call system** — `beforeToolCall` / `afterToolCall` hooks for full control
- **Streaming events** — modern Go design with channels and `iter.Seq`
- **Message queues** — Steering (interrupt) and Follow-up (queue) support
- **Background task management** — long-running tasks executed in background

---

## Installation

```bash
go get github.com/chonk-dev/chonk-agent/agent
```

**Requirements**: Go 1.26+, [chonk-ai](https://github.com/chonk-dev/chonk-ai)

---

## Quick Start

### 1. Basic conversation

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

### 2. Tool calls

```go
type WeatherParams struct {
    City string `json:"city" jsonschema:"City name"`
}

weatherTool := agent.NewTool(
    "get_weather",
    "Get current weather for a location",
    func(ctx context.Context, toolCallID string, params map[string]any,
         update agent.ToolUpdate) (agent.ToolResult, error) {

        city := params["city"].(string)
        return agent.ToolResult{
            Content: []chonkai.UserContent{
                chonkai.TextContent{Type: "text", Text: fmt.Sprintf("25°C in %s", city)},
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

a.Prompt(ctx, chonkai.UserMessage{RawText: "What's the weather in Beijing?"})
a.WaitIdle(ctx)
```

### 3. beforeToolCall hook (block execution)

```go
a := agent.NewAgent(
    agent.WithModel(model),
    agent.WithTools(bashTool),
    agent.WithAPIKey("sk-..."),
    agent.WithBeforeToolCall(func(ctx context.Context,
        bctx agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {

        if bctx.ToolCall.Name == "bash" {
            return &agent.BeforeToolCallResult{
                Block:  true,
                Reason: "bash is disabled for security",
            }, nil
        }
        return nil, nil // allow
    }),
)
```

### 4. afterToolCall hook (modify result)

```go
a := agent.NewAgent(
    agent.WithModel(model),
    agent.WithTools(databaseTool),
    agent.WithAPIKey("sk-..."),
    agent.WithAfterToolCall(func(ctx context.Context,
        actx agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {

        logAudit(actx.ToolCall.Name, actx.Args)

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

### 5. Steering and Follow-up

```go
a := agent.NewAgent(
    agent.WithModel(model),
    agent.WithSystemPrompt("You are helpful."),
    agent.WithAPIKey("sk-..."),
)

go a.Prompt(ctx, chonkai.UserMessage{RawText: "Read config.json"})

// Interrupt mid-execution
time.Sleep(500 * time.Millisecond)
a.Steer(chonkai.UserMessage{RawText: "Stop! Don't read it."})

// Queue a follow-up after the current task
a.FollowUp(chonkai.UserMessage{RawText: "Check package.json instead"})

a.WaitIdle(ctx)
```

---

## API Reference

### Creating an agent

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

### State access

```go
prompt   := a.State.SystemPrompt
messages := a.State.Messages
streaming := a.State.IsStreaming

// Modify
a.State.SystemPrompt = "new prompt"
a.State.Tools = append(a.State.Tools, newTool)
```

### Run control

```go
a.Prompt(ctx, chonkai.UserMessage{RawText: "Hello"})  // start
a.Continue(ctx)                                         // continue
a.WaitIdle(ctx)                                         // wait until idle
a.Abort()                                               // abort current run
a.Reset()                                               // reset state
```

### Message queues

```go
a.Steer(chonkai.UserMessage{RawText: "change direction"})    // interrupt
a.FollowUp(chonkai.UserMessage{RawText: "then do this"})     // queue

a.ClearSteeringQueue()
a.ClearFollowUpQueue()
a.ClearAllQueues()

if a.HasQueuedMessages() { ... }
```

### Event subscription

```go
events := a.Subscribe()
defer events.Close()

for event := range events.Events() {
    switch event.Type {
    case agent.EventAgentStart:
    case agent.EventAgentEnd:
    case agent.EventTurnStart:
    case agent.EventTurnEnd:
    case agent.EventMessageUpdate:
        fmt.Print(event.AssistantEvent.Delta)
    case agent.EventToolExecutionStart:
        fmt.Printf("Tool: %s args: %v\n", event.ToolName, event.ToolArgs)
    case agent.EventToolExecutionUpdate:
        fmt.Printf("Progress: %v\n", event.ToolResult)
    case agent.EventToolExecutionEnd:
        fmt.Printf("Done, isError: %v\n", event.ToolIsError)
    }
}
```

---

## Project Structure

```
chonk-agent/
├── agent/
│   ├── agent.go              # Agent struct and options
│   ├── types.go              # Context and result types
│   ├── event.go              # AgentEvent definitions
│   ├── event_channel.go      # EventChannel subscription
│   ├── loop.go               # Core agent loop
│   ├── run.go                # Run control (Prompt/Continue/Abort)
│   ├── queue.go              # MessageQueue (Steering/Follow-up)
│   ├── tool.go               # Tool definition
│   ├── tool_executor.go      # Tool execution with hook support
│   ├── background_tasks.go   # Background task management
│   ├── stream.go             # StreamFn definition
│   └── errors.go             # Error types
├── examples/
│   └── basic/
│       └── main.go
├── go.mod
└── README.md
```

---

## License

MIT
