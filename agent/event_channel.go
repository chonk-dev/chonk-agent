package agent

import (
	"iter"
	"sync"
)

// EventChannel 事件订阅 channel
type EventChannel struct {
	ch     chan AgentEvent
	done   chan struct{}
	mu     sync.RWMutex
	closed bool
}

// NewEventChannel 创建事件订阅 channel
func NewEventChannel(bufSize int) *EventChannel {
	return &EventChannel{
		ch:   make(chan AgentEvent, bufSize),
		done: make(chan struct{}),
	}
}

// Send 发送事件（阻塞直到发送成功或 channel 关闭）
func (ec *EventChannel) Send(event AgentEvent) bool {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	if ec.closed {
		return false
	}

	select {
	case ec.ch <- event:
		return true
	case <-ec.done:
		return false
	}
}

// Events 返回事件迭代器
func (ec *EventChannel) Events() iter.Seq[AgentEvent] {
	return func(yield func(AgentEvent) bool) {
		defer func() {
			ec.mu.Lock()
			ec.closed = true
			ec.mu.Unlock()
			close(ec.done)
		}()

		for event := range ec.ch {
			if !yield(event) {
				return
			}
		}
	}
}

// Close 关闭 channel
func (ec *EventChannel) Close() {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	if !ec.closed {
		ec.closed = true
		close(ec.ch)
		close(ec.done)
	}
}
