package agent_test

import (
	"context"
	"testing"
	"time"

	agent "github.com/chonk-dev/chonk-agent/agent"
	chonkai "github.com/chonk-dev/chonk-ai"
)

func TestAgentDefaultStateParity(t *testing.T) {
	a := agent.NewAgent()

	if a.State.SystemPrompt != "" {
		t.Fatalf("expected empty system prompt, got %q", a.State.SystemPrompt)
	}
	if a.State.Model == nil {
		t.Fatal("expected default model to be set")
	}
	if a.State.ThinkingLevel != agent.ThinkingOff {
		t.Fatalf("expected thinking level off, got %q", a.State.ThinkingLevel)
	}
	if len(a.State.Tools) != 0 {
		t.Fatalf("expected no tools, got %d", len(a.State.Tools))
	}
	if len(a.State.Messages) != 0 {
		t.Fatalf("expected no messages, got %d", len(a.State.Messages))
	}
	if a.State.IsStreaming {
		t.Fatal("expected IsStreaming false")
	}
	if a.State.StreamingMessage != nil {
		t.Fatal("expected nil StreamingMessage")
	}
	if len(a.State.InProgressToolIDs) != 0 {
		t.Fatalf("expected no pending tool calls, got %d", len(a.State.InProgressToolIDs))
	}
	if a.State.ErrorMessage != "" {
		t.Fatalf("expected empty error message, got %q", a.State.ErrorMessage)
	}
}

func TestAgentCustomInitialStateParity(t *testing.T) {
	model := testModel()
	a := agent.NewAgent(
		agent.WithSystemPrompt("You are a helpful assistant."),
		agent.WithModel(model),
		agent.WithThinkingLevel(chonkai.ThinkingLow),
	)

	if a.State.SystemPrompt != "You are a helpful assistant." {
		t.Fatalf("unexpected system prompt: %q", a.State.SystemPrompt)
	}
	if a.State.Model != model {
		t.Fatal("unexpected model")
	}
	if a.State.ThinkingLevel != chonkai.ThinkingLow {
		t.Fatalf("unexpected thinking level: %q", a.State.ThinkingLevel)
	}
}

func TestAgentSubscribeParity(t *testing.T) {
	a := agent.NewAgent()

	eventCount := 0
	unsubscribe := a.SubscribeFunc(func(_ agent.AgentEvent, _ context.Context) {
		eventCount++
	})

	if eventCount != 0 {
		t.Fatalf("expected 0 events on subscribe, got %d", eventCount)
	}

	a.State.SystemPrompt = "Test prompt"
	if eventCount != 0 {
		t.Fatalf("expected no events on state mutation, got %d", eventCount)
	}

	unsubscribe()
	a.State.SystemPrompt = "Another prompt"
	if eventCount != 0 {
		t.Fatalf("expected no events after unsubscribe, got %d", eventCount)
	}
}

func TestAgentAwaitSubscribersBeforePromptResolves(t *testing.T) {
	barrier := make(chan struct{})
	listenerDone := make(chan struct{})

	a := agent.NewAgent(
		agent.WithModel(testModel()),
		agent.WithStreamFn(func(ctx context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
			msg := assistantText("ok", chonkai.StopReasonStop)
			return streamWithMessage(msg, false)
		}),
	)

	a.SubscribeFunc(func(event agent.AgentEvent, _ context.Context) {
		if event.Type == agent.EventAgentEnd {
			<-barrier
			close(listenerDone)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	promptDone := make(chan error, 1)
	go func() {
		promptDone <- a.Prompt(ctx, userMessage("hello"))
	}()

	time.Sleep(10 * time.Millisecond)

	select {
	case <-promptDone:
		t.Fatal("prompt resolved before subscriber finished")
	default:
	}

	select {
	case <-listenerDone:
		t.Fatal("listener finished before barrier release")
	default:
	}

	if !a.State.IsStreaming {
		t.Fatal("expected IsStreaming true while waiting")
	}

	close(barrier)

	if err := <-promptDone; err != nil {
		t.Fatalf("prompt failed: %v", err)
	}

	select {
	case <-listenerDone:
	default:
		t.Fatal("listener did not finish")
	}

	if a.State.IsStreaming {
		t.Fatal("expected IsStreaming false after completion")
	}
}

func TestAgentWaitIdleAwaitsSubscribers(t *testing.T) {
	barrier := make(chan struct{})

	a := agent.NewAgent(
		agent.WithModel(testModel()),
		agent.WithStreamFn(func(ctx context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
			msg := assistantText("ok", chonkai.StopReasonStop)
			return streamWithMessage(msg, false)
		}),
	)

	a.SubscribeFunc(func(event agent.AgentEvent, _ context.Context) {
		if event.Type == agent.EventMessageEnd {
			if _, ok := event.Message.(chonkai.AssistantMessage); ok {
				<-barrier
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	promptDone := make(chan error, 1)
	go func() {
		promptDone <- a.Prompt(ctx, userMessage("hello"))
	}()

	for i := 0; i < 50; i++ {
		if a.State.IsStreaming {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	idleDone := make(chan error, 1)
	go func() {
		idleDone <- a.WaitIdle(ctx)
	}()

	time.Sleep(10 * time.Millisecond)

	select {
	case <-idleDone:
		t.Fatal("WaitIdle resolved before subscriber finished")
	default:
	}

	if !a.State.IsStreaming {
		t.Fatal("expected IsStreaming true while running")
	}

	close(barrier)

	if err := <-promptDone; err != nil {
		t.Fatalf("prompt failed: %v", err)
	}
	if err := <-idleDone; err != nil {
		t.Fatalf("WaitIdle failed: %v", err)
	}

	if a.State.IsStreaming {
		t.Fatal("expected IsStreaming false after completion")
	}
}

func TestAgentSubscribersReceiveAbortContext(t *testing.T) {
	var received context.Context

	a := agent.NewAgent(
		agent.WithModel(testModel()),
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

	a.SubscribeFunc(func(event agent.AgentEvent, ctx context.Context) {
		if event.Type == agent.EventAgentStart {
			received = ctx
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	promptDone := make(chan error, 1)
	go func() {
		promptDone <- a.Prompt(ctx, userMessage("hello"))
	}()

	time.Sleep(10 * time.Millisecond)
	if received == nil {
		t.Fatal("expected abort context to be received")
	}
	select {
	case <-received.Done():
		t.Fatal("context should not be aborted yet")
	default:
	}

	a.Abort()
	if err := <-promptDone; err != nil {
		t.Fatalf("prompt failed: %v", err)
	}

	select {
	case <-received.Done():
	default:
		t.Fatal("context did not cancel after abort")
	}
}

func TestAgentStateMutatorsParity(t *testing.T) {
	a := agent.NewAgent()

	a.State.SystemPrompt = "Custom prompt"
	if a.State.SystemPrompt != "Custom prompt" {
		t.Fatalf("unexpected system prompt: %q", a.State.SystemPrompt)
	}

	newModel := testModel()
	newModel.Provider = "google"
	a.State.Model = newModel
	if a.State.Model != newModel {
		t.Fatal("unexpected model assignment")
	}

	a.State.ThinkingLevel = chonkai.ThinkingHigh
	if a.State.ThinkingLevel != chonkai.ThinkingHigh {
		t.Fatalf("unexpected thinking level: %q", a.State.ThinkingLevel)
	}

	tools := []agent.Tool{{Name: "test", Description: "test tool"}}
	a.SetTools(tools)
	if len(a.State.Tools) != 1 || a.State.Tools[0].Name != "test" {
		t.Fatal("tools not set correctly")
	}

	messages := []agent.AgentMessage{userMessage("Hello")}
	a.SetMessages(messages)
	if len(a.State.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(a.State.Messages))
	}

	newMessage := assistantText("Hi", chonkai.StopReasonStop)
	a.State.Messages = append(a.State.Messages, newMessage)
	if len(a.State.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(a.State.Messages))
	}

	a.SetMessages(nil)
	if len(a.State.Messages) != 0 {
		t.Fatalf("expected messages cleared, got %d", len(a.State.Messages))
	}
}

func TestAgentSteeringQueueParity(t *testing.T) {
	a := agent.NewAgent()
	msg := userMessage("Steering message")
	a.Steer(msg)
	for _, m := range a.State.Messages {
		if um, ok := m.(chonkai.UserMessage); ok && um.RawText == msg.RawText {
			t.Fatal("steering message should not be in state messages yet")
		}
	}
}

func TestAgentFollowUpQueueParity(t *testing.T) {
	a := agent.NewAgent()
	msg := userMessage("Follow-up message")
	a.FollowUp(msg)
	for _, m := range a.State.Messages {
		if um, ok := m.(chonkai.UserMessage); ok && um.RawText == msg.RawText {
			t.Fatal("follow-up message should not be in state messages yet")
		}
	}
}

func TestAgentAbortNoPanic(t *testing.T) {
	a := agent.NewAgent()
	a.Abort()
}

func TestAgentPromptWhileStreaming(t *testing.T) {
	a := agent.NewAgent(
		agent.WithModel(testModel()),
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- a.Prompt(ctx, userMessage("First"))
	}()

	time.Sleep(10 * time.Millisecond)

	if err := a.Prompt(ctx, userMessage("Second")); err == nil || err.Error() != agent.ErrAgentBusy.Error() {
		t.Fatalf("expected ErrAgentBusy, got %v", err)
	}

	a.Abort()
	<-done
}

func TestAgentContinueWhileStreaming(t *testing.T) {
	a := agent.NewAgent(
		agent.WithModel(testModel()),
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- a.Prompt(ctx, userMessage("First"))
	}()

	time.Sleep(10 * time.Millisecond)

	if err := a.Continue(ctx); err == nil || err.Error() != agent.ErrAgentBusyContinue.Error() {
		t.Fatalf("expected ErrAgentBusyContinue, got %v", err)
	}

	a.Abort()
	<-done
}

func TestAgentContinueFollowUpFromAssistantTail(t *testing.T) {
	a := agent.NewAgent(
		agent.WithModel(testModel()),
		agent.WithStreamFn(streamFnFromMessages([]chonkai.AssistantMessage{
			assistantText("Processed", chonkai.StopReasonStop),
		}, false)),
	)

	a.State.Messages = []agent.AgentMessage{
		userMessage("Initial"),
		assistantText("Initial response", chonkai.StopReasonStop),
	}

	a.FollowUp(userMessage("Queued follow-up"))

	if err := a.Continue(context.Background()); err != nil {
		t.Fatalf("continue failed: %v", err)
	}

	found := false
	for _, m := range a.State.Messages {
		if um, ok := m.(chonkai.UserMessage); ok && um.RawText == "Queued follow-up" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected queued follow-up to be added")
	}

	last := a.State.Messages[len(a.State.Messages)-1]
	if _, ok := last.(chonkai.AssistantMessage); !ok {
		t.Fatal("expected last message to be assistant")
	}
}

func TestAgentContinueSteeringOneAtATime(t *testing.T) {
	responseCount := 0
	a := agent.NewAgent(
		agent.WithModel(testModel()),
		agent.WithStreamFn(func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
			responseCount++
			msg := assistantText("Processed", chonkai.StopReasonStop)
			msg.Content = []chonkai.ContentBlock{chonkai.TextContent{Type: "text", Text: "Processed"}}
			return streamWithMessage(msg, false)
		}),
	)

	a.State.Messages = []agent.AgentMessage{
		userMessage("Initial"),
		assistantText("Initial response", chonkai.StopReasonStop),
	}

	a.Steer(userMessage("Steering 1"))
	a.Steer(userMessage("Steering 2"))

	if err := a.Continue(context.Background()); err != nil {
		t.Fatalf("continue failed: %v", err)
	}

	recent := a.State.Messages[len(a.State.Messages)-4:]
	roles := make([]string, 0, 4)
	for _, m := range recent {
		switch m.(type) {
		case chonkai.UserMessage:
			roles = append(roles, "user")
		case chonkai.AssistantMessage:
			roles = append(roles, "assistant")
		}
	}
	expected := []string{"user", "assistant", "user", "assistant"}
	if len(roles) != len(expected) {
		t.Fatalf("unexpected roles length: %v", roles)
	}
	for i := range roles {
		if roles[i] != expected[i] {
			t.Fatalf("unexpected roles sequence: %v", roles)
		}
	}
	if responseCount != 2 {
		t.Fatalf("expected 2 responses, got %d", responseCount)
	}
}

func TestAgentSessionIDForwarded(t *testing.T) {
	var received string
	a := agent.NewAgent(
		agent.WithModel(testModel()),
		agent.WithSessionID("session-abc"),
		agent.WithStreamFn(func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, opts *chonkai.SimpleStreamOptions) *chonkai.EventStream {
			received = opts.SessionID
			return streamWithMessage(assistantText("ok", chonkai.StopReasonStop), false)
		}),
	)

	if err := a.Prompt(context.Background(), userMessage("hello")); err != nil {
		t.Fatalf("prompt failed: %v", err)
	}
	if received != "session-abc" {
		t.Fatalf("unexpected session id: %q", received)
	}

	a.SessionID = "session-def"
	if a.SessionID != "session-def" {
		t.Fatalf("unexpected session id after set: %q", a.SessionID)
	}
	if err := a.Prompt(context.Background(), userMessage("hello again")); err != nil {
		t.Fatalf("prompt failed: %v", err)
	}
	if received != "session-def" {
		t.Fatalf("unexpected session id: %q", received)
	}
}
