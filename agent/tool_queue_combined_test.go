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

// TestAgentToolWithSteering 测试工具调用中使用转向
func TestAgentToolWithSteering(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	blockedTool := agent.NewTool(
		"blocked_tool",
		"A tool that will be blocked",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			t.Error("This tool should never be called")
			return agent.ToolResult{}, nil
		},
		agent.WithSchema[struct{}](),
	)

	beforeToolCallCalled := false

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are a tool-calling assistant. When the user asks you to call a tool, you MUST call it immediately without any text response first."),
		agent.WithTools(blockedTool),
		agent.WithAPIKey(testAPIKey),
		agent.WithBeforeToolCall(func(ctx context.Context, bctx agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
			beforeToolCallCalled = true
			t.Logf("beforeToolCall called for tool: %s", bctx.ToolCall.Name)

			return &agent.BeforeToolCallResult{
				Block:  true,
				Reason: "This tool is blocked for testing",
			}, nil
		}),
	)

	events := a.Subscribe()
	toolBlocked := false

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventToolExecutionEnd {
				if event.ToolIsError {
					toolBlocked = true
				}
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Call the blocked_tool function now."}); err != nil {
		t.Logf("Prompt returned: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Logf("WaitIdle returned: %v", err)
	}

	time.Sleep(2 * time.Second)

	t.Logf("beforeToolCall called: %v", beforeToolCallCalled)
	t.Logf("Tool blocked: %v", toolBlocked)

	if !beforeToolCallCalled {
		t.Error("Expected beforeToolCall hook to be called")
	}
	if !toolBlocked {
		t.Error("Expected tool to be blocked by beforeToolCall hook")
	}
}

// TestAgentToolWithFollowUp 测试工具调用后使用后续消息
func TestAgentToolWithFollowUp(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	modifiableTool := agent.NewTool(
		"modifiable_tool",
		"A tool with modifiable result",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			t.Logf("Tool called")
			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Original result"},
				},
				Details: map[string]any{"original": true},
			}, nil
		},
		agent.WithSchema[struct{}](),
	)

	afterToolCallCalled := false
	resultModified := false

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are helpful."),
		agent.WithTools(modifiableTool),
		agent.WithAPIKey(testAPIKey),
		agent.WithAfterToolCall(func(ctx context.Context, actx agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
			afterToolCallCalled = true
			t.Logf("afterToolCall called for tool: %s", actx.ToolCall.Name)

			resultModified = true
			return &agent.AfterToolCallResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Modified result"},
				},
				Details: map[string]any{"modified": true},
			}, nil
		}),
	)

	events := a.Subscribe()
	toolExecuted := false

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventToolExecutionEnd {
				toolExecuted = true
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Use the modifiable tool"}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	t.Logf("afterToolCall called: %v", afterToolCallCalled)
	t.Logf("Result modified: %v", resultModified)
	t.Logf("Tool executed: %v", toolExecuted)

	if !toolExecuted {
		t.Fatal("Expected tool to be executed")
	}
	if !afterToolCallCalled {
		t.Error("Expected afterToolCall hook to be called")
	}
	if !resultModified {
		t.Error("Expected result to be modified by afterToolCall hook")
	}
}

// TestAgentMultipleToolsWithSteering 测试多个工具调用中使用转向
func TestAgentMultipleToolsWithSteering(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	executionOrder := []string{}

	tool1 := agent.NewTool(
		"tool_first",
		"Execute the first step of the workflow. Must be called before tool_second.",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			executionOrder = append(executionOrder, "tool1")
			time.Sleep(100 * time.Millisecond)

			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "First step complete. Now call tool_second."},
				},
			}, nil
		},
		agent.WithSchema[struct{}](),
	)

	tool2 := agent.NewTool(
		"tool_second",
		"Execute the second step of the workflow. Must be called after tool_first.",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			executionOrder = append(executionOrder, "tool2")
			time.Sleep(100 * time.Millisecond)

			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Second step complete."},
				},
			}, nil
		},
		agent.WithSchema[struct{}](),
	)

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are a tool executor. Always call all requested tools. Never skip a tool call."),
		agent.WithTools(tool1, tool2),
		agent.WithAPIKey(testAPIKey),
	)

	events := a.Subscribe()
	toolCount := 0

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventToolExecutionStart {
				toolCount++
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Call tool_first, then call tool_second. Both tool calls are required."}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Logf("WaitIdle returned: %v", err)
	}

	time.Sleep(2 * time.Second)

	t.Logf("Execution order: %v", executionOrder)
	t.Logf("Tools started: %d", toolCount)

	if toolCount < 2 {
		t.Errorf("Expected both tools to be called, got %d tool start events", toolCount)
	}
	if len(executionOrder) < 2 {
		t.Errorf("Expected both tools to execute, got execution order: %v", executionOrder)
	}
}

// TestAgentToolAndFollowUpChain 测试工具调用链 + 后续消息
func TestAgentToolAndFollowUpChain(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	summarizeTool := agent.NewTool(
		"summarize",
		"Summarize content",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			t.Logf("Summarize tool called")
			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Summary complete"},
				},
			}, nil
		},
		agent.WithSchema[struct{}](),
	)

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are helpful."),
		agent.WithTools(summarizeTool),
		agent.WithAPIKey(testAPIKey),
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

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Summarize this text"}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	t.Logf("Messages: %d", messageCount)
	t.Logf("Final state messages: %d", len(a.State.Messages))

	if messageCount < 1 {
		t.Error("Expected at least 1 message")
	}
}

// TestAgentConcurrentToolAndSteering 测试并发工具执行中的转向
func TestAgentConcurrentToolAndSteering(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	parallelExecuted := int32(0)

	parallelTool1 := agent.NewTool(
		"parallel_1",
		"Fetch data from source A. Must be called as part of the two-source data fetch.",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			atomic.AddInt32(&parallelExecuted, 1)
			time.Sleep(200 * time.Millisecond)

			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Data from source A retrieved."},
				},
			}, nil
		},
		agent.WithSchema[struct{}](),
	)

	parallelTool2 := agent.NewTool(
		"parallel_2",
		"Fetch data from source B. Must be called as part of the two-source data fetch.",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			atomic.AddInt32(&parallelExecuted, 1)
			time.Sleep(200 * time.Millisecond)

			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Data from source B retrieved."},
				},
			}, nil
		},
		agent.WithSchema[struct{}](),
	)

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are a tool executor. Always call all requested tools. Never skip a tool call."),
		agent.WithTools(parallelTool1, parallelTool2),
		agent.WithAPIKey(testAPIKey),
	)

	events := a.Subscribe()
	toolStartCount := 0

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventToolExecutionStart {
				toolStartCount++
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Call parallel_1 and parallel_2. Both tool calls are required."}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Logf("WaitIdle returned: %v", err)
	}

	time.Sleep(2 * time.Second)
	t.Logf("Parallel tools executed: %d", parallelExecuted)
	t.Logf("Tool start events: %d", toolStartCount)

	if toolStartCount < 2 {
		t.Errorf("Expected both parallel tools to start, got %d start events", toolStartCount)
	}
	if int(parallelExecuted) < 2 {
		t.Errorf("Expected both parallel tools to execute, got %d executions", parallelExecuted)
	}
}
