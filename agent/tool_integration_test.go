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

// TestAgentWithTools 测试工具调用
func TestAgentWithTools(t *testing.T) {
	apiKey := os.Getenv("CHONKAI_TEST_OPENAI_KEY")
	baseURL := os.Getenv("CHONKAI_TEST_OPENAI_BASE")
	modelID := os.Getenv("CHONKAI_TEST_OPENAI_MODEL")

	if apiKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       modelID,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  baseURL,
	}

	weatherTool := agent.NewTool(
		"get_weather",
		"Get current weather for a location",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			city := params["city"].(string)
			t.Logf("Tool called: get_weather(%s)", city)

			update(agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Fetching weather data..."},
				},
				Details: map[string]any{"status": "loading", "city": city},
			})

			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "25°C in " + city},
				},
				Details: map[string]any{"temperature": 25, "city": city, "condition": "sunny"},
			}, nil
		},
		agent.WithLabel("Weather Tool"),
		agent.WithSchema[struct {
			City string `json:"city" jsonschema:"City name"`
		}](),
	)

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are a helpful assistant. Use tools when appropriate."),
		agent.WithTools(weatherTool),
		agent.WithAPIKey(apiKey),
	)

	events := a.Subscribe()
	toolCallReceived := false
	toolResultReceived := false

	go func() {
		for event := range events.Events() {
			t.Logf("Event: %s", event.Type)

			switch event.Type {
			case agent.EventToolExecutionStart:
				t.Logf("Tool execution started: %s with args: %v", event.ToolName, event.ToolArgs)
				toolCallReceived = true

			case agent.EventToolExecutionUpdate:
				if event.ToolResult != nil && len(event.ToolResult.Content) > 0 {
					t.Logf("Tool update: %v", event.ToolResult.Content)
				}

			case agent.EventToolExecutionEnd:
				t.Logf("Tool execution completed: %s, isError: %v", event.ToolName, event.ToolIsError)
				toolResultReceived = true
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Logf("Sending prompt...")
	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "What's the weather in Beijing?"}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle failed: %v", err)
	}

	time.Sleep(3 * time.Second)

	t.Logf("Messages count: %d", len(a.State.Messages))
	for i, msg := range a.State.Messages {
		t.Logf("Message %d: %T", i, msg)
		switch m := msg.(type) {
		case chonkai.UserMessage:
			t.Logf("  User: %s", m.RawText)
		case chonkai.AssistantMessage:
			t.Logf("  Assistant: StopReason=%s, Content=%d blocks", m.StopReason, len(m.Content))
		case chonkai.ToolResultMessage:
			t.Logf("  ToolResult: ToolName=%s, Content=%v", m.ToolName, m.Content)
		}
	}

	if !toolCallReceived {
		t.Error("Expected tool execution to start")
	}
	if !toolResultReceived {
		t.Error("Expected tool execution to complete with a result")
	}

	t.Logf("Tool call received: %v, tool result received: %v", toolCallReceived, toolResultReceived)

}
