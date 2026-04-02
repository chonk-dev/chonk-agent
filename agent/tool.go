package agent

import (
	"context"
	"encoding/json"

	chonkai "github.com/chonk-dev/chonk-ai"
	"github.com/google/jsonschema-go/jsonschema"
)

// ToolOption 工具配置选项
type ToolOption func(*Tool)

// WithLabel 设置工具标签
func WithLabel(label string) ToolOption {
	return func(t *Tool) {
		t.Label = label
	}
}

// WithSchema 从 Go struct 生成 JSON Schema
func WithSchema[T any]() ToolOption {
	return func(t *Tool) {
		schema, err := jsonschema.For[T](nil)
		if err != nil {
			return
		}
		data, _ := json.Marshal(schema)
		t.Parameters = json.RawMessage(data)
	}
}

// WithPrepare 添加参数预处理钩子
func WithPrepare[T any](fn func(raw map[string]any) (T, error)) ToolOption {
	return func(t *Tool) {
		t.PrepareArguments = func(raw map[string]any) (map[string]any, error) {
			result, err := fn(raw)
			if err != nil {
				return nil, err
			}
			// 将 T 转为 map[string]any
			data, err := json.Marshal(result)
			if err != nil {
				return nil, err
			}
			var m map[string]any
			err = json.Unmarshal(data, &m)
			return m, err
		}
	}
}

// WithValidate 添加输入验证钩子
func WithValidate(fn func(context.Context, map[string]any) error) ToolOption {
	return func(t *Tool) {
		t.ValidateInput = fn
	}
}

// WithPermission 添加权限检查钩子
func WithPermission(fn func(context.Context, map[string]any) (PermissionResult, error)) ToolOption {
	return func(t *Tool) {
		t.CheckPermission = fn
	}
}

// WithReadOnly 标记为只读工具
func WithReadOnly() ToolOption {
	return func(t *Tool) {
		t.IsReadOnly = true
		t.IsConcurrencySafe = true
	}
}

// WithDestructive 标记为破坏性工具
func WithDestructive() ToolOption {
	return func(t *Tool) {
		t.IsDestructive = true
		t.IsConcurrencySafe = false
		t.InterruptBehavior = InterruptCancel
	}
}

// WithProgressType 设置进度类型
func WithProgressType(pt ToolProgressType) ToolOption {
	return func(t *Tool) {
		t.ProgressType = pt
	}
}

// WithSummary 设置摘要函数
func WithSummary(fn func(map[string]any) string) ToolOption {
	return func(t *Tool) {
		t.GetSummary = fn
	}
}

// WithActivityDesc 设置活动描述函数
func WithActivityDesc(fn func(map[string]any) string) ToolOption {
	return func(t *Tool) {
		t.GetActivityDesc = fn
	}
}

// NewTool 创建工具
func NewTool(name, desc string, execute ToolExecute, opts ...ToolOption) Tool {
	t := Tool{
		Name:              name,
		Label:             name,
		Description:       desc,
		Execute:           execute,
		IsConcurrencySafe: true,
		IsReadOnly:        false,
		InterruptBehavior: InterruptBlock,
	}

	for _, opt := range opts {
		opt(&t)
	}

	return t
}

// ToChonkai 转换为 chonkai.Tool
func (t *Tool) ToChonkai() chonkai.Tool {
	return chonkai.Tool{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  t.Parameters,
	}
}
