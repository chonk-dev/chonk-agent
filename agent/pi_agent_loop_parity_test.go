package agent_test

import (
	"context"
	"testing"
	"time"

	agent "github.com/chonk-dev/chonk-agent/agent"
	chonkai "github.com/chonk-dev/chonk-ai"
)

type notificationMessage struct {
	Text string
}

type customMessage struct {
	Text string
}



func TestAgentLoopEmitsEvents(t *testing.T) {
	agentContext := agent.AgentContext{
		SystemPrompt: "You are helpful.",
		Messages:     []agent.AgentMessage{},
		Tools:        nil,
	}

	userPrompt := userMessage("Hello")

	config := agent.LoopConfig{
		Model:        testModel(),
		ConvertToLlm: func(messages []agent.AgentMessage) []chonkai.Message { return convertToLLM(messages) },
	}

	streamFn := func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		return streamWithMessage(assistantText("Hi there!", chonkai.StopReasonStop), false)
	}
	config.StreamFn = streamFn

	events := make([]agent.AgentEvent, 0)
	stream := agent.RunAgentLoop(context.Background(), []agent.AgentMessage{userPrompt}, agentContext, config)

	for event := range stream.Events() {
		events = append(events, event)
	}

	messages := stream.Result()
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if _, ok := messages[0].(chonkai.UserMessage); !ok {
		t.Fatal("expected first message to be user")
	}
	if _, ok := messages[1].(chonkai.AssistantMessage); !ok {
		t.Fatal("expected second message to be assistant")
	}

	eventTypes := make(map[agent.AgentEventType]struct{})
	for _, e := range events {
		eventTypes[e.Type] = struct{}{}
	}
	required := []agent.AgentEventType{
		agent.EventAgentStart,
		agent.EventTurnStart,
		agent.EventMessageStart,
		agent.EventMessageEnd,
		agent.EventTurnEnd,
		agent.EventAgentEnd,
	}
	for _, typ := range required {
		if _, ok := eventTypes[typ]; !ok {
			t.Fatalf("missing event type %s", typ)
		}
	}
}

func TestAgentLoopCustomMessageConvertToLLM(t *testing.T) {
	notification := notificationMessage{Text: "notice"}

	agentContext := agent.AgentContext{
		SystemPrompt: "You are helpful.",
		Messages:     []agent.AgentMessage{notification},
		Tools:        nil,
	}

	userPrompt := userMessage("Hello")

	var converted []chonkai.Message
	config := agent.LoopConfig{
		Model: testModel(),
		ConvertToLlm: func(messages []agent.AgentMessage) []chonkai.Message {
			converted = nil
			for _, m := range messages {
				switch msg := m.(type) {
				case chonkai.UserMessage:
					converted = append(converted, msg)
				case chonkai.AssistantMessage:
					converted = append(converted, msg)
				case chonkai.ToolResultMessage:
					converted = append(converted, msg)
				default:
					_ = msg
				}
			}
			return converted
		},
	}

	config.StreamFn = func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		return streamWithMessage(assistantText("Response", chonkai.StopReasonStop), false)
	}

	stream := agent.RunAgentLoop(context.Background(), []agent.AgentMessage{userPrompt}, agentContext, config)
	for range stream.Events() {
	}

	if len(converted) != 1 {
		t.Fatalf("expected 1 converted message, got %d", len(converted))
	}
	if _, ok := converted[0].(chonkai.UserMessage); !ok {
		t.Fatal("expected converted message to be user")
	}
}

func TestAgentLoopTransformContextOrder(t *testing.T) {
	agentContext := agent.AgentContext{
		SystemPrompt: "You are helpful.",
		Messages: []agent.AgentMessage{
			userMessage("old 1"),
			assistantText("old r1", chonkai.StopReasonStop),
			userMessage("old 2"),
			assistantText("old r2", chonkai.StopReasonStop),
		},
	}

	userPrompt := userMessage("new message")

	var transformed []agent.AgentMessage
	var converted []chonkai.Message

	config := agent.LoopConfig{
		Model: testModel(),
		TransformContext: func(_ context.Context, messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
			transformed = messages[len(messages)-2:]
			return transformed, nil
		},
		ConvertToLlm: func(messages []agent.AgentMessage) []chonkai.Message {
			converted = convertToLLM(messages)
			return converted
		},
	}

	config.StreamFn = func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		return streamWithMessage(assistantText("Response", chonkai.StopReasonStop), false)
	}

	stream := agent.RunAgentLoop(context.Background(), []agent.AgentMessage{userPrompt}, agentContext, config)
	for range stream.Events() {
	}

	if len(transformed) != 2 {
		t.Fatalf("expected 2 transformed messages, got %d", len(transformed))
	}
	if len(converted) != 2 {
		t.Fatalf("expected 2 converted messages, got %d", len(converted))
	}
}

func TestAgentLoopToolCalls(t *testing.T) {
	executed := make([]string, 0)
	echoTool := agent.NewTool(
		"echo",
		"Echo tool",
		func(_ context.Context, _ string, params map[string]any, _ agent.ToolUpdate) (agent.ToolResult, error) {
			val, _ := params["value"].(string)
			executed = append(executed, val)
			return agent.ToolResult{
				Content: []chonkai.UserContent{chonkai.TextContent{Type: "text", Text: "echoed: " + val}},
				Details: map[string]any{"value": val},
			}, nil
		},
	)

	agentContext := agent.AgentContext{
		SystemPrompt: "",
		Messages:     []agent.AgentMessage{},
		Tools:        []agent.Tool{echoTool},
	}

	userPrompt := userMessage("echo something")

	config := agent.LoopConfig{
		Model:        testModel(),
		ConvertToLlm: convertToLLM,
	}

	callIndex := 0
	config.StreamFn = func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		stream := chonkai.NewEventStream(8)
		go func() {
			if callIndex == 0 {
				msg := assistantMessage([]chonkai.ContentBlock{
					toolCallBlock("tool-1", "echo", map[string]any{"value": "hello"}),
				}, chonkai.StopReasonToolUse)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			} else {
				msg := assistantText("done", chonkai.StopReasonStop)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			}
			callIndex++
		}()
		return stream
	}

	events := make([]agent.AgentEvent, 0)
	stream := agent.RunAgentLoop(context.Background(), []agent.AgentMessage{userPrompt}, agentContext, config)
	for event := range stream.Events() {
		events = append(events, event)
	}

	if len(executed) != 1 || executed[0] != "hello" {
		t.Fatalf("unexpected tool execution: %v", executed)
	}

	var sawStart, sawEnd bool
	for _, e := range events {
		if e.Type == agent.EventToolExecutionStart {
			sawStart = true
		}
		if e.Type == agent.EventToolExecutionEnd {
			sawEnd = true
			if e.ToolIsError {
				t.Fatal("unexpected tool error")
			}
		}
	}
	if !sawStart || !sawEnd {
		t.Fatalf("expected tool execution events, start=%v end=%v", sawStart, sawEnd)
	}
}

func TestAgentLoopMutatedBeforeToolArgs(t *testing.T) {
	executed := make([]any, 0)
	echoTool := agent.NewTool(
		"echo",
		"Echo tool",
		func(_ context.Context, _ string, params map[string]any, _ agent.ToolUpdate) (agent.ToolResult, error) {
			executed = append(executed, params["value"])
			return agent.ToolResult{
				Content: []chonkai.UserContent{chonkai.TextContent{Type: "text", Text: "ok"}},
			}, nil
		},
	)

	agentContext := agent.AgentContext{
		SystemPrompt: "",
		Messages:     nil,
		Tools:        []agent.Tool{echoTool},
	}

	config := agent.LoopConfig{
		Model:        testModel(),
		ConvertToLlm: convertToLLM,
		BeforeToolCall: func(_ context.Context, ctx agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
			if ctx.Args != nil {
				ctx.Args["value"] = 123
			}
			return nil, nil
		},
	}

	callIndex := 0
	config.StreamFn = func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		stream := chonkai.NewEventStream(8)
		go func() {
			if callIndex == 0 {
				msg := assistantMessage([]chonkai.ContentBlock{
					toolCallBlock("tool-1", "echo", map[string]any{"value": "hello"}),
				}, chonkai.StopReasonToolUse)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			} else {
				msg := assistantText("done", chonkai.StopReasonStop)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			}
			callIndex++
		}()
		return stream
	}

	stream := agent.RunAgentLoop(context.Background(), []agent.AgentMessage{userMessage("echo")}, agentContext, config)
	for range stream.Events() {
	}

	if len(executed) != 1 || executed[0] != 123 {
		t.Fatalf("expected mutated args to be used, got %v", executed)
	}
}

func TestAgentLoopPrepareArguments(t *testing.T) {
	executed := make([][]map[string]string, 0)
	editTool := agent.NewTool(
		"edit",
		"Edit tool",
		func(_ context.Context, _ string, params map[string]any, _ agent.ToolUpdate) (agent.ToolResult, error) {
			raw, _ := params["edits"].([]any)
			edits := make([]map[string]string, 0, len(raw))
			for _, item := range raw {
				entry, _ := item.(map[string]any)
				edits = append(edits, map[string]string{
					"oldText": entry["oldText"].(string),
					"newText": entry["newText"].(string),
				})
			}
			executed = append(executed, edits)
			return agent.ToolResult{
				Content: []chonkai.UserContent{chonkai.TextContent{Type: "text", Text: "ok"}},
			}, nil
		},
		agent.WithPrepare(func(raw map[string]any) (map[string]any, error) {
			oldText, _ := raw["oldText"].(string)
			newText, _ := raw["newText"].(string)
			if oldText == "" || newText == "" {
				return raw, nil
			}
			return map[string]any{
				"edits": []map[string]any{{"oldText": oldText, "newText": newText}},
			}, nil
		}),
	)

	agentContext := agent.AgentContext{
		SystemPrompt: "",
		Messages:     nil,
		Tools:        []agent.Tool{editTool},
	}

	config := agent.LoopConfig{
		Model:        testModel(),
		ConvertToLlm: convertToLLM,
	}

	callIndex := 0
	config.StreamFn = func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		stream := chonkai.NewEventStream(8)
		go func() {
			if callIndex == 0 {
				msg := assistantMessage([]chonkai.ContentBlock{
					toolCallBlock("tool-1", "edit", map[string]any{"oldText": "before", "newText": "after"}),
				}, chonkai.StopReasonToolUse)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			} else {
				msg := assistantText("done", chonkai.StopReasonStop)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			}
			callIndex++
		}()
		return stream
	}

	stream := agent.RunAgentLoop(context.Background(), []agent.AgentMessage{userMessage("edit")}, agentContext, config)
	for range stream.Events() {
	}

	if len(executed) != 1 || len(executed[0]) != 1 {
		t.Fatalf("unexpected edits: %v", executed)
	}
	if executed[0][0]["oldText"] != "before" || executed[0][0]["newText"] != "after" {
		t.Fatalf("unexpected edits: %v", executed)
	}
}

func TestAgentLoopParallelOrder(t *testing.T) {
	firstDone := make(chan struct{})
	firstResolved := false
	parallelObserved := false

	echoTool := agent.NewTool(
		"echo",
		"Echo tool",
		func(_ context.Context, _ string, params map[string]any, _ agent.ToolUpdate) (agent.ToolResult, error) {
			value := params["value"].(string)
			if value == "first" {
				<-firstDone
				firstResolved = true
			}
			if value == "second" && !firstResolved {
				parallelObserved = true
			}
			return agent.ToolResult{
				Content: []chonkai.UserContent{chonkai.TextContent{Type: "text", Text: "echoed: " + value}},
				Details: map[string]any{"value": value},
			}, nil
		},
	)

	agentContext := agent.AgentContext{
		SystemPrompt: "",
		Messages:     nil,
		Tools:        []agent.Tool{echoTool},
	}

	config := agent.LoopConfig{
		Model:         testModel(),
		ConvertToLlm:  convertToLLM,
		ToolExecution: agent.ToolExecutionParallel,
	}

	callIndex := 0
	config.StreamFn = func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		stream := chonkai.NewEventStream(8)
		go func() {
			if callIndex == 0 {
				msg := assistantMessage([]chonkai.ContentBlock{
					toolCallBlock("tool-1", "echo", map[string]any{"value": "first"}),
					toolCallBlock("tool-2", "echo", map[string]any{"value": "second"}),
				}, chonkai.StopReasonToolUse)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
				go func() {
					time.Sleep(20 * time.Millisecond)
					close(firstDone)
				}()
			} else {
				msg := assistantText("done", chonkai.StopReasonStop)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			}
			callIndex++
		}()
		return stream
	}

	events := make([]agent.AgentEvent, 0)
	stream := agent.RunAgentLoop(context.Background(), []agent.AgentMessage{userMessage("echo both")}, agentContext, config)
	for event := range stream.Events() {
		events = append(events, event)
	}

	toolResultIDs := make([]string, 0)
	for _, e := range events {
		if e.Type != agent.EventMessageEnd {
			continue
		}
		if msg, ok := e.Message.(chonkai.ToolResultMessage); ok {
			toolResultIDs = append(toolResultIDs, msg.ToolCallID)
		}
	}

	if !parallelObserved {
		t.Fatal("expected parallel execution to be observed")
	}
	if len(toolResultIDs) != 2 || toolResultIDs[0] != "tool-1" || toolResultIDs[1] != "tool-2" {
		t.Fatalf("unexpected tool result order: %v", toolResultIDs)
	}
}

func TestAgentLoopQueuedMessagesAfterTools(t *testing.T) {
	executed := make([]string, 0)
	echoTool := agent.NewTool(
		"echo",
		"Echo tool",
		func(_ context.Context, _ string, params map[string]any, _ agent.ToolUpdate) (agent.ToolResult, error) {
			value := params["value"].(string)
			executed = append(executed, value)
			return agent.ToolResult{
				Content: []chonkai.UserContent{chonkai.TextContent{Type: "text", Text: "ok:" + value}},
				Details: map[string]any{"value": value},
			}, nil
		},
	)

	agentContext := agent.AgentContext{
		SystemPrompt: "",
		Messages:     nil,
		Tools:        []agent.Tool{echoTool},
	}

	queued := userMessage("interrupt")
	queuedDelivered := false
	sawInterrupt := false
	callIndex := 0

	config := agent.LoopConfig{
		Model:         testModel(),
		ConvertToLlm:  convertToLLM,
		ToolExecution: agent.ToolExecutionSequential,
		GetSteering: func() ([]agent.AgentMessage, error) {
			if len(executed) >= 1 && !queuedDelivered {
				queuedDelivered = true
				return []agent.AgentMessage{queued}, nil
			}
			return nil, nil
		},
	}

	config.StreamFn = func(_ context.Context, _ *chonkai.Model, ctx *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		if callIndex == 1 {
			for _, msg := range ctx.Messages {
				if um, ok := msg.(chonkai.UserMessage); ok && um.RawText == "interrupt" {
					sawInterrupt = true
				}
			}
		}
		stream := chonkai.NewEventStream(8)
		go func() {
			if callIndex == 0 {
				msg := assistantMessage([]chonkai.ContentBlock{
					toolCallBlock("tool-1", "echo", map[string]any{"value": "first"}),
					toolCallBlock("tool-2", "echo", map[string]any{"value": "second"}),
				}, chonkai.StopReasonToolUse)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			} else {
				msg := assistantText("done", chonkai.StopReasonStop)
				stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
				stream.Close(msg, nil)
			}
			callIndex++
		}()
		return stream
	}

	events := make([]agent.AgentEvent, 0)
	stream := agent.RunAgentLoop(context.Background(), []agent.AgentMessage{userMessage("start")}, agentContext, config)
	for event := range stream.Events() {
		events = append(events, event)
	}

	if len(executed) != 2 || executed[0] != "first" || executed[1] != "second" {
		t.Fatalf("unexpected tool execution order: %v", executed)
	}

	eventSequence := make([]string, 0)
	for _, e := range events {
		if e.Type != agent.EventMessageStart {
			continue
		}
		switch msg := e.Message.(type) {
		case chonkai.ToolResultMessage:
			eventSequence = append(eventSequence, "tool:"+msg.ToolCallID)
		case chonkai.UserMessage:
			eventSequence = append(eventSequence, msg.RawText)
		}
	}

	indexTool1 := indexOf(eventSequence, "tool:tool-1")
	indexTool2 := indexOf(eventSequence, "tool:tool-2")
	indexInterrupt := indexOf(eventSequence, "interrupt")
	if indexInterrupt == -1 || indexTool1 == -1 || indexTool2 == -1 {
		t.Fatalf("unexpected event sequence: %v", eventSequence)
	}
	if indexTool1 > indexInterrupt || indexTool2 > indexInterrupt {
		t.Fatalf("interrupt injected before tools: %v", eventSequence)
	}
	if !sawInterrupt {
		t.Fatal("interrupt not seen in second call context")
	}
}

func TestAgentLoopContinueValidation(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty context")
		}
	}()
	agent.RunAgentLoopContinue(context.Background(), agent.AgentContext{SystemPrompt: "test"}, agent.LoopConfig{
		Model:        testModel(),
		ConvertToLlm: convertToLLM,
	})
}

func TestAgentLoopContinueAssistantTailPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on assistant tail")
		}
	}()
	agent.RunAgentLoopContinue(context.Background(), agent.AgentContext{
		SystemPrompt: "test",
		Messages:     []agent.AgentMessage{assistantText("hi", chonkai.StopReasonStop)},
	}, agent.LoopConfig{
		Model:        testModel(),
		ConvertToLlm: convertToLLM,
	})
}

func TestAgentLoopContinueNoUserEvents(t *testing.T) {
	agentContext := agent.AgentContext{
		SystemPrompt: "You are helpful.",
		Messages:     []agent.AgentMessage{userMessage("Hello")},
		Tools:        nil,
	}

	config := agent.LoopConfig{
		Model:        testModel(),
		ConvertToLlm: convertToLLM,
	}

	config.StreamFn = func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		return streamWithMessage(assistantText("Response", chonkai.StopReasonStop), false)
	}

	stream := agent.RunAgentLoopContinue(context.Background(), agentContext, config)

	events := make([]agent.AgentEvent, 0)
	for event := range stream.Events() {
		events = append(events, event)
	}

	messages := stream.Result()
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if _, ok := messages[0].(chonkai.AssistantMessage); !ok {
		t.Fatal("expected assistant message")
	}

	messageEndEvents := make([]agent.AgentEvent, 0)
	for _, e := range events {
		if e.Type == agent.EventMessageEnd {
			messageEndEvents = append(messageEndEvents, e)
		}
	}
	if len(messageEndEvents) != 1 {
		t.Fatalf("expected 1 message_end event, got %d", len(messageEndEvents))
	}
	if _, ok := messageEndEvents[0].Message.(chonkai.AssistantMessage); !ok {
		t.Fatal("expected message_end to be assistant")
	}
}

func TestAgentLoopContinueCustomTail(t *testing.T) {
	agentContext := agent.AgentContext{
		SystemPrompt: "You are helpful.",
		Messages:     []agent.AgentMessage{customMessage{Text: "Hook content"}},
		Tools:        nil,
	}

	config := agent.LoopConfig{
		Model: testModel(),
		ConvertToLlm: func(messages []agent.AgentMessage) []chonkai.Message {
			out := make([]chonkai.Message, 0, len(messages))
			for _, m := range messages {
				switch msg := m.(type) {
				case customMessage:
					out = append(out, chonkai.UserMessage{RawText: msg.Text, Timestamp: time.Now()})
				case chonkai.UserMessage:
					out = append(out, msg)
				case chonkai.AssistantMessage:
					out = append(out, msg)
				case chonkai.ToolResultMessage:
					out = append(out, msg)
				}
			}
			return out
		},
	}

	config.StreamFn = func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		return streamWithMessage(assistantText("Response", chonkai.StopReasonStop), false)
	}

	stream := agent.RunAgentLoopContinue(context.Background(), agentContext, config)
	for range stream.Events() {
	}
	messages := stream.Result()
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if _, ok := messages[0].(chonkai.AssistantMessage); !ok {
		t.Fatal("expected assistant message")
	}
}

func convertToLLM(messages []agent.AgentMessage) []chonkai.Message {
	out := make([]chonkai.Message, 0, len(messages))
	for _, m := range messages {
		switch msg := m.(type) {
		case chonkai.UserMessage:
			out = append(out, msg)
		case chonkai.AssistantMessage:
			out = append(out, msg)
		case chonkai.ToolResultMessage:
			out = append(out, msg)
		}
	}
	return out
}

func indexOf(list []string, target string) int {
	for i, item := range list {
		if item == target {
			return i
		}
	}
	return -1
}
