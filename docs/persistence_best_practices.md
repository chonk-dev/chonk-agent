# 持久化保存最佳实践

本文总结在使用本库时，保存历史消息的最佳时机与推荐做法，并提供 `saveMessages`/`loadMessages` 的完整实现示例（含 text/image/thinking/tool_call 的全量保存）。

## 最佳时机（推荐）

最稳妥的时机是每次 `Prompt` 或 `Continue` 完成后保存：

- 运行已结束，`State.Messages` 不再被并发修改。
- 避免写到一半的消息或出现数据竞争。

```go
if err := a.Prompt(ctx, agent.NewUserMessage(agent.NewTextContent("你好"))); err != nil {
    return err
}
_ = saveMessages("history.json", a.State.Messages)
```

如果你在后台启动运行，也可以先 `WaitIdle` 后再保存：

```go
if err := a.WaitIdle(ctx); err != nil {
    return err
}
_ = saveMessages("history.json", a.State.Messages)
```

## 更高可靠性（可选）

如果你希望崩溃时尽量不丢数据，可以用事件驱动的增量保存，但注意：

- `SubscribeFunc` 回调是同步执行，不能做慢 IO。
- 推荐回调中只投递信号，落盘在后台 goroutine，必要时加防抖。

```go
saveCh := make(chan struct{}, 1)

a.SubscribeFunc(func(event agent.AgentEvent, _ context.Context) {
    if event.Type == agent.EventMessageEnd || event.Type == agent.EventAgentEnd {
        select {
        case saveCh <- struct{}{}:
        default:
        }
    }
})

go func() {
    for range saveCh {
        _ = saveMessages("history.json", a.State.Messages)
    }
}()
```

## 结论

- **默认场景**：在 `Prompt/Continue` 结束后保存。
- **高可靠场景**：事件触发 + 异步落盘（可加防抖与原子写入）。

---

## `saveMessages`/`loadMessages` 完整实现（全量内容）

说明：
- 支持 `text` / `image` / `thinking` / `tool_call`。
- 仅处理本库支持的三类消息：`UserMessage` / `AssistantMessage` / `ToolResultMessage`。

```go
package main

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/chonk-dev/chonk-agent/agent"
	chonkai "github.com/chonk-dev/chonk-ai"
)

type PersistBlock struct {
	Type string `json:"type"` // "text" | "image" | "thinking" | "tool_call"

	// text
	Text          string `json:"text,omitempty"`
	TextSignature string `json:"textSignature,omitempty"`

	// image
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`

	// thinking
	Thinking          string `json:"thinking,omitempty"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
	Redacted          bool   `json:"redacted,omitempty"`

	// tool_call
	ID               string          `json:"id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`
}

type PersistMessage struct {
	Role    string         `json:"role"` // user | assistant | tool
	Content []PersistBlock `json:"content,omitempty"`

	// assistant metadata
	Api        string        `json:"api,omitempty"`
	Provider   string        `json:"provider,omitempty"`
	Model      string        `json:"model,omitempty"`
	ResponseID string        `json:"responseId,omitempty"`
	Usage      chonkai.Usage `json:"usage,omitempty"`
	StopReason string        `json:"stopReason,omitempty"`
	ErrorMsg   string        `json:"errorMessage,omitempty"`
	Timestamp  time.Time     `json:"timestamp"`

	// tool result metadata
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	ToolIsErr  bool   `json:"toolIsError,omitempty"`
}

func saveMessages(path string, messages []agent.AgentMessage) error {
	items := make([]PersistMessage, 0, len(messages))
	for _, m := range messages {
		if p, ok := toPersist(m); ok {
			items = append(items, p)
		}
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadMessages(path string) ([]agent.AgentMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var items []PersistMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	out := make([]agent.AgentMessage, 0, len(items))
	for _, p := range items {
		msg, err := fromPersist(p)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func toPersist(m agent.AgentMessage) (PersistMessage, bool) {
	switch msg := m.(type) {
	case chonkai.UserMessage:
		return PersistMessage{
			Role:      "user",
			Content:   userContentToBlocks(msg.Content),
			Timestamp: msg.Timestamp,
		}, true
	case chonkai.AssistantMessage:
		return PersistMessage{
			Role:       "assistant",
			Content:    assistantContentToBlocks(msg.Content),
			Api:        string(msg.Api),
			Provider:   string(msg.Provider),
			Model:      msg.Model,
			ResponseID: msg.ResponseID,
			Usage:      msg.Usage,
			StopReason: string(msg.StopReason),
			ErrorMsg:   msg.ErrorMsg,
			Timestamp:  msg.Timestamp,
		}, true
	case chonkai.ToolResultMessage:
		return PersistMessage{
			Role:       "tool",
			Content:    userContentToBlocks(msg.Content),
			ToolCallID: msg.ToolCallID,
			ToolName:   msg.ToolName,
			ToolIsErr:  msg.IsError,
			Timestamp:  msg.Timestamp,
		}, true
	}
	return PersistMessage{}, false
}

func fromPersist(p PersistMessage) (agent.AgentMessage, error) {
	switch p.Role {
	case "user":
		return chonkai.UserMessage{
			Content:   blocksToUserContent(p.Content),
			Timestamp: p.Timestamp,
		}, nil
	case "assistant":
		return chonkai.AssistantMessage{
			Content:    blocksToAssistantContent(p.Content),
			Api:        chonkai.Api(p.Api),
			Provider:   chonkai.ProviderID(p.Provider),
			Model:      p.Model,
			ResponseID: p.ResponseID,
			Usage:      p.Usage,
			StopReason: chonkai.StopReason(p.StopReason),
			ErrorMsg:   p.ErrorMsg,
			Timestamp:  p.Timestamp,
		}, nil
	case "tool":
		return chonkai.ToolResultMessage{
			ToolCallID: p.ToolCallID,
			ToolName:   p.ToolName,
			IsError:    p.ToolIsErr,
			Content:    blocksToUserContent(p.Content),
			Timestamp:  p.Timestamp,
		}, nil
	default:
		return nil, errors.New("unknown role")
	}
}

func userContentToBlocks(content []chonkai.UserContent) []PersistBlock {
	out := make([]PersistBlock, 0, len(content))
	for _, c := range content {
		switch v := c.(type) {
		case chonkai.TextContent:
			out = append(out, PersistBlock{
				Type:          "text",
				Text:          v.Text,
				TextSignature: v.TextSignature,
			})
		case chonkai.ImageContent:
			out = append(out, PersistBlock{
				Type:     "image",
				Data:     v.Data,
				MimeType: v.MimeType,
			})
		}
	}
	return out
}

func assistantContentToBlocks(content []chonkai.ContentBlock) []PersistBlock {
	out := make([]PersistBlock, 0, len(content))
	for _, c := range content {
		switch v := c.(type) {
		case chonkai.TextContent:
			out = append(out, PersistBlock{
				Type:          "text",
				Text:          v.Text,
				TextSignature: v.TextSignature,
			})
		case chonkai.ImageContent:
			out = append(out, PersistBlock{
				Type:     "image",
				Data:     v.Data,
				MimeType: v.MimeType,
			})
		case chonkai.ThinkingContent:
			out = append(out, PersistBlock{
				Type:              "thinking",
				Thinking:          v.Thinking,
				ThinkingSignature: v.ThinkingSignature,
				Redacted:          v.Redacted,
			})
		case chonkai.ToolCall:
			out = append(out, PersistBlock{
				Type:             "tool_call",
				ID:               v.ID,
				Name:             v.Name,
				Arguments:        v.Arguments,
				ThoughtSignature: v.ThoughtSignature,
			})
		}
	}
	return out
}

func blocksToUserContent(items []PersistBlock) []chonkai.UserContent {
	out := make([]chonkai.UserContent, 0, len(items))
	for _, b := range items {
		switch b.Type {
		case "text":
			out = append(out, chonkai.TextContent{
				Type:          "text",
				Text:          b.Text,
				TextSignature: b.TextSignature,
			})
		case "image":
			out = append(out, chonkai.ImageContent{
				Type:     "image",
				Data:     b.Data,
				MimeType: b.MimeType,
			})
		}
	}
	return out
}

func blocksToAssistantContent(items []PersistBlock) []chonkai.ContentBlock {
	out := make([]chonkai.ContentBlock, 0, len(items))
	for _, b := range items {
		switch b.Type {
		case "text":
			out = append(out, chonkai.TextContent{
				Type:          "text",
				Text:          b.Text,
				TextSignature: b.TextSignature,
			})
		case "image":
			out = append(out, chonkai.ImageContent{
				Type:     "image",
				Data:     b.Data,
				MimeType: b.MimeType,
			})
		case "thinking":
			out = append(out, chonkai.ThinkingContent{
				Type:              "thinking",
				Thinking:          b.Thinking,
				ThinkingSignature: b.ThinkingSignature,
				Redacted:          b.Redacted,
			})
		case "tool_call":
			out = append(out, chonkai.ToolCall{
				Type:             "tool_call",
				ID:               b.ID,
				Name:             b.Name,
				Arguments:        b.Arguments,
				ThoughtSignature: b.ThoughtSignature,
			})
		}
	}
	return out
}
```
