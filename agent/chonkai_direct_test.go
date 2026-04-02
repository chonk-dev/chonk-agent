package agent_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/chonk-dev/chonk-agent/agent"
	chonkai "github.com/chonk-dev/chonk-ai"
	_ "github.com/chonk-dev/chonk-ai/provider/openai"
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

var (
	testOpenAIBaseURL = getEnv("CHONKAI_TEST_OPENAI_BASE", "https://api.openai.com/v1")
	testOpenAIKey     = getEnv("CHONKAI_TEST_OPENAI_KEY", "")
	testOpenAIModel   = getEnv("CHONKAI_TEST_OPENAI_MODEL", "gpt-4o-mini")
)

// TestChonkAIDirect 直接测试 chonk-ai
func TestChonkAIDirect(t *testing.T) {
	if testOpenAIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Logf("Streaming from %s at %s", testOpenAIModel, testOpenAIBaseURL)

	stream := chonkai.Stream(ctx, model,
		&chonkai.Context{
			SystemPrompt: "You are a helpful assistant.",
			Messages: []chonkai.Message{
				chonkai.UserMessage{RawText: "Say hello in one word."},
			},
		},
		&chonkai.StreamOptions{
			APIKey: testOpenAIKey,
		},
	)

	for event := range stream.Events() {
		t.Logf("Event: %s", event.Type)
		if event.Type == chonkai.EventTextDelta {
			t.Logf("Delta: %s", event.Delta)
		}
	}

	result := stream.Result()
	t.Logf("Result: StopReason=%s, ErrorMsg=%s, Content=%v", result.StopReason, result.ErrorMsg, result.Content)

	if result.StopReason != chonkai.StopReasonStop {
		t.Errorf("Expected StopReasonStop, got %s (%s)", result.StopReason, result.ErrorMsg)
	}
}

// TestAgentWithDirectStream 使用直接流测试 Agent
func TestAgentWithDirectStream(t *testing.T) {
	if testOpenAIKey == "" {
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
		agent.WithAPIKey(testOpenAIKey),
	)

	events := a.Subscribe()
	receivedText := false

	go func() {
		for event := range events.Events() {
			t.Logf("Agent event: %s", event.Type)
			if event.Type == agent.EventMessageUpdate && event.AssistantEvent != nil {
				if event.AssistantEvent.Type == chonkai.EventTextDelta {
					receivedText = true
					t.Logf("Received delta: %s", event.AssistantEvent.Delta)
				}
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Say hello in one word."}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	if len(a.State.Messages) < 2 {
		t.Errorf("Expected at least 2 messages, got %d", len(a.State.Messages))
	}

	if assistantMsg, ok := a.State.Messages[1].(chonkai.AssistantMessage); ok {
		t.Logf("Assistant: StopReason=%s, ErrorMsg=%s", assistantMsg.StopReason, assistantMsg.ErrorMsg)
		if len(assistantMsg.Content) > 0 {
			t.Logf("Content: %v", assistantMsg.Content)
			receivedText = true
		}
	}

	if !receivedText {
		t.Error("Expected to receive text")
	}
}
