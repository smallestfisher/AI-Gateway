# 08 · MCP 预留设计

> **本轮只预留接口与数据模型，不做运行时实现**。目标是让未来「Claude Code / Codex CLI 通过网关使用 MCP」可以平滑落地，不必改核心。

---

## 1. 背景与目标

MCP（Model Context Protocol）把「工具/资源/提示」标准化。网关在 MCP 生态中有两个潜在角色：

1. **MCP Consumer（消费方）**：把 MCP server 提供的工具，作为 IR 的 `Tools` 暴露给上游模型调用。
2. **MCP Provider（提供方）**：网关自身对外暴露一个 MCP server，聚合后端配置好的多个 MCP server 的能力，供客户端发现与调用。

本轮设计**同时为两者预留**，但 v1 仅实现最小骨架（注册表 + 工具发现接口）。

---

## 2. 三个预留子系统

### 2.1 MCP Registry（注册表）
- 数据库：`mcp_servers`（见 `03-database.md §2.6`）。
- 字段：`name`、`transport`（stdio/sse/http）、`command_url`、`env`、`enabled`。
- 运行时：`MCPRegistry` 维护「已连接的 MCP client」句柄池，懒连接、健康探活。

### 2.2 MCP Routing（路由/绑定）
- 数据库：`mcp_bindings`（scope: `global` / `model` / `client`，target_id，permission）。
- 含义：某个 MCP server 的工具集，在什么范围内被**注入**到请求的 `Tools`。
- 解析优先级（与 Client Profile 一致）：`client > model > global`，工具集**取并集**（同名按高优先级覆盖）。

### 2.3 MCP Permission（权限）
- `mcp_bindings.permission`（JSONB）：
  ```json
  {
    "allow_tools": ["fs_read", "git_status"],
    "deny_tools": ["fs_write"],
    "auto_approve": true,
    "require_confirmation": ["dangerous_*"]
  }
  ```
- 预留「自动批准 / 需确认 / 拒绝」三态策略。

---

## 3. 接口设计（Go，预留）

```go
package mcp

// Server 描述一个已注册的 MCP server
type Server struct {
    ID        string
    Name      string
    Transport string            // stdio | sse | http
    Endpoint  string            // 命令或 URL
    Env       map[string]string
}

// Registry MCP 注册与连接管理（v1 仅接口 + 注册表 CRUD）
type Registry interface {
    List() []*Server
    Get(id string) (*Server, bool)
    // Discover 列出某 server 暴露的工具（转成 IR Tool）
    Discover(ctx context.Context, id string) ([]ir.Tool, error)
    // Call 调用某工具（v2 实现，用于网关侧工具执行）
    Call(ctx context.Context, id, toolName string, args json.RawMessage) ([]ir.Block, error)
}

// Binder 把 MCP 工具按绑定关系注入请求
type Binder interface {
    // Inject 根据 (model, client) 解析绑定的 MCP server，合并工具集到 req.Tools
    Inject(ctx context.Context, req *ir.UnifiedRequest) error
}
```

### 3.1 在 Pipeline 中的位置（预留钩子）
```
Ingress.DecodeRequest → IR
   │
   ▼
mcp.Binder.Inject(req)        ◀── 预留：把绑定的 MCP 工具加入 req.Tools
   │
   ▼
Router.Resolve → Egress.Send
   │
   ▼（若上游返回 tool_use，且工具是 MCP 工具，且上游不原生执行）
mcp.Registry.Call(...)        ◀── 预留：网关侧执行 MCP 工具，回填 tool_result，循环
   │
   ▼
Ingress.EncodeResponse
```

> v1：`Inject` 为空实现（不注入），`Call` 未启用。接口已就位，后续填充即可。

---

## 4. 对外 MCP 端点（预留）

未来网关可作为 MCP Provider 暴露：
```
GET  /v1/mcp/servers                  # 列出可用 MCP server（按权限）
POST /v1/mcp/tools/call               # 调用工具（受 permission 控制）
GET  /v1/mcp/resources                # 列出资源
```
- 鉴权复用 API Key + 权限模型。
- 路由与 Admin「协议管理」登记的一个自定义 protocol（`mcp`）挂钩。

---

## 5. v1 / v2 范围

| 能力 | v1（本轮预留） | v2（后续实现） |
|------|---------------|----------------|
| MCP server CRUD（后台） | ✅ 表 + Admin API | — |
| 工具发现 `Discover` | ✅ 接口 | 实现（连接 stdio/sse/http） |
| 工具注入 `Inject` | ✅ 钩子（空实现） | 按绑定合并工具集 |
| 网关侧工具执行 `Call` | ❌ | ✅（含权限/确认） |
| 对外 MCP Provider 端点 | ❌ | ✅ |
| 权限策略 UI | ✅ 表 + 基础字段 | ✅ 完整审批流 |

---

## 6. 数据流示例（v2 愿景）

```
Claude Code → 网关(POST /v1/messages, model=claude-sonnet)
   │ Binder.Inject：该 model 绑定了 mcp:filesystem → 注入 fs_read/fs_write 工具
   ▼
Router → Egress → Anthropic（带 fs_read/fs_write 工具定义）
   │ Anthropic 返回 tool_use(fs_read, {path})
   ▼
mcp.Registry.Call(filesystem, fs_read, {path}) → tool_result
   │ 注入回历史，再次 Egress → Anthropic
   ▼
Anthropic 返回最终文本 → 回 Claude Code
```

> 注意：网关侧工具执行会让网关「持有会话状态」（需缓存工具调用历史），这是 v2 的复杂点，v1 不引入。
