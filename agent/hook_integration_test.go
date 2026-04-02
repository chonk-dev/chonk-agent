package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/chonk-dev/chonk-agent/agent"
	chonkai "github.com/chonk-dev/chonk-ai"
	_ "github.com/chonk-dev/chonk-ai/provider/openai"
)

// TestAgentBeforeToolCallHook 测试 beforeToolCall 钩子
func TestAgentBeforeToolCallHook(t *testing.T) {
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
					t.Logf("Tool execution ended with error (blocked)")
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
	t.Logf("Final messages: %d", len(a.State.Messages))

	if !beforeToolCallCalled {
		t.Error("Expected beforeToolCall hook to be called: model did not call the tool")
	}
	if !toolBlocked {
		t.Error("Expected tool to be blocked by beforeToolCall hook")
	}
}

// TestAgentAfterToolCallHook 测试 afterToolCall 钩子
func TestAgentAfterToolCallHook(t *testing.T) {
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
			t.Logf("Original result: %v", actx.Result.Content)

			resultModified = true
			return &agent.AfterToolCallResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Modified result by afterToolCall hook"},
				},
				Details: map[string]any{"modified": true, "original": actx.Result.Details},
			}, nil
		}),
	)

	events := a.Subscribe()
	toolExecuted := false

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventToolExecutionEnd {
				toolExecuted = true
				t.Logf("Tool execution ended, isError: %v", event.ToolIsError)
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
	t.Logf("Final messages: %d", len(a.State.Messages))

	if !afterToolCallCalled {
		t.Error("Expected afterToolCall hook to be called")
	}

	if !resultModified {
		t.Error("Expected result to be modified by afterToolCall hook")
	}

	if !toolExecuted {
		t.Error("Expected tool to be executed")
	}
}

// TestAgentBeforeToolCallConditionalBlock 测试条件性阻止工具
func TestAgentBeforeToolCallConditionalBlock(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	conditionalTool := agent.NewTool(
		"conditional_tool",
		"A tool with conditional blocking",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			t.Logf("Conditional tool called")
			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Tool executed"},
				},
			}, nil
		},
		agent.WithSchema[struct {
			ShouldBlock bool `json:"should_block"`
		}](),
	)

	blockCount := 0
	allowCount := 0

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are a tool executor. Always call the requested tool with the exact parameters specified. Never skip a tool call."),
		agent.WithTools(conditionalTool),
		agent.WithAPIKey(testAPIKey),
		agent.WithBeforeToolCall(func(ctx context.Context, bctx agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
			shouldBlock, _ := bctx.Args["should_block"].(bool)

			if shouldBlock {
				blockCount++
				t.Logf("Blocking tool (should_block=true)")
				return &agent.BeforeToolCallResult{
					Block:  true,
					Reason: "Blocked based on should_block parameter",
				}, nil
			}

			allowCount++
			t.Logf("Allowing tool (should_block=false)")
			return nil, nil
		}),
	)

	events := a.Subscribe()

	go func() {
		for range events.Events() {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Call conditional_tool with should_block=true"}); err != nil {
		t.Logf("Prompt 1 returned: %v", err)
	}
	if err := a.WaitIdle(ctx); err != nil {
		t.Logf("WaitIdle 1 returned: %v", err)
	}

	time.Sleep(1 * time.Second)

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Call conditional_tool with should_block=false"}); err != nil {
		t.Fatalf("Prompt 2 failed: %v", err)
	}
	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle 2 failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	t.Logf("Block count: %d", blockCount)
	t.Logf("Allow count: %d", allowCount)
	t.Logf("Final messages: %d", len(a.State.Messages))

	if blockCount < 1 {
		t.Error("Expected at least 1 block")
	}
	if allowCount < 1 {
		t.Error("Expected at least 1 allow")
	}
}

// TestAgentAfterToolCallAudit 测试 afterToolCall 审计功能
func TestAgentAfterToolCallAudit(t *testing.T) {
	if testAPIKey == "" {
		t.Skip("Skipping: CHONKAI_TEST_OPENAI_KEY not set")
	}

	model := &chonkai.Model{
		ID:       testOpenAIModel,
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  testOpenAIBaseURL,
	}

	auditTool := agent.NewTool(
		"audit_tool",
		"A tool with audit trail",
		func(ctx context.Context, toolCallID string, params map[string]any, update agent.ToolUpdate) (agent.ToolResult, error) {
			t.Logf("Audit tool called")
			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: "Operation completed"},
				},
				Details: map[string]any{"operation": "delete", "target": "file.txt"},
			}, nil
		},
		agent.WithSchema[struct{}](),
	)

	auditTrail := []map[string]any{}
	toolExecuted := false

	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are helpful."),
		agent.WithTools(auditTool),
		agent.WithAPIKey(testAPIKey),
		agent.WithAfterToolCall(func(ctx context.Context, actx agent.AfterToolCallContext) (*agent.AfterToolCallResult, error) {
			auditEntry := map[string]any{
				"tool":      actx.ToolCall.Name,
				"args":      actx.Args,
				"result":    actx.Result.Details,
				"audited":   true,
				"auditedAt": time.Now().Unix(),
			}
			auditTrail = append(auditTrail, auditEntry)
			
			t.Logf("Audit entry added: %v", auditEntry)
			
			return &agent.AfterToolCallResult{
				Details: map[string]any{
					"original": actx.Result.Details,
					"audit":    auditEntry,
				},
			}, nil
		}),
	)

	events := a.Subscribe()

	go func() {
		for event := range events.Events() {
			if event.Type == agent.EventToolExecutionEnd {
				toolExecuted = true
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Use the audit tool"}); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	if err := a.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	t.Logf("Audit trail length: %d", len(auditTrail))
	t.Logf("Tool executed: %v", toolExecuted)

	if !toolExecuted {
		t.Fatal("Expected tool to be executed")
	}
	if len(auditTrail) < 1 {
		t.Fatal("Expected audit entry to be created when tool is executed")
	}
	t.Logf("First audit entry: %v", auditTrail[0])
	if audited, ok := auditTrail[0]["audited"].(bool); !ok || !audited {
		t.Error("Expected audit entry to be marked as audited")
	}
}
