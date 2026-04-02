package agent

import "errors"

// Agent 相关错误
var (
	ErrAgentBusy                   = errors.New("Agent is already processing a prompt. Use steer() or followUp() to queue messages, or wait for completion.")
	ErrAgentBusyContinue           = errors.New("Agent is already processing. Wait for completion before continuing.")
	ErrNoMessages                  = errors.New("No messages to continue from")
	ErrCannotContinueFromAssistant = errors.New("Cannot continue from message role: assistant")
)
