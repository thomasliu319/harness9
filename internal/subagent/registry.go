// Package subagent — Registry：子代理定义注册表。
// 本文件实现 Registry，持有所有已注册子代理定义（SubAgentDefinition）的集合。
// 约定：启动阶段一次性注册（Register/LoadFromDir），运行期只读（List/Get），
// 因此无需加锁，与 tools.Registry 的使用约定一致。
package subagent

import (
	"fmt"
	"sort"
)

// Registry 持有所有已注册子代理定义。线程不安全：约定在启动阶段一次性注册，
// 运行期只读（List/Get），与现有 tools.Registry 的使用约定一致。
type Registry struct {
	defs map[string]SubAgentDefinition
}

// NewRegistry 创建空的子代理定义注册表。
func NewRegistry() *Registry {
	return &Registry{defs: make(map[string]SubAgentDefinition)}
}

// Register 注册一个子代理定义。定义非法或重名时返回 error。
func (r *Registry) Register(def SubAgentDefinition) error {
	if err := def.Validate(); err != nil {
		return err
	}
	if _, exists := r.defs[def.Name]; exists {
		return fmt.Errorf("子代理 %q 已注册", def.Name)
	}
	r.defs[def.Name] = def
	return nil
}

// Get 按名称查找子代理定义。
func (r *Registry) Get(name string) (SubAgentDefinition, bool) {
	def, ok := r.defs[name]
	return def, ok
}

// List 返回所有子代理定义，按 Name 升序稳定排序。
func (r *Registry) List() []SubAgentDefinition {
	out := make([]SubAgentDefinition, 0, len(r.defs))
	for _, d := range r.defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
