package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/chonk-dev/chonk-agent/agent"
	chonkai "github.com/chonk-dev/chonk-ai"
	_ "github.com/chonk-dev/chonk-ai/provider/openai"
)

// TestAgentBasicPrompt 测试基础对话
func TestAgentBasicPrompt(t *testing.T) {
	if testOpenAIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	// 使用 WithAPIKey 设置 API Key（推荐方式）
	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are a helpful assistant."),
		agent.WithAPIKey(testOpenAIKey),
	)

	events := a.Subscribe()
	receivedText := false
	done := make(chan struct{})

	go func() {
		defer close(done)
		for event := range events.Events() {
			t.Logf("Received event: %s", event.Type)
			if event.Type == agent.EventMessageUpdate && event.AssistantEvent != nil {
				if event.AssistantEvent.Type == chonkai.EventTextDelta {
					receivedText = true
					t.Logf("Received delta: %s", event.AssistantEvent.Delta)
				}
			}
			if event.Type == agent.EventAgentEnd {
				t.Logf("Agent ended with %d messages", len(event.Messages))
			}
		}
		t.Log("Event channel closed")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Logf("Sending prompt...")
	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Say hello in one word."}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	t.Logf("Waiting for idle...")
	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle failed: %v", err)
	}

	// 等待事件处理完成
	select {
	case <-done:
		t.Log("Done channel closed")
	case <-time.After(5 * time.Second):
		t.Log("Timeout waiting for events")
	}

	if len(a.State.Messages) < 2 {
		t.Errorf("Expected at least 2 messages, got %d", len(a.State.Messages))
	}

	// 检查助手消息内容
	if len(a.State.Messages) >= 2 {
		if assistantMsg, ok := a.State.Messages[1].(chonkai.AssistantMessage); ok {
			t.Logf("Assistant message: StopReason=%s, ErrorMsg=%s", assistantMsg.StopReason, assistantMsg.ErrorMsg)
			if len(assistantMsg.Content) > 0 {
				t.Logf("Assistant content: %v", assistantMsg.Content)
				receivedText = true
			}
		}
	}

	if !receivedText {
		t.Error("Expected to receive text content")
	}

	t.Logf("Final state: %d messages, isStreaming=%v", len(a.State.Messages), a.State.IsStreaming)
}
