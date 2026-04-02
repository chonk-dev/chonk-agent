package agent

import (
	"context"

	chonkai "github.com/chonk-dev/chonk-ai"
)

// StreamFn 流式函数签名
type StreamFn func(
	ctx context.Context,
	model *chonkai.Model,
	conv *chonkai.Context,
	opts *chonkai.SimpleStreamOptions,
) *chonkai.EventStream

// StreamSimple 简化的流式调用（默认实现）
func StreamSimple(
	ctx context.Context,
	model *chonkai.Model,
	conv *chonkai.Context,
	opts *chonkai.SimpleStreamOptions,
) *chonkai.EventStream {
	return chonkai.StreamSimple(ctx, model, conv, opts)
}
