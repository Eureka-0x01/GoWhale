package tools

import (
	"encoding/json"

	"gowhale/internal/llm"
)

// Tool 是一个「技能」：模型可以调用它来操作本地环境。
type Tool interface {
	Name() string
	Description() string
	// Schema 返回参数的 JSON Schema（OpenAI function parameters 格式）。
	Schema() map[string]any
	// Execute 执行工具，args 是模型给出的参数（JSON）。
	Execute(args json.RawMessage) (string, error)
}

// Approvable 由「有副作用、需要用户审批」的工具实现。
// 未实现该接口的工具（如只读的 read_file / list_dir）会被自动放行。
type Approvable interface {
	// Review 检查本次调用，返回审批决策。
	Review(args json.RawMessage) Decision
}

// Decision 描述一次操作的审批需求与「授权作用域」。
// 用户一旦对某个作用域选择「始终允许」，同作用域的后续操作将自动放行。
type Decision struct {
	NeedApproval bool   // 是否需要审批
	Danger       string // 高危原因（非空表示危险；危险操作不支持「始终允许」）
	ScopeKind    string // 作用域类型："dir"（按目录，前缀匹配）| "session"（会话级）| ""（不记忆）
	Scope        string // 作用域标识（dir：目录路径；session：命名键）
}

// Registry 管理所有可用工具。
type Registry struct {
	tools map[string]Tool
}

// New 用给定的工具集合创建注册表。
func New(ts ...Tool) *Registry {
	m := make(map[string]Tool, len(ts))
	for _, t := range ts {
		m[t.Name()] = t
	}
	return &Registry{tools: m}
}

// Definitions 把所有工具转换成发给模型的定义列表。
func (r *Registry) Definitions() []llm.Tool {
	defs := make([]llm.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, llm.Tool{
			Type: "function",
			Function: llm.ToolFunctionSchema{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		})
	}
	return defs
}

// Lookup 按名称取出工具。
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}
