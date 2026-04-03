# Agent 方法说明（中文）

本文按“做什么/什么时候用”说明 Agent 的公开方法，便于接入 IM 或业务系统。

## 创建与配置

### `NewAgent(opts ...AgentOption) *Agent`
- 做什么：创建 Agent 实例。
- 什么时候用：程序启动时或新会话创建时。

常用选项（举例）：
- `WithSystemPrompt(...)`
- `WithModel(...)`
- `WithTools(...)`
- `WithMessages(...)`
- `WithGetApiKey(...)`

## 状态设置

### `SetTools(tools []Tool)`
- 做什么：替换工具列表（会复制 slice）。
- 什么时候用：你需要动态更新工具集（建议在空闲时调用）。

### `SetMessages(messages []AgentMessage)`
- 做什么：替换消息历史（会复制 slice）。
- 什么时候用：启动时加载历史消息或重置对话。

## 运行控制

### `Prompt(ctx, messages ...AgentMessage) error`
- 做什么：启动一次新的对话轮次。
- 什么时候用：收到用户新消息时。

### `Continue(ctx) error`
- 做什么：从已有对话继续生成下一轮。
- 什么时候用：你已经有消息历史，想让模型继续。
- 约束：最后一条必须是 user/toolResult；若是 assistant，会先尝试消费 steering/followUp 队列。

### `Abort()`
- 做什么：取消当前运行。
- 什么时候用：用户中途打断、系统超时或需要立即停止。

### `WaitIdle(ctx) error`
- 做什么：等待当前运行结束。
- 什么时候用：需要在保存历史/更新配置前确保运行完成。

### `Reset()`
- 做什么：清空消息、状态、队列。
- 什么时候用：开始一个全新会话或手动“清空聊天记录”。

## 事件订阅

### `Subscribe() *EventChannel`
- 做什么：返回事件通道，可 `range Events()` 读取事件。
- 什么时候用：需要异步观察事件流（UI 刷新、日志）。
- 注意：用完后调用 `Unsubscribe` 或 `EventChannel.Close()`。

### `SubscribeFunc(fn func(AgentEvent, context.Context)) func()`
- 做什么：注册回调监听事件，返回取消订阅函数。
- 什么时候用：需要同步监听并影响本次运行（例如埋点、审计）。
- 注意：回调是同步执行，避免慢 IO。

### `Unsubscribe(ec *EventChannel)`
- 做什么：取消订阅并关闭对应 channel。
- 什么时候用：不再需要事件时，释放资源。

## 队列管理（IM 重点）

### `Steer(msg AgentMessage)`
- 做什么：插队/纠偏消息，加入 steering 队列。
- 什么时候用：用户打断或需要优先处理的新指令。
- 说明：不会自动触发运行，除非当前正在运行或之后调用 `Prompt/Continue`。

### `FollowUp(msg AgentMessage)`
- 做什么：追加消息，加入 followUp 队列。
- 什么时候用：不打断当前生成，只想在结束后继续处理。

### `ClearSteeringQueue() / ClearFollowUpQueue() / ClearAllQueues()`
- 做什么：清空队列。
- 什么时候用：要丢弃排队消息或重置会话。

### `HasQueuedMessages() bool`
- 做什么：判断是否有排队消息。
- 什么时候用：调度层判断是否需要继续运行。

## 简短示例

```go
userMsg := agent.NewUserMessage(agent.NewTextContent("你好"))

// 空闲时直接 Prompt
if err := a.Prompt(ctx, userMsg); err == nil {
    return nil
}

// 运行中：根据紧急程度选择 Steer 或 FollowUp
a.Steer(userMsg)   // 需要打断
// 或
a.FollowUp(userMsg) // 只想追加
```
