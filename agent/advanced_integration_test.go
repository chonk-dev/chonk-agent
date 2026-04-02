package agent_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chonk-dev/chonk-agent/agent"
	chonkai "github.com/chonk-dev/chonk-ai"
	_ "github.com/chonk-dev/chonk-ai/provider/openai"
)

var testAPIKey = getEnv("CHONKAI_TEST_OPENAI_KEY", "")

// TestAgentMultiTurnToolCalls 测试多轮工具调用
func TestAgentMultiTurnToolCalls(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	callCount := int32(0)

	multiTurnTool := agent.NewTool(
		"process_files",
		"Process files in the system. Call with action='scan' to find files, then call with action='delete' to remove them.",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			count := atomic.AddInt32(&callCount, 1)
			t.Logf("Tool called (call #%d)", count)

			if count == 1 {
				return agent.ToolResult{
					Content: []chonkai.UserContent{
						chonkai.TextContent{Type: "text", Text: "Found 3 files. Now call process_files again with action='delete' to remove them."},
					},
					Details: map[string]any{"files": 3, "action": "pending"},
				}, nil
			}
			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Deleted 3 files successfully."},
				},
				Details: map[string]any{"files": 3, "action": "deleted"},
			}, nil
		},
		agent.WithSchema[struct {
			Action string `json:"action" jsonschema:"Action to perform: 'scan' to find files, 'delete' to remove them"`
		}](),
	)

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are a file management assistant. When you receive tool results asking you to call a tool again, you MUST call it again as instructed."),
		agent.WithTools(multiTurnTool),
		agent.WithAPIKey(testAPIKey),
	)

	events := a.Subscribe()
	toolCallCount := 0
	turnCount := 0

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventToolExecutionStart {
				toolCallCount++
				t.Logf("Tool call #%d started", toolCallCount)
			}
			if event.Type == agent.EventTurnEnd {
				turnCount++
				t.Logf("Turn #%d ended", turnCount)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Use process_files with action='scan' to find files, then use it again with action='delete' to remove them."}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle failed: %v", err)
	}

	time.Sleep(3 * time.Second)

	t.Logf("Total tool calls: %d", toolCallCount)
	t.Logf("Total turns: %d", turnCount)
	t.Logf("Final messages: %d", len(a.State.Messages))

	if toolCallCount < 2 {
		t.Errorf("Expected at least 2 tool calls (tool called twice), got %d", toolCallCount)
	}

	if turnCount < 2 {
		t.Error("Expected at least 2 turns for multi-turn conversation")
	}
}

// TestAgentUserInterruptTool 测试用户消息打断工具执行
func TestAgentUserInterruptTool(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	slowToolCallCount := int32(0)

	slowTool := agent.NewTool(
		"slow_tool",
		"A slow running tool",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			atomic.AddInt32(&slowToolCallCount, 1)
			t.Logf("Slow tool started (call #%d)", slowToolCallCount)

			for i := range 5 {
				select {
				case <-ctx.Done():
					t.Logf("Slow tool cancelled")
					return agent.ToolResult{}, ctx.Err()
				case <-time.After(500 * time.Millisecond):
					update(agent.ToolResult{
						Content: []chonkai.UserContent{
							chonkai.TextContent{Type: "text", Text: "Still working..."},
						},
						Details: map[string]any{"progress": i + 1},
					})
				}
			}

			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Done"},
				},
			}, nil
		},
		agent.WithSchema[struct{}](),
	)

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are helpful."),
		agent.WithTools(slowTool),
		agent.WithAPIKey(testAPIKey),
	)

	events := a.Subscribe()
	interrupted := false
	messageCount := 0

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventToolExecutionStart {
				t.Logf("Tool execution started")

				go func() {
					time.Sleep(1 * time.Second)
					t.Logf("Injecting user interrupt message...")
					a.Steer(chonkai.UserMessage{RawText: "Stop and tell me the current status"})
					interrupted = true
				}()
			}
			if event.Type == agent.EventMessageEnd {
				messageCount++
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Run the slow tool"}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Logf("WaitIdle returned: %v", err)
	}

	time.Sleep(3 * time.Second)

	t.Logf("Slow tool call count: %d", slowToolCallCount)
	t.Logf("Interrupted: %v", interrupted)
	t.Logf("Message count: %d", messageCount)
	t.Logf("Final messages: %d", len(a.State.Messages))

	if slowToolCallCount < 1 {
		t.Error("Expected slow tool to be called")
	}
}

// TestAgentConcurrentUserMessages 测试并发用户消息
func TestAgentConcurrentUserMessages(t *testing.T) {
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

	events := a.Subscribe()
	messageCount := 0

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventMessageEnd {
				messageCount++
				t.Logf("Message #%d ended", messageCount)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Count to 3"}); err != nil {
		t.Fatalf("First prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("First WaitIdle failed: %v", err)
	}

	firstMessageCount := messageCount

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Now count to 5"}); err != nil {
		t.Fatalf("Second prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("Second WaitIdle failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	t.Logf("Total messages: %d", messageCount)
	t.Logf("Messages after first prompt: %d", firstMessageCount)
	t.Logf("Final state messages: %d", len(a.State.Messages))

	if messageCount <= firstMessageCount {
		t.Error("Expected second prompt to generate additional messages")
	}

	if len(a.State.Messages) < 4 {
		t.Errorf("Expected at least 4 messages (2 user + 2 assistant), got %d", len(a.State.Messages))
	}
}

// TestAgentSteerDuringToolExecution 测试工具执行期间的转向
func TestAgentSteerDuringToolExecution(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	executionStep := 0

	stepTool := agent.NewTool(
		"step_tool",
		"A tool with multiple steps",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			steps := []string{"Step 1", "Step 2", "Step 3", "Step 4", "Step 5"}

			for i, step := range steps {
				executionStep = i + 1

				select {
				case <-ctx.Done():
					t.Logf("Tool cancelled at step %d", i+1)
					return agent.ToolResult{}, ctx.Err()
				case <-time.After(300 * time.Millisecond):
					update(agent.ToolResult{
						Content: []chonkai.UserContent{
							chonkai.TextContent{Type: "text", Text: step},
						},
						Details: map[string]any{"currentStep": i + 1, "totalSteps": len(steps)},
					})
				}
			}

			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "All steps completed"},
				},
			}, nil
		},
		agent.WithSchema[struct{}](),
	)

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are helpful."),
		agent.WithTools(stepTool),
		agent.WithAPIKey(testAPIKey),
	)

	events := a.Subscribe()
	steerInjected := false
	finalStep := 0

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventToolExecutionStart {
				t.Logf("Tool execution started")

				go func() {
					for executionStep < 2 {
						time.Sleep(100 * time.Millisecond)
					}

					t.Logf("Injecting steer message at step %d", executionStep)
					a.Steer(chonkai.UserMessage{RawText: "Skip remaining steps and report current progress"})
					steerInjected = true
					finalStep = executionStep
				}()
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Execute all steps"}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Logf("WaitIdle returned: %v", err)
	}

	time.Sleep(2 * time.Second)

	t.Logf("Steer injected: %v", steerInjected)
	t.Logf("Final execution step: %d", finalStep)
	t.Logf("Final messages: %d", len(a.State.Messages))

	if !steerInjected {
		t.Error("Expected steer message to be injected")
	}
}
