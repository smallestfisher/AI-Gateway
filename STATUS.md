# AI Gateway 项目状态

**更新日期：** 2026-06-21  
**当前阶段：** Phase 5 局部完成 + Sub-project 1（Config Admin UI）已完成

---

## ✅ 已完成的功能

### 核心网关功能（Phase 0-5）

**✅ 统一 IR 与协议适配器**
- ✅ Unified IR（Block Model）完整实现
- ✅ OpenAI Chat Completions 适配器（非流式 + 流式）
- ✅ Anthropic Messages 适配器（非流式 + 流式）
- ✅ OpenAI Responses 适配器 / Codex CLI（非流式 + 流式）
- ✅ 跨协议无损转换测试通过（工具调用、reasoning、usage 等）

**✅ 路由与健康管理**
- ✅ Router：别名 → 多通道，failover / weighted / auto 模式
- ✅ Health 模块：Redis 滑动窗口熔断器（错误率 + TTFT）
- ✅ 熔断状态：open → half-open → closed 自动恢复
- ✅ 首字节前 Failover（Pipeline 层）

**✅ Egress（上游调用）**
- ✅ 按 Provider 分离的传输池
- ✅ Auth 注入（API Key / Bearer Token）
- ✅ Client Profile 三级合并（default → provider → model）
- ✅ 超时控制、重试逻辑、可重试 vs 不可重试错误分类
- ✅ 流式 SSE 转发
- ✅ TTFT 和延迟采样（写入 Redis 供健康检查）

**✅ 配置中心（PostgreSQL + Redis）**
- ✅ PostgreSQL schema（16 张表，包含 `request_logs`）
- ✅ Registry：DB 配置快照 + Redis pub/sub 热更新
- ✅ 原子快照切换（零 DB 热路径）
- ✅ 配置版本追踪（`config_meta.version`）

**✅ 认证与计费**
- ✅ API Key 认证（DB + Redis 缓存）
- ✅ RPM 限流（Redis 固定窗口）
- ✅ Token 配额计费（Redis 余额 + 惰性 DB 同步）
- ✅ 模型白名单
- ✅ 错误响应：401 / 403 / 402 / 429（符合各协议规范）

**✅ Admin API（内部管理）**
- ✅ REST CRUD：Providers / Models / Model-Channels / Client Profiles / Router Policies
- ✅ 用户管理：创建用户、发放 API Key（明文仅显示一次）、配额设置
- ✅ Bearer Token 认证（静态 `GATEWAY_ADMIN_TOKEN`）
- ✅ 配置热更新触发（写操作自动发布 `config:invalidate`）

**✅ 基础设施**
- ✅ Docker Compose 一键启动（gateway + postgres + redis）
- ✅ Go 构建、vet、测试全部通过（包含跨协议集成测试）
- ✅ 健康检查端点：`GET /healthz`
- ✅ 可从环境变量或 DB 配置启动

---

### Config Admin UI（Sub-project 1 — 前端管理界面）

**✅ 技术栈**
- ✅ Next.js 16.2.9（App Router）
- ✅ TypeScript + Tailwind CSS 4 + shadcn/ui
- ✅ TanStack Query v5（状态管理）
- ✅ react-hook-form + zod（表单验证）
- ✅ next-themes（深色/浅色主题）
- ✅ 温暖土色设计系统（terracotta accent + warm cream background）

**✅ 认证与架构**
- ✅ Token 登录 → httpOnly cookie
- ✅ BFF 模式：Next.js Route Handler 代理 `/api/admin/*`
- ✅ `proxy.ts` 守卫（Next.js 16 的 middleware 重命名）
- ✅ 401 自动重定向登录页

**✅ 已实现页面（4 个配置页面）**
1. **✅ Providers**（`/providers`）
   - List / Create / Edit / Delete
   - 字段：name, protocol, base_url, api_key, timeouts, retries, weight, priority, health thresholds, metadata, enabled
   - DataTable + Sheet 表单 + 确认对话框

2. **✅ Models + Channels**（`/models`）
   - Model alias CRUD
   - 每个 Model 展开显示 Channels
   - Channel 绑定/解绑（provider + upstream_model + weight + priority）

3. **✅ Client Profiles**（`/profiles`）
   - CRUD: scope (default/provider/model), target, user_agent, origin, referer, headers(JSON), strip_client_headers, enabled

4. **✅ Router Policies**（`/router`）
   - 全局策略 + 按模型覆盖
   - mode (failover/weighted/auto) + params

**✅ UI 组件库**
- ✅ shadcn/ui 全套组件：Button, Card, Dialog, Sheet, Table, Badge, Select, Switch, Tooltip, Sonner Toast
- ✅ DataTable（可排序、可筛选）
- ✅ FormSheet（侧边抽屉表单）
- ✅ ConfirmDialog（删除确认）
- ✅ EmptyState（空状态提示）
- ✅ Sidebar 导航 + Topbar（面包屑 + 主题切换）

**✅ 数据层**
- ✅ `lib/types.ts`：镜像 Go DTOs
- ✅ `lib/api.ts`：类型化 fetch 封装
- ✅ `hooks/use-resource.ts`：通用 CRUD hooks
- ✅ TanStack Query：自动缓存失效 + 乐观更新

---

## ⚠️ 未完成的功能

### Sub-project 2：Log Center + Dashboard（后端未就绪）

**❌ 缺失后端：请求日志写入**
- `request_logs` 表已存在（schema 完整），但**没有代码写入数据**
- 需要在 `internal/pipeline` 或 `internal/server` 增加日志记录逻辑
- 需要：请求开始/结束时写入 `request_logs`（含 TTFT、latency、tokens、status、error）

**❌ Log Center 前端（依赖日志数据）**
- 查询界面（按用户/模型/Provider/状态/时间筛选）
- 请求详情查看（IR 转换备注、headers、body）
- 导出功能

**❌ Dashboard 前端（依赖日志数据）**
- 实时统计卡片（请求数、活跃用户、Provider 数、平均延迟）
- 成功率图表
- TTFT 趋势图
- 按模型 Top 5
- 最近异常列表

**❌ Health 监控页面（前端）**
- 实时健康状态展示
- 熔断历史
- TTFT / 延迟分布图

---

### Sub-project 3+：请求模拟器（Request Simulator）

**❌ 后端 API（`/api/admin/simulator`）**
- Dry-run 模式：模拟请求路由（不实际发送上游）
- 分阶段展示：原始请求 → IR 转换 → 选中 Channel → 出口 URL/Headers/Body
- 转换备注（协议差异、字段映射）

**❌ 前端页面**
- 模拟器表单（选择协议、模型、输入示例请求）
- 分阶段可视化渲染
- 导出模拟结果

---

### Users & API Keys 管理页面（前端）

**后端已就绪**（`POST /api/admin/users`, `POST /api/admin/users/:id/api-keys`, `PUT /api/admin/users/:id/quota`）

**❌ 前端缺失**
- `/users` 页面：用户列表、创建用户
- 每个用户展开查看：
  - API Keys 列表（发放新 Key，显示 key 一次后不再展示）
  - 配额设置（balance, rpm, tpm, whitelist）
  - 禁用用户

---

### 其他待完善功能

**❌ 协议管理页面（Protocols）**
- 当前内置协议（openai_chat, anthropic_messages, openai_responses）
- 前端仅需展示（只读），暂不支持动态添加协议

**❌ MCP 管理（Phase 5+）**
- 后端：`mcp_servers` 和 `mcp_bindings` 表已存在
- 缺失：MCP Server 注册 CRUD 的 Admin API
- 缺失：工具发现接口（`Discover`）
- 缺失：前端页面

**❌ Audit Logs 页面**
- `audit_logs` 表已存在
- 缺失：后端写入审计日志逻辑
- 缺失：前端查询页面

**❌ 测试**
- **Web UI 测试缺失**：规范要求 Vitest + @testing-library/react，但未实现
- Go 集成测试需要 `GATEWAY_TEST_POSTGRES_DSN` 和 `GATEWAY_TEST_REDIS_ADDR` 环境变量

**❌ ClickHouse 日志迁移（Phase 5）**
- `request_logs` 当前在 PostgreSQL
- 规模化后需迁移到 ClickHouse（`LogSink` 接口切换）

**❌ 更多协议适配器（Phase 5）**
- Google Gemini
- OpenRouter
- AWS Bedrock

**❌ MCP 工具执行（Phase 5 v2）**
- 网关侧 MCP 工具调用（`Call` + 会话状态）

**❌ 可观测性增强**
- Prometheus/Grafana 集成
- 指标分片
- 连接池调优

**❌ 生产环境优化**
- 多副本部署验证
- 压测（目标：1000+ 并发流式请求）
- 代理出口 IP 池轮换

---

## 📋 优先级建议（接下来做什么）

### 高优先级（产品完整性）

1. **✅ Users & API Keys 前端页面**（1-2 天）
   - 后端已就绪，只需前端 CRUD 页面
   - 用户可以通过 UI 发放 API Key，完成"可对外提供服务"的闭环

2. **❌ 请求日志写入（后端）**（2-3 天）
   - 在 `internal/pipeline` 或 `internal/server` 增加日志记录
   - 写入 `request_logs` 表（含 TTFT、latency、tokens、status）
   - 解锁 Dashboard 和 Log Center

3. **❌ Dashboard 前端**（3-4 天）
   - 实时统计卡片
   - 成功率 / TTFT 趋势图
   - 按模型 Top 5
   - 最近异常列表

4. **❌ Log Center 前端**（3-4 天）
   - 请求日志查询（筛选 + 分页）
   - 请求详情（IR 转换、headers、body）
   - 导出功能

### 中优先级（可观测性）

5. **❌ Health 监控页面**（2-3 天）
   - 实时健康状态
   - 熔断历史
   - TTFT / 延迟分布

6. **❌ Request Simulator**（4-5 天）
   - 后端 dry-run API
   - 前端分阶段可视化

7. **❌ Audit Logs**（2 天）
   - 后端写入逻辑
   - 前端查询页面

### 低优先级（扩展功能）

8. **❌ 前端测试**（2-3 天）
   - Vitest + @testing-library/react
   - 表单验证 + 少量 hooks 测试

9. **❌ MCP 管理**（Phase 5+，5-7 天）
   - Admin API：MCP Server CRUD
   - 工具发现接口
   - 前端页面

10. **❌ ClickHouse 迁移**（Phase 5，3-5 天）
    - LogSink 接口抽象
    - ClickHouse 适配器
    - 数据迁移脚本

11. **❌ 更多协议适配器**（每个 3-5 天）
    - Google Gemini
    - OpenRouter
    - AWS Bedrock

---

## 🎯 当前里程碑状态

| 里程碑 | 可对外能力 | 状态 |
|--------|-----------|------|
| M1（P1 末） | Claude Code/OpenAI SDK 经网关用 1 个上游，工具调用 + 流式可用 | ✅ 完成 |
| M2（P2 末） | Codex 可用；多上游 failover/weighted；客户端伪装 | ✅ 完成 |
| M3（P3 末） | 完整后台可视化 + 健康熔断 + auto 路由（**生产可用的最小 SaaS**） | ⚠️ **配置页面完成，Users/Logs/Dashboard 待补** |
| M4（P4 末） | 全可观测 + 模拟器 + MCP 注册 | ❌ 未开始 |
| M5（P5） | 规模化 + 多协议扩展 | ❌ 未开始 |

---

## 🚀 快速验证（当前可测试的功能）

### 启动网关

```bash
# 1. 启动 docker compose（gateway + postgres + redis）
docker compose up --build

# 2. 健康检查
curl http://localhost:8080/healthz
# => {"protocols":["openai_chat","anthropic_messages","openai_responses"],"status":"ok"}
```

### 启动 Web UI

```bash
cd apps/web

# 1. 安装依赖（需要 Node 20+ / pnpm）
pnpm install

# 2. 配置环境变量（创建 .env.local）
# GATEWAY_URL=http://localhost:8080

# 3. 启动开发服务器
pnpm dev
# => http://localhost:3000

# 4. 登录
# 浏览器访问 http://localhost:3000/login
# 输入 GATEWAY_ADMIN_TOKEN（从 docker-compose.yml 或环境变量获取）
```

### 已可测试的功能

- ✅ Providers 管理（增删改查）
- ✅ Models + Channels 管理
- ✅ Client Profiles 管理
- ✅ Router Policies 管理
- ✅ 配置热更新（修改后秒级生效，无需重启）
- ✅ 深色/浅色主题切换

### 待补充才能测试的功能

- ❌ 用户管理 + API Key 发放（需要前端页面）
- ❌ Dashboard 统计（需要日志写入）
- ❌ 日志查询（需要日志写入）
- ❌ 健康监控可视化（需要前端页面）
- ❌ 请求模拟器（需要后端 API）

---

## 📝 总结

**当前状态：** 核心网关功能（Phase 0-5）已完成，Config Admin UI（Sub-project 1）的 4 个配置页面已实现，**但 Users & Keys、Dashboard、Log Center、Health、Simulator 等页面尚未开发**。

**最紧急待补：**
1. **Users & API Keys 前端页面**（后端已就绪，仅需 2 天）
2. **请求日志写入后端逻辑**（解锁 Dashboard 和 Log Center）

**里程碑状态：** M1/M2 完成，M3 部分完成（配置管理 ✅，可观测性 ❌），M4/M5 未开始。

**交付建议：** 优先完成 Users 页面（快速闭环），然后补全日志写入 + Dashboard + Log Center（达到"生产可用的最小 SaaS"标准）。
