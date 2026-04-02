package main

import (
	"context"
	"fmt"
	"os"

	"github.com/chonk-dev/chonk-agent/agent"
	chonkai "github.com/chonk-dev/chonk-ai"
	_ "github.com/chonk-dev/chonk-ai/provider/openai"
)

func main() {
	// 创建模型
	model := &chonkai.Model{
		ID:       "gpt-4o-mini",
		Api:      chonkai.ApiOpenAICompletions,
		Provider: "openai",
		BaseURL:  "https://api.openai.com/v1",
	}

	// 创建 Agent
	a := agent.NewAgent(
		agent.WithModel(model),
		agent.WithSystemPrompt("You are a helpful assistant."),
	)

	// 订阅事件
	events := a.Subscribe()
	go func() {
		for event := range events.Events() {
			switch event.Type {
			case agent.EventMessageUpdate:
				if event.AssistantEvent != nil && event.AssistantEvent.Type == chonkai.EventTextDelta {
					fmt.Print(event.AssistantEvent.Delta)
				}
			case agent.EventAgentEnd:
				fmt.Println("\nDone!")
			}
		}
	}()

	// 发送消息
	ctx := context.Background()
	if err := a.Prompt(ctx, chonkai.UserMessage{RawText: "Say hello in one word."}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// 等待完成
	if err := a.WaitIdle(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Wait error: %v\n", err)
		os.Exit(1)
	}
}
