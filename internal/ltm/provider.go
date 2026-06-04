// Package ltm — provider：Phase 3 扩展接口定义（Phase 3 接缝）。
// 本文件定义三个扩展接口（Provider / Embedder / Consolidator）和 noopProvider 默认实现，
// 作为向量嵌入语义检索、Dreaming 巩固和外部记忆提供者的预留插口（Phase 3 扩展点）。
// 当前主流程不调用这些接口，noopProvider 使代码在不注入任何实现的情况下可编译运行。
package ltm

import (
	"context"

	"github.com/harness9/internal/schema"
)

// Provider 是外部记忆提供者的扩展接口（Phase 3 接缝，当前仅 noopProvider 实现）。
// 参考 HermesAgent 的提供者插件系统：每个生命周期阶段允许外部存储介入。
//
// 后续可实现接入 Mem0 / Honcho / 向量库等外部记忆后端。
type Provider interface {
	// Prefetch 在每个 turn 前按 query 预取相关记忆（语义检索）。
	Prefetch(ctx context.Context, query string) ([]*Entry, error)
	// Sync 在每个 turn 结束后同步对话数据给提供者。
	Sync(ctx context.Context, userContent, assistantContent string) error
	// OnPreCompress 在上下文压缩前从待压缩消息中提取记忆。
	OnPreCompress(ctx context.Context, msgs []schema.Message) error
	// OnSessionEnd 在会话结束时执行收尾固化。
	OnSessionEnd(ctx context.Context) error
}

// Embedder 是向量嵌入扩展接口（Phase 3 接缝，当前无实现）。
// 后续可接入 Ollama 本地嵌入或 OpenAI Embeddings，为 Store 增加语义检索。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Consolidator 是 Dreaming 巩固扩展接口（Phase 3 接缝，当前无实现）。
// 后续可由 cron 触发，批量筛选短期信号晋升为长期记忆。
type Consolidator interface {
	Consolidate(ctx context.Context) (promoted int, err error)
}

// noopProvider 是 Provider 的空实现，所有钩子均为无操作。
// 作为默认提供者占位，使主流程在未配置外部提供者时仍可正常运行。
type noopProvider struct{}

// NewNoopProvider 返回一个无操作的 Provider。
func NewNoopProvider() Provider { return noopProvider{} }

func (noopProvider) Prefetch(context.Context, string) ([]*Entry, error)    { return nil, nil }
func (noopProvider) Sync(context.Context, string, string) error            { return nil }
func (noopProvider) OnPreCompress(context.Context, []schema.Message) error { return nil }
func (noopProvider) OnSessionEnd(context.Context) error                    { return nil }
