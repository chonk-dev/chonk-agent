package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/chonk-dev/chonk-agent/agent"
	chonkai "github.com/chonk-dev/chonk-ai"
	_ "github.com/chonk-dev/chonk-ai/provider/openai"
)

// TestAgentSteering 测试转向消息功能
func TestAgentSteering(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are a helpful assistant."),
		agent.WithAPIKey(testAPIKey),
		agent.WithSteeringMode(agent.ModeOneAtATime),
	)

	events := a.Subscribe()
	messageCount := 0

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventMessageEnd {
				messageCount++
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Logf("Sending initial prompt...")
	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Say hello"}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// 验证队列操作
	a.Steer(chonkai.UserMessage{RawText: "Steer message"})

	if !a.HasQueuedMessages() {
		t.Error("Expected queued messages after Steer")
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	t.Logf("Total messages: %d", len(a.State.Messages))
	t.Logf("Events received: %d", messageCount)

	// 验证至少完成了初始对话 (user + assistant)
	if len(a.State.Messages) < 2 {
		t.Errorf("Expected at least 2 messages (initial user + assistant), got %d", len(a.State.Messages))
	}
}

// TestAgentFollowUp 测试后续消息功能
func TestAgentFollowUp(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are a helpful assistant."),
		agent.WithAPIKey(testAPIKey),
		agent.WithFollowUpMode(agent.ModeOneAtATime),
	)

	events := a.Subscribe()
	messageCount := 0

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventMessageEnd {
				messageCount++
			}
			if event.Type == agent.EventAgentEnd {
				t.Logf("Agent ended with %d messages", len(event.Messages))
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Logf("Sending initial prompt...")
	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Say hello"}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// 添加后续消息
	a.FollowUp(chonkai.UserMessage{RawText: "Now say goodbye"})

	if !a.HasQueuedMessages() {
		t.Error("Expected queued messages after FollowUp")
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	t.Logf("Total messages: %d", len(a.State.Messages))
	t.Logf("Events received: %d", messageCount)

	// 验证至少完成了初始对话 (user + assistant)
	if len(a.State.Messages) < 2 {
		t.Errorf("Expected at least 2 messages (initial user + assistant), got %d", len(a.State.Messages))
	}
}

// TestAgentSteeringMode 测试转向队列模式
func TestAgentSteeringMode(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	t.Run("OneAtATime", func(t *testing.T) {
		a := agent.NewAgent(
			agent.WithModel(model),
			agent.WithSystemPrompt("You are helpful."),
			agent.WithAPIKey(testAPIKey),
			agent.WithSteeringMode(agent.ModeOneAtATime),
		)

		// 添加多个转向消息
		a.Steer(chonkai.UserMessage{RawText: "Message 1"})
		a.Steer(chonkai.UserMessage{RawText: "Message 2"})
		a.Steer(chonkai.UserMessage{RawText: "Message 3"})

		// 验证队列有消息
		if !a.HasQueuedMessages() {
			t.Error("Expected queued messages")
		}

		// Drain 应该只返回一个消息（one-at-a-time）
		a.ClearSteeringQueue()
	})

	t.Run("All", func(t *testing.T) {
		a := agent.NewAgent(
			agent.WithModel(model),
			agent.WithSystemPrompt("You are helpful."),
			agent.WithAPIKey(testAPIKey),
			agent.WithSteeringMode(agent.ModeAll),
		)

		// 添加多个转向消息
		a.Steer(chonkai.UserMessage{RawText: "Message 1"})
		a.Steer(chonkai.UserMessage{RawText: "Message 2"})

		if !a.HasQueuedMessages() {
			t.Error("Expected queued messages")
		}

		a.ClearSteeringQueue()
	})
}

// TestAgentFollowUpMode 测试后续队列模式
func TestAgentFollowUpMode(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	t.Run("OneAtATime", func(t *testing.T) {
		a := agent.NewAgent(
			agent.WithModel(model),
			agent.WithSystemPrompt("You are helpful."),
			agent.WithAPIKey(testAPIKey),
			agent.WithFollowUpMode(agent.ModeOneAtATime),
		)

		a.FollowUp(chonkai.UserMessage{RawText: "Message 1"})
		a.FollowUp(chonkai.UserMessage{RawText: "Message 2"})

		if !a.HasQueuedMessages() {
			t.Error("Expected queued messages")
		}

		a.ClearFollowUpQueue()
	})

	t.Run("All", func(t *testing.T) {
		a := agent.NewAgent(
			agent.WithModel(model),
			agent.WithSystemPrompt("You are helpful."),
			agent.WithAPIKey(testAPIKey),
			agent.WithFollowUpMode(agent.ModeAll),
		)

		a.FollowUp(chonkai.UserMessage{RawText: "Message 1"})
		a.FollowUp(chonkai.UserMessage{RawText: "Message 2"})

		if !a.HasQueuedMessages() {
			t.Error("Expected queued messages")
		}

		a.ClearFollowUpQueue()
	})
}

// TestAgentClearQueues 测试清除队列
func TestAgentClearQueues(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are helpful."),
		agent.WithAPIKey(testAPIKey),
	)

	// 添加转向消息
	a.Steer(chonkai.UserMessage{RawText: "Steer 1"})
	a.Steer(chonkai.UserMessage{RawText: "Steer 2"})

	// 添加后续消息
	a.FollowUp(chonkai.UserMessage{RawText: "Follow 1"})
	a.FollowUp(chonkai.UserMessage{RawText: "Follow 2"})

	if !a.HasQueuedMessages() {
		t.Error("Expected queued messages")
	}

	// 测试清除所有队列
	a.ClearAllQueues()

	if a.HasQueuedMessages() {
		t.Error("Expected no queued messages after ClearAllQueues")
	}
}

// TestAgentSteeringAndFollowUpCombined 测试转向和后续组合
func TestAgentSteeringAndFollowUpCombined(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are a helpful assistant."),
		agent.WithAPIKey(testAPIKey),
		agent.WithSteeringMode(agent.ModeOneAtATime),
		agent.WithFollowUpMode(agent.ModeOneAtATime),
	)

	events := a.Subscribe()
	messageCount := 0

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventMessageEnd {
				messageCount++
				t.Logf("Message %d ended", messageCount)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 发送初始消息
	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Start a task"}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// 同时添加转向和后续消息
	a.Steer(chonkai.UserMessage{RawText: "Actually, do this instead"})
	a.FollowUp(chonkai.UserMessage{RawText: "Then summarize"})

	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	t.Logf("Final message count: %d", len(a.State.Messages))
	t.Logf("Events received: %d", messageCount)

	// 验证至少完成了初始对话 (user + assistant)
	if len(a.State.Messages) < 2 {
		t.Errorf("Expected at least 2 messages (initial user + assistant), got %d", len(a.State.Messages))
	}
}
