package agent

import "errors"

// Agent 相关错误
var (
	ErrAgentBusy                   = errors.New("agent is already processing a prompt. Use Steer() or FollowUp() to queue messages, or wait for completion")
	ErrNoMessages                  = errors.New("no messages to continue from")
	ErrCannotContinueFromAssistant = errors.New("cannot continue from message role: assistant")
)
