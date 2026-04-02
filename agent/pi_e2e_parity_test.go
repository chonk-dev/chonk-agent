package agent_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	agent "github.com/chonk-dev/chonk-agent/agent"
	chonkai "github.com/chonk-dev/chonk-ai"
)

func TestE2EBasicPromptParity(t *testing.T) {
	model := testModel()
	a := agent.NewAgent(
		agent.WithSystemPrompt("You are a helpful assistant. Keep your responses concise."),
		agent.WithModel(model),
		agent.WithThinkingLevel(agent.ThinkingOff),
		agent.WithTools(),
		agent.WithStreamFn(streamFnFromMessages([]chonkai.AssistantMessage{
			assistantText("4", chonkai.StopReasonStop),
		}, false)),
	)

	if err := a.Prompt(context.Background(), userMessage("What is 2+2? Answer with just the number.")); err != nil {
		t.Fatalf("prompt failed: %v", err)
	}

	if a.State.IsStreaming {
		t.Fatal("expected IsStreaming false")
	}
	if len(a.State.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(a.State.Messages))
	}
	if _, ok := a.State.Messages[0].(chonkai.UserMessage); !ok {
		t.Fatal("expected first message to be user")
	}
	assistantMsg, ok := a.State.Messages[1].(chonkai.AssistantMessage)
	if !ok {
		t.Fatal("expected assistant message")
	}
	if !containsText(assistantMsg, "4") {
		t.Fatal("expected assistant message to contain 4")
	}
}

func TestE2EToolExecutionParity(t *testing.T) {
	model := testModel()
	tool := calculateTool()

	callIndex := 0
	streamFn := func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		stream := chonkai.NewEventStream(8)
		go func() {
			if callIndex == 0 {
				msg := assistantMessage([]chonkai.ContentBlock{
					chonkai.TextContent{Type: "text", Text: "Let me calculate that."},
					toolCallBlock("calc-1", "calculate", map[string]any{"expression": "123 * 456"}),
				}, chonkai.StopReasonToolUse)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			} else {
				msg := assistantText("The result is 56088.", chonkai.StopReasonStop)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			}
			callIndex++
		}()
		return stream
	}

	a := agent.NewAgent(
		agent.WithSystemPrompt("You are a helpful assistant. Always use the calculator tool for math."),
		agent.WithModel(model),
		agent.WithThinkingLevel(agent.ThinkingOff),
		agent.WithTools(tool),
		agent.WithStreamFn(streamFn),
	)

	pendingSnapshots := make([]struct {
		typ string
		ids []string
	}, 0)
	a.SubscribeFunc(func(event agent.AgentEvent, _ context.Context) {
		if event.Type == agent.EventToolExecutionStart || event.Type == agent.EventToolExecutionEnd {
			ids := make([]string, 0, len(a.State.InProgressToolIDs))
			for id := range a.State.InProgressToolIDs {
				ids = append(ids, id)
			}
			pendingSnapshots = append(pendingSnapshots, struct {
				typ string
				ids []string
			}{typ: string(event.Type), ids: ids})
		}
	})

	if err := a.Prompt(context.Background(), userMessage("Calculate 123 * 456 using the calculator tool.")); err != nil {
		t.Fatalf("prompt failed: %v", err)
	}

	if len(a.State.Messages) < 4 {
		t.Fatalf("expected at least 4 messages, got %d", len(a.State.Messages))
	}
	var toolResult chonkai.ToolResultMessage
	foundTool := false
	for _, msg := range a.State.Messages {
		if tr, ok := msg.(chonkai.ToolResultMessage); ok {
			toolResult = tr
			foundTool = true
			break
		}
	}
	if !foundTool {
		t.Fatal("expected tool result message")
	}
	if !containsToolText(toolResult, "123 * 456 = 56088") {
		t.Fatal("unexpected tool result content")
	}

	lastMsg, ok := a.State.Messages[len(a.State.Messages)-1].(chonkai.AssistantMessage)
	if !ok {
		t.Fatal("expected final assistant message")
	}
	if !containsText(lastMsg, "56088") {
		t.Fatal("expected final assistant message to contain 56088")
	}
	if len(a.State.InProgressToolIDs) != 0 {
		t.Fatalf("expected no pending tool calls, got %d", len(a.State.InProgressToolIDs))
	}

	if len(pendingSnapshots) != 2 || len(pendingSnapshots[0].ids) != 1 || len(pendingSnapshots[1].ids) != 0 {
		t.Fatalf("unexpected pending snapshots: %+v", pendingSnapshots)
	}
}

func TestE2EAbortParity(t *testing.T) {
	model := testModel()
	a := agent.NewAgent(
		agent.WithSystemPrompt("You are a helpful assistant."),
		agent.WithModel(model),
		agent.WithThinkingLevel(agent.ThinkingOff),
		agent.WithStreamFn(func(ctx context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
			stream := chonkai.NewEventStream(8)
			go func() {
				partial := assistantText("", chonkai.StopReasonStop)
				stream.Push(chonkai.Event{Type: chonkai.EventStart, Partial: &partial})
				<-ctx.Done()
				msg := assistantText("Aborted", chonkai.StopReasonAborted)
				msg.ErrorMsg = "aborted"
				stream.Push(chonkai.Event{Type: chonkai.EventError, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			}()
			return stream
		}),
	)

	done := make(chan error, 1)
	go func() {
		done <- a.Prompt(context.Background(), userMessage("Count slowly from 1 to 20."))
	}()
	time.Sleep(30 * time.Millisecond)
	a.Abort()
	if err := <-done; err != nil {
		t.Fatalf("prompt failed: %v", err)
	}

	if a.State.IsStreaming {
		t.Fatal("expected IsStreaming false after abort")
	}
	if len(a.State.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(a.State.Messages))
	}

	lastMsg, ok := a.State.Messages[len(a.State.Messages)-1].(chonkai.AssistantMessage)
	if !ok {
		t.Fatal("expected last message to be assistant")
	}
	if lastMsg.StopReason != chonkai.StopReasonAborted {
		t.Fatalf("expected stopReason aborted, got %s", lastMsg.StopReason)
	}
	if lastMsg.ErrorMsg == "" {
		t.Fatal("expected error message on abort")
	}
	if a.State.ErrorMessage != lastMsg.ErrorMsg {
		t.Fatalf("expected state error message to match, got %q", a.State.ErrorMessage)
	}
}

func TestE2EStateUpdatesParity(t *testing.T) {
	model := testModel()
	a := agent.NewAgent(
		agent.WithSystemPrompt("You are a helpful assistant."),
		agent.WithModel(model),
		agent.WithThinkingLevel(agent.ThinkingOff),
		agent.WithStreamFn(streamFnFromMessages([]chonkai.AssistantMessage{
			assistantText("1 2 3 4 5", chonkai.StopReasonStop),
		}, true)),
	)

	events := make([]agent.AgentEventType, 0)
	a.SubscribeFunc(func(event agent.AgentEvent, _ context.Context) {
		events = append(events, event.Type)
	})

	if err := a.Prompt(context.Background(), userMessage("Count from 1 to 5.")); err != nil {
		t.Fatalf("prompt failed: %v", err)
	}

	expectTypes := []agent.AgentEventType{
		agent.EventAgentStart,
		agent.EventTurnStart,
		agent.EventMessageStart,
		agent.EventMessageUpdate,
		agent.EventMessageEnd,
		agent.EventTurnEnd,
		agent.EventAgentEnd,
	}
	for _, typ := range expectTypes {
		if !containsEvent(events, typ) {
			t.Fatalf("missing event type: %s", typ)
		}
	}

	indexStart := indexEvent(events, agent.EventAgentStart)
	indexMessageStart := indexEvent(events, agent.EventMessageStart)
	indexMessageEnd := indexEvent(events, agent.EventMessageEnd)
	indexAgentEnd := lastIndexEvent(events, agent.EventAgentEnd)
	if !(indexStart < indexMessageStart && indexMessageStart < indexMessageEnd && indexMessageEnd < indexAgentEnd) {
		t.Fatalf("unexpected event ordering: %v", events)
	}

	if a.State.IsStreaming {
		t.Fatal("expected IsStreaming false")
	}
	if len(a.State.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(a.State.Messages))
	}
}

func TestE2EMultiTurnConversationParity(t *testing.T) {
	model := testModel()
	callIndex := 0
	a := agent.NewAgent(
		agent.WithSystemPrompt("You are a helpful assistant."),
		agent.WithModel(model),
		agent.WithThinkingLevel(agent.ThinkingOff),
		agent.WithStreamFn(func(_ context.Context, _ *chonkai.Model, ctx *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
			stream := chonkai.NewEventStream(8)
			go func() {
				if callIndex == 0 {
					msg := assistantText("Nice to meet you, Alice.", chonkai.StopReasonStop)
					stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
					stream.Close(msg, nil)
				} else {
					hasAlice := false
					for _, m := range ctx.Messages {
						if um, ok := m.(chonkai.UserMessage); ok {
							if strings.Contains(um.RawText, "Alice") {
								hasAlice = true
							}
						}
					}
					msg := assistantText("I do not know your name.", chonkai.StopReasonStop)
					if hasAlice {
						msg = assistantText("Your name is Alice.", chonkai.StopReasonStop)
					}
					stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
					stream.Close(msg, nil)
				}
				callIndex++
			}()
			return stream
		}),
	)

	if err := a.Prompt(context.Background(), userMessage("My name is Alice.")); err != nil {
		t.Fatalf("prompt failed: %v", err)
	}
	if len(a.State.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(a.State.Messages))
	}

	if err := a.Prompt(context.Background(), userMessage("What is my name?")); err != nil {
		t.Fatalf("prompt failed: %v", err)
	}
	if len(a.State.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(a.State.Messages))
	}

	lastMsg, ok := a.State.Messages[3].(chonkai.AssistantMessage)
	if !ok {
		t.Fatal("expected assistant message")
	}
	if !containsText(lastMsg, "Alice") {
		t.Fatal("expected assistant message to mention Alice")
	}
}

func TestE2EPreserveThinkingBlocksParity(t *testing.T) {
	model := testModel()
	model.Reasoning = true

	a := agent.NewAgent(
		agent.WithSystemPrompt("You are a helpful assistant."),
		agent.WithModel(model),
		agent.WithThinkingLevel(chonkai.ThinkingLow),
		agent.WithStreamFn(streamFnFromMessages([]chonkai.AssistantMessage{
			assistantMessage([]chonkai.ContentBlock{
				chonkai.ThinkingContent{Type: "thinking", Thinking: "step by step"},
				chonkai.TextContent{Type: "text", Text: "4"},
			}, chonkai.StopReasonStop),
		}, false)),
	)

	if err := a.Prompt(context.Background(), userMessage("What is 2+2?")); err != nil {
		t.Fatalf("prompt failed: %v", err)
	}

	assistantMsg, ok := a.State.Messages[1].(chonkai.AssistantMessage)
	if !ok {
		t.Fatal("expected assistant message")
	}
	if len(assistantMsg.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(assistantMsg.Content))
	}
	if _, ok := assistantMsg.Content[0].(chonkai.ThinkingContent); !ok {
		t.Fatal("expected first block to be thinking content")
	}
	if _, ok := assistantMsg.Content[1].(chonkai.TextContent); !ok {
		t.Fatal("expected second block to be text content")
	}
}

func TestContinueValidationParity(t *testing.T) {
	model := testModel()
	a := agent.NewAgent(
		agent.WithSystemPrompt("Test"),
		agent.WithModel(model),
	)

	if err := a.Continue(context.Background()); err == nil || !errors.Is(err, agent.ErrNoMessages) {
		t.Fatalf("expected ErrNoMessages, got %v", err)
	}

	assistantMsg := assistantText("Hello", chonkai.StopReasonStop)
	a.State.Messages = []agent.AgentMessage{assistantMsg}
	if err := a.Continue(context.Background()); err == nil || !errors.Is(err, agent.ErrCannotContinueFromAssistant) {
		t.Fatalf("expected ErrCannotContinueFromAssistant, got %v", err)
	}
}

func TestContinueFromUserParity(t *testing.T) {
	model := testModel()
	a := agent.NewAgent(
		agent.WithSystemPrompt("You are a helpful assistant. Follow instructions exactly."),
		agent.WithModel(model),
		agent.WithThinkingLevel(agent.ThinkingOff),
		agent.WithStreamFn(streamFnFromMessages([]chonkai.AssistantMessage{
			assistantText("HELLO WORLD", chonkai.StopReasonStop),
		}, false)),
	)

	a.State.Messages = []agent.AgentMessage{
		chonkai.UserMessage{
			Content:   []chonkai.UserContent{chonkai.TextContent{Type: "text", Text: "Say exactly: HELLO WORLD"}},
			Timestamp: time.Now(),
		},
	}

	if err := a.Continue(context.Background()); err != nil {
		t.Fatalf("continue failed: %v", err)
	}
	if len(a.State.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(a.State.Messages))
	}
	if _, ok := a.State.Messages[1].(chonkai.AssistantMessage); !ok {
		t.Fatal("expected assistant message")
	}
}

func TestContinueFromToolResultParity(t *testing.T) {
	model := testModel()
	tool := calculateTool()
	a := agent.NewAgent(
		agent.WithSystemPrompt("You are a helpful assistant. After getting a calculation result, state the answer clearly."),
		agent.WithModel(model),
		agent.WithThinkingLevel(agent.ThinkingOff),
		agent.WithTools(tool),
		agent.WithStreamFn(streamFnFromMessages([]chonkai.AssistantMessage{
			assistantText("The answer is 8.", chonkai.StopReasonStop),
		}, false)),
	)

	userMsg := chonkai.UserMessage{
		Content:   []chonkai.UserContent{chonkai.TextContent{Type: "text", Text: "What is 5 + 3?"}},
		Timestamp: time.Now(),
	}
	assistantMsg := assistantMessage([]chonkai.ContentBlock{
		chonkai.TextContent{Type: "text", Text: "Let me calculate that."},
		toolCallBlock("calc-1", "calculate", map[string]any{"expression": "5 + 3"}),
	}, chonkai.StopReasonToolUse)
	toolResult := chonkai.ToolResultMessage{
		ToolCallID: "calc-1",
		ToolName:   "calculate",
		Content:    []chonkai.UserContent{chonkai.TextContent{Type: "text", Text: "5 + 3 = 8"}},
		IsError:    false,
		Timestamp:  time.Now(),
	}

	a.State.Messages = []agent.AgentMessage{userMsg, assistantMsg, toolResult}

	if err := a.Continue(context.Background()); err != nil {
		t.Fatalf("continue failed: %v", err)
	}

	lastMsg, ok := a.State.Messages[len(a.State.Messages)-1].(chonkai.AssistantMessage)
	if !ok {
		t.Fatal("expected assistant message")
	}
	if !containsText(lastMsg, "8") {
		t.Fatal("expected final assistant message to contain 8")
	}
}

func calculateTool() agent.Tool {
	return agent.NewTool(
		"calculate",
		"Evaluate mathematical expressions",
		func(_ context.Context, _ string, params map[string]any, _ agent.ToolUpdate) (agent.ToolResult, error) {
			expression, _ := params["expression"].(string)
			result, err := calculateExpression(expression)
			if err != nil {
				return agent.ToolResult{}, err
			}
			return agent.ToolResult{
				Content: []chonkai.UserContent{
					chonkai.TextContent{Type: "text", Text: expression + " = " + result},
				},
			}, nil
		},
	)
}

func calculateExpression(expression string) (string, error) {
	fields := strings.Fields(expression)
	if len(fields) != 3 {
		return "", errors.New("invalid expression")
	}
	left, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", err
	}
	right, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return "", err
	}
	var result float64
	switch fields[1] {
	case "+":
		result = left + right
	case "-":
		result = left - right
	case "*":
		result = left * right
	case "/":
		result = left / right
	default:
		return "", errors.New("invalid operator")
	}
	if result == float64(int64(result)) {
		return strconv.FormatInt(int64(result), 10), nil
	}
	return strconv.FormatFloat(result, 'f', -1, 64), nil
}

func containsText(msg chonkai.AssistantMessage, needle string) bool {
	for _, block := range msg.Content {
		if text, ok := block.(chonkai.TextContent); ok {
			if strings.Contains(text.Text, needle) {
				return true
			}
		}
	}
	return false
}

func containsToolText(msg chonkai.ToolResultMessage, needle string) bool {
	for _, block := range msg.Content {
		if text, ok := block.(chonkai.TextContent); ok {
			if strings.Contains(text.Text, needle) {
				return true
			}
		}
	}
	return false
}

func containsEvent(events []agent.AgentEventType, target agent.AgentEventType) bool {
	for _, e := range events {
		if e == target {
			return true
		}
	}
	return false
}

func indexEvent(events []agent.AgentEventType, target agent.AgentEventType) int {
	for i, e := range events {
		if e == target {
			return i
		}
	}
	return -1
}

func lastIndexEvent(events []agent.AgentEventType, target agent.AgentEventType) int {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] == target {
			return i
		}
	}
	return -1
}
