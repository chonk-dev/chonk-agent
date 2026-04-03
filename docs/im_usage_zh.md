# IM 接入示例：何时 Prompt / Steer / FollowUp

本示例演示在 IM 场景下的最小路由逻辑：

- 新消息且 Agent 空闲 -> Prompt
- 正在运行时：
  - 需要打断/纠偏 -> Steer
  - 只想排队追加 -> FollowUp

## 示例代码

```go
package main

import (
	"context"
	"errors"

	"github.com/chonk-dev/chonk-agent/agent"
)

// IncomingMessage 表示 IM 入站消息（可按你自己的字段扩展）
type IncomingMessage struct {
	Text          string
	UrgentInterrupt bool // true: 需要打断当前生成
}

// handleIncomingMessage 处理 IM 消息
func handleIncomingMessage(ctx context.Context, a *agent.Agent, msg IncomingMessage) error {
	userMsg := agent.NewUserMessage(agent.NewTextContent(msg.Text))

	// 优先尝试 Prompt：若 Agent 空闲，直接开启新一轮
	if err := a.Prompt(ctx, userMsg); err == nil {
		return nil
	} else if !errors.Is(err, agent.ErrAgentBusy) {
		return err
	}

	// 走到这里，说明 Agent 正在运行
	if msg.UrgentInterrupt {
		// 需要打断/纠偏：使用 Steer
		a.Steer(userMsg)
		return nil
	}

	// 只想排队追加：使用 FollowUp
	a.FollowUp(userMsg)
	return nil
}
```

## 说明

- `Prompt`：用于“新一轮对话”的入口。
- `Steer`：用于插队/打断/纠偏，适合用户急切更正或安全拦截。
- `FollowUp`：用于不打断当前生成、在本轮结束后追加处理。
