package agent_test

import (
	"context"
	"encoding/json"
	"time"

	agent "github.com/chonk-dev/chonk-agent/agent"
	chonkai "github.com/chonk-dev/chonk-ai"
)

func testModel() *chonkai.Model {
	return &chonkai.Model{
		ID:       "mock",
		Name:     "mock",
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  "https://example.invalid",
	}
}

func userMessage(text string) chonkai.UserMessage {
	return chonkai.UserMessage{
		RawText:   text,
		Timestamp: time.Now(),
	}
}

func assistantMessage(content []chonkai.ContentBlock, stopReason chonkai.StopReason) chonkai.AssistantMessage {
	return chonkai.AssistantMessage{
		Content:    content,
		Api:        chonkai.ApiOpenAICompletions,
		Provider:   "openai",
		Model:      "mock",
		StopReason: stopReason,
		Timestamp:  time.Now(),
	}
}

func assistantText(text string, stopReason chonkai.StopReason) chonkai.AssistantMessage {
	return assistantMessage([]chonkai.ContentBlock{
		chonkai.TextContent{Type: "text", Text: text},
	}, stopReason)
}

func toolCallBlock(id, name string, args any) chonkai.ToolCall {
	data, _ := json.Marshal(args)
	return chonkai.ToolCall{
		Type:      "toolCall",
		ID:        id,
		Name:      name,
		Arguments: data,
	}
}

func streamWithMessage(msg chonkai.AssistantMessage, withDelta bool) *chonkai.EventStream {
	stream := chonkai.NewEventStream(8)
	go func() {
		if withDelta {
			partial := msg
			stream.Push(chonkai.Event{Type: chonkai.EventStart, Partial: &partial})
			stream.Push(chonkai.Event{Type: chonkai.EventTextDelta, Partial: &partial, Delta: ""})
		}
		stream.Push(chonkai.Event{Type: chonkai.EventDone, Reason: msg.StopReason, Message: &msg})
		stream.Close(msg, nil)
	}()
	return stream
}

func streamFnFromMessages(messages []chonkai.AssistantMessage, withDelta bool) agent.StreamFn {
	index := 0
	return func(_ context.Context, _ *chonkai.Model, _ *chonkai.Context, _ *chonkai.SimpleStreamOptions) *chonkai.EventStream {
		msg := messages[index]
		index++
		return streamWithMessage(msg, withDelta)
	}
}
