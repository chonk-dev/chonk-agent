package agent

import (
	"sync"
)

// MessageQueue 消息队列
type MessageQueue struct {
	mu   sync.Mutex
	msgs []AgentMessage
	mode QueueMode
}

// NewMessageQueue 创建消息队列
func NewMessageQueue(mode QueueMode) *MessageQueue {
	return &MessageQueue{
		msgs: make([]AgentMessage, 0),
		mode: mode,
	}
}

// Enqueue 添加消息到队列
func (q *MessageQueue) Enqueue(msg AgentMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.msgs = append(q.msgs, msg)
}

// Drain 取出队列中的消息
func (q *MessageQueue) Drain() []AgentMessage {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.mode == ModeAll {
		drained := make([]AgentMessage, len(q.msgs))
		copy(drained, q.msgs)
		q.msgs = q.msgs[:0]
		return drained
	}

	if len(q.msgs) == 0 {
		return nil
	}

	first := q.msgs[0]
	q.msgs = q.msgs[1:]
	return []AgentMessage{first}
}

// Clear 清空队列
func (q *MessageQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.msgs = q.msgs[:0]
}

// HasItems 检查队列是否有消息
func (q *MessageQueue) HasItems() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.msgs) > 0
}
