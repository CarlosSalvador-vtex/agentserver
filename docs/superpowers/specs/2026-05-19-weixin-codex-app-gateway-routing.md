# 设计：微信 channel 路由到 codex-app-gateway

**日期**：2026-05-19
**状态**：已对齐，待按本文写 implementation plan

## 目标

为 `workspace_im_channels.routing_mode = "codex"` 的微信 channel 加一条新的转发路径，把入站消息送到 `codex-app-gateway`（以下简称 **CXG**）背后的 `codex app-server` subprocess 处理，并把 codex 生成的助手回复送回微信。

成功标准（Phase 1 MVP）：
- 用户在微信发文本 → 收到 codex 生成的回复文本
- 同一用户的多轮对话保持 codex thread 上下文
- 一个 workspace 下多个微信用户并发对话互不串
- codex 处理失败 / 超时 / 子进程崩溃 → 用户收到中文错误提示，typing indicator 停掉
- 同一用户上一条还在跑就再发新消息 → 收到「⚠️ 上一条还在处理」拒绝（不排队）

非 MVP 目标（Phase 2+）：
- 流式分段发送（按段落 / 代码块切块）
- 图片入站 / 出站
- 工具调用过程可视化
- per-thread FIFO queue（取代 MVP 的拒绝模式）
- oplog 审计接入

## 背景：当前两侧的契约

### imbridge 侧（已有）
- `Bridge.forwardMessage` (`internal/imbridge/bridge.go:411`) 按 `RoutingMode` 二选一：
  - `"nanoclaw"`（默认空）→ POST 沙箱 pod `/message`
  - `"stateless_cc"` → POST `agentserver` 的 `/api/workspaces/{wsID}/im/inbound`，由 cc-broker 异步处理，回复经 `/api/internal/imbridge/send` 推回
- 入站 metadata（含 `context_token`）写入 `channel_meta` 表；出站时回读、传给 `provider.Send(ctx, creds, to, text, meta)`
- 入站 forward 成功后启动 typing keepalive（5min 兜底），出站调 `bridge.StopTyping(channelID, userID)`
- DB schema：`workspace_im_channels.routing_mode TEXT`；UI 通过 `PATCH /api/workspaces/{wid}/im/channels/{cid}` 改

### CXG 侧（已有）
- 当前对外 endpoints：
  - `GET /codex-app/ws` —— TUI 透传代理，纯字节转发
  - `GET /notebook/ws` —— jupyter SDK 透传代理 + `oplog/interceptor` 审计
- 均为 codex v2 JSON-RPC over WebSocket，1:1 镜像 `codex app-server --listen` 的协议
- 一个 workspace 一个 codex subprocess（`supervisor.Key{WorkspaceID}`），多线程在 subprocess 内部共享 sqlite state
- subprocess 由 `internal/codexappgateway/supervisor/` 管理，含 spawn、readyz wait、idle reap、crash respawn、`CODEX_HOME` 的 S3 持久化
- 通过 `[mcp_servers.agentserver]` 把所有工具能力外包给 `env-mcp` 子模块；内置 `shell_tool`/`unified_exec`/`apply_patch_freeform` 全部禁用

### codex 审批 RPC 现状（关键事实）
- codex v2 protocol 定义在 `codex-rs/app-server-protocol/src/protocol/common.rs:1277-1341`
- 与本方案相关的几个 server-to-client 请求：
  - `item/commandExecution/requestApproval`、`item/fileChange/requestApproval` —— 内置 shell / apply-patch 路径触发；CXG 已禁用，**不触发**
  - `item/permissions/requestApproval` —— sandbox 权限升级；不太会发生
  - `item/tool/requestUserInput` —— MCP 工具调用的通用审批入口
  - `mcpServer/elicitation/request` —— MCP 服务端发起的 elicitation；envmcp 不发
- MCP 工具是否需要审批由 `codex-rs/core/src/mcp_tool_call.rs:2160` 决定：tool 的 annotations 没标 `readOnlyHint=true` 时默认 `approval_required=true`
- **envmcp 工具目前全无 annotations** → codex 会对**每个** `agentserver.*` 工具调用都发 `item/tool/requestUserInput`
- 默认 `default_tools_approval_mode = Auto` —— 这意味着 codex 会向客户端发审批请求等回复

## 架构

### 通信拓扑

```
WeChat user
   │ (iLink long-poll)
   ▼
WeixinProvider.Poll  ───►  Bridge.pollLoop
                              │
                              │  forwardMessage (case "codex")
                              ▼
                           imbridge HTTP POST /api/internal/imbridge/codex/turn
                              │
                              ▼
                    ┌──────────────────────────────────────────────┐
                    │  agentserver: codex_im_inbound handler       │
                    │  ① resolve agent_sessions.codex_thread_id    │
                    │  ② POST {CXG_URL}/api/turns                  │
                    │  ③ 响应回来后:                                │
                    │     - 持久化 thread_id                        │
                    │     - 抽取最后一条 text item                  │
                    │     - POST /api/internal/imbridge/send        │
                    └──────────────────────────────────────────────┘
                              │  (HTTP POST)
                              ▼
                    ┌──────────────────────────────────────────────┐
                    │  codex-app-gateway: POST /api/turns (新增)    │
                    │  ① 验证 X-Internal-Secret                    │
                    │  ② supervisor.EnsureSubprocess(workspace_id) │
                    │  ③ loopback ws 池 取/起 conn                  │
                    │     - 首次: dial + initialize + initialized  │
                    │  ④ thread/start (if thread_id null)          │
                    │  ⑤ turn/start {thread_id, params.input}      │
                    │  ⑥ 读 notification 流:                       │
                    │     - 自动批准 *Approval / requestUserInput  │
                    │     - 累积 item/completed.items              │
                    │  ⑦ turn/completed → 返回 JSON 给 agentserver │
                    └──────────────────────────────────────────────┘
                              │  (loopback ws)
                              ▼
                    codex app-server subprocess（已有 supervisor 管）
```

三种通信：

| 段 | 协议 | 跨进程? | 备注 |
|---|---|---|---|
| imbridge → agentserver handler | HTTP POST `/api/internal/imbridge/codex/turn`（X-Internal-Secret） | 现部署同进程，设计上可分 | 与 stateless_cc 路径同模式 |
| agentserver handler → CXG | HTTP POST `/api/turns`（X-Internal-Secret） | **是** | 新增；MVP 一次性 JSON 返回 |
| CXG → codex subprocess | WebSocket + codex v2 JSON-RPC | 否（loopback） | 复用 supervisor + 池化 ws |

### 关键决策

1. **新增 endpoint 在 CXG，不在 agentserver**。codex v2 JSON-RPC 客户端代码（initialize 握手 / notification demux / 审批处理）放在最靠近 subprocess 的地方；agentserver 只做薄 HTTP 调用。少一跳 ws、未来其它 channel（Telegram、web）可直接复用 `/api/turns`。
2. **REST 是 JSON-RPC 的薄信封**。`POST /api/turns` 请求 / 响应字段名与嵌套 1:1 镜像 codex v2 protocol 类型，`params.input` 就是 `turn/start.params.input`、`items[]` 就是 `item/completed.item` 序列、`status` 就是 `turn/completed.status`。CXG 内部用 `json.RawMessage` 透传，不维护与 codex 同步的 schema。
3. **per-workspace 一条 loopback ws 长连**，多 turn 多路复用。首次 dial 时 initialize / initialized 一次；后续 turn 直接走。空闲 5min 或对端关时清理重建。
4. **审批全自动同意（MVP）**。两条防御：
   - `codexhome.go` 的 `[mcp_servers.agentserver]` 加 `default_tools_approval_mode = "approve"` —— 让 codex 大多数时候压根不发审批请求
   - broker 收到任何 `*Approval` / `requestUserInput` / `elicitation/request` 帧仍立即回 approve / allow —— 兜底
5. **thread_id 持久化在 agent_sessions 新列**。复用 stateless_cc 的 `(workspace_id, external_id=chat_jid)` 主键。CXG 完全无状态，thread state 在 subprocess 自己的 `CODEX_HOME` 里（supervisor 已 S3 持久化 + idle reap）。
6. **per-thread 串行**。同 thread 上一条 turn 还在跑就拒绝新消息（MVP）；Phase 2 升级 FIFO queue。检测机制：broker 在已知 thread_id 上发 `turn/start`，codex 会返回 thread-already-active 错误；broker 捕获并映射为 `error.code=thread_busy`。无需 broker 自己维护 in-flight 集合。
7. **MVP 鉴权全靠 `X-Internal-Secret`** —— `/api/turns` 不走 Bearer。Service account token 模型留给 Phase 2（如果以后想把 `/api/turns` 公开 / 复用 CXG 现有 Bearer 验签链）。

## REST API（对齐 JSON-RPC）

### 请求

```http
POST /api/turns
X-Internal-Secret: <CODEX_INTERNAL_SECRET>
Content-Type: application/json

{
  "workspace_id": "ws-xxx",
  "thread_id": "thr-xxx" | null,
  "params": {
    "input": [
      {"type": "text", "text": "..."},
      {"type": "image", "source": {"type": "base64", "media_type": "image/jpeg", "data": "..."}}
    ]
  },
  "timeout_ms": 300000
}
```

字段语义：
- `workspace_id`：必填，CXG 定位 supervisor key
- `thread_id`：可选；null/缺省 → broker 内部先发 `thread/start` 获取 thread id 并回写到响应
- `params`：内部 1:1 镜像 codex `turn/start.params`，MVP 只用 `input`，其余字段（model、permission_profile）若有也透传
- `timeout_ms`：可选，默认 300000（5min）

### 响应（成功 / codex 报错 / 超时 / 取消）

HTTP 状态 `200` 表示 codex turn 完成了一个生命周期（无论成功失败）：

```jsonc
{
  "thread_id": "thr-xxx",
  "turn_id": "trn-yyy" | null,
  "status": "completed" | "failed" | "cancelled" | "timeout",
  "items": [
    {
      "type": "text",
      "content": [{"type": "text", "text": "..."}]
    },
    {
      "type": "tool_call",
      "name": "agentserver.read_file",
      "arguments": {"path": "..."},
      "result": {"...": "..."}
    },
    {
      "type": "image",
      "content": [{"type": "image", "source": {"...": "..."}}]
    }
  ],
  "error": {
    "code": "subprocess_crash" | "ws_disconnect" | "codex_failed" | "timeout" | "invalid_thread" | "thread_busy",
    "message": "..."
  } | null
}
```

- `status="completed"` → `error` 为 null
- 其它 status → `error` 必填
- MVP 失败状态返回 `items=[]`；Phase 2 可保留 partial items

### 响应（请求错 / 内部错）

| HTTP | 含义 | body |
|---|---|---|
| 400 | 缺 workspace_id、input 为空、JSON 解析失败等 | `{"error":{...}}` |
| 401 | X-Internal-Secret 不对 | text |
| 502 | subprocess 起不来、loopback ws 永久 dial 失败、broker panic | `{"error":{...}}` |
| 503 | per-workspace 并发上限（Phase 2 加） | `{"error":{...}}` |

### agentserver 侧消费

`/api/internal/imbridge/codex/turn` handler 在收到 `/api/turns` 响应后：
- 若 `status="completed"`：从 `items[]` 取**最后一条** `type:"text"` 的 content 拼成纯文本，调 `/api/internal/imbridge/send`
- 若 `status="completed"` 且有 `type:"image"` items：MVP 忽略，Phase 2 调 `/api/internal/imbridge/send-image`
- 若 `status` 非 completed：根据 `error.code` 映射成中文文案，调 `/api/internal/imbridge/send`：
  - `subprocess_crash` / `ws_disconnect` → "⚠️ Codex 处理失败，请稍后重试"
  - `codex_failed` → "⚠️ Codex 处理失败：<error.message 简短摘要>"
  - `timeout` → "⚠️ 处理超时，请稍后重试"
  - `thread_busy` → "⚠️ 上一条还在处理，请稍候"
  - `invalid_thread` → "⚠️ 会话已重置，请重发消息"（agentserver handler 同时清空 `codex_thread_id`，下一条消息会重开 thread）
  - `cancelled` → "⚠️ 处理已取消"
- 无论上述哪条路径，发送 endpoint 自带 `bridge.StopTyping` 副作用

## 失败与并发

| 场景 | 处理 |
|---|---|
| 同 thread 同时 2 个 turn | codex 在 `turn/start` 上返回 thread-already-active；broker 捕获并 REST 返回 `status:"failed"`, `error.code="thread_busy"` |
| 不同 thread 同 workspace 并发 | codex subprocess 内部自己串行；REST 直接并发发，subprocess 排队 |
| codex subprocess 崩 | supervisor respawn；**该 workspace 下所有 in-flight turn** 立即标 `failed`, `subprocess_crash` |
| loopback ws 失联 | **该 workspace 下所有 in-flight turn** 立即标 `failed`, `ws_disconnect`；下次访问重建 ws |
| 客户端传 thread_id 但 codex 不认 | broker 捕获 codex 错误，REST 返回 `status:"failed"`, `error.code="invalid_thread"`；agentserver handler 收到后**清空** `agent_sessions.codex_thread_id` 并发错误文案 |
| 任何 `*Approval` / `requestUserInput` / `elicitation/request` 帧 | broker 立即回 approve / allow，turn 继续 |
| turn 超过 timeout_ms | broker 发 `turn/cancel`，REST 返回 `status:"timeout"` |
| agentserver → CXG HTTP 5xx | imbridge handler 返回 502 给 bridge → bridge 不推进 cursor → 下次 poll 重试（at-least-once） |
| CXG → agentserver send 失败 | imbridge handler log + drop reply（与 stateless_cc 一致；MVP 不重试） |
| 微信端 -14 session expired | bridge 自动 1h backoff（之前 PR 已加） |

### typing indicator
- imbridge handler 给 bridge 返 202 → bridge 启 5min typing keepalive（已有逻辑）
- handler 同步 await CXG REST 响应 → POST 微信 send endpoint → 出站 handler 自动 `bridge.StopTyping`（已有）
- 所有失败路径都要走 send（哪怕只发错误文案）以触发 StopTyping

## State / 持久化

```sql
ALTER TABLE agent_sessions ADD COLUMN codex_thread_id TEXT NULL;
```

- key 仍是 `(workspace_id, external_id)`，与 stateless_cc 共表
- 同 chat_jid 切 routing_mode 时 cc_thread_id / codex_thread_id 互不影响
- 字段读写通过 `db.GetSessionByExternalID` / `db.SetSessionCodexThreadID(sessionID, threadID)`

CXG 完全无状态；thread state 在 subprocess `CODEX_HOME` 里，supervisor 管。

鉴权：`/api/turns` MVP 走 `X-Internal-Secret`，与 agentserver 共享。**不引 service account token**，不动 `codex_remote_tokens` 表。Phase 2 如果想把 `/api/turns` 改成 Bearer 验签链复用，再加。

## 配置 / Migration

### CXG 启动配置（`internal/codexappgateway/config.go`）
- 复用 `INTERNAL_API_SECRET` 环境变量（agentserver 已用）即可；不引新变量
- mount `/api/turns` 时加 X-Internal-Secret middleware

### CXG codex 配置（`codexhome.go`）
```toml
[mcp_servers.agentserver]
# 已有字段保留
default_tools_approval_mode = "approve"   # 新增
```

### agentserver 启动配置
- 新增 env：`CODEX_APP_GATEWAY_URL`（默认 `http://codex-app-gateway:8086`）

### DB migration
```sql
-- migrations/NNN_codex_thread_id.sql
ALTER TABLE agent_sessions ADD COLUMN codex_thread_id TEXT NULL;
```

### UI
- channel 设置页 `routing_mode` 下拉框加 `codex` 选项
- handlers.go `routing_mode` 白名单加 `"codex"`

## 文件级改动清单

### CXG (`internal/codexappgateway/`)

**新增**：
- `turn_api.go` —— `POST /api/turns` HTTP handler、request / response struct、超时控制
- `broker/pool.go` —— per-workspace `*loopbackConn` 池
- `broker/conn.go` —— 单条 loopback ws：dial + initialize + initialized + frame 读循环 + notification demux（按 turn_id）+ 自动审批回复
- `broker/protocol.go` —— codex v2 JSON-RPC 类型最小子集（envelope: Request/Response/Notification; turn/start params; item/completed; turn/completed; thread/started; 4 个 approval method）。多余字段 `json.RawMessage` 透传

**改动**：
- `server.go` —— mount `/api/turns` + X-Internal-Secret middleware
- `codexhome/codexhome.go` —— `[mcp_servers.agentserver]` 加 `default_tools_approval_mode = "approve"`
- `config.go` —— 加 `InternalSecret` 字段（从 `INTERNAL_API_SECRET` 读）

### agentserver

**新增**：
- `internal/server/codex_im_inbound.go` —— `POST /api/internal/imbridge/codex/turn` handler；resolve agent_session、调 CXG、写回 send
- `internal/server/codex_client.go` —— 小 HTTP 客户端封装 CXG REST 调用（POST、超时、错误映射）

**改动**：
- `internal/imbridge/bridge.go`：
  - `forwardMessage` 加 `case "codex": return b.forwardToCodex(...)`
  - 新增 `forwardToCodex`（mimic `forwardToAgentserver`）
- `internal/imbridgesvc/handlers.go:971` —— routing_mode 白名单加 `"codex"`
- `internal/server/server.go` —— mount `/api/internal/imbridge/codex/turn`
- `internal/db/agent_sessions.go` —— `IMSession` struct 加 `CodexThreadID *string`；新方法 `SetSessionCodexThreadID(sessionID, threadID)`
- `internal/db/migrations/` —— 新 sql migration

### Web UI
- channel 设置页 routing_mode 下拉加 `codex`

### 不改
- `internal/codexappgateway/{supervisor,codexhome除上述,oplog,auth}` —— 全部基础设施直接复用
- `internal/weixin/ilink.go` —— 微信侧协议无关
- `internal/imbridge/weixin_provider.go` —— 出站 Send/SendImage 已支持 meta

## 工作量估算

| 模块 | 行数 |
|---|---|
| CXG turn_api + protocol types | ~300 |
| CXG broker pool + conn | ~400 |
| CXG codexhome 改动 | ~10 |
| agentserver codex_im_inbound handler + client | ~250 |
| agentserver bridge.forwardToCodex | ~80 |
| agentserver db migration + SetSessionCodexThreadID | ~40 |
| UI dropdown 加项 | ~20 |
| 单元测试 | ~400 |
| 集成测试 | ~150 |
| **合计** | **~1650 行** |

## 测试计划

**单元**：
- `internal/codexappgateway/broker/protocol_test.go`：手搓 frame，验证 envelope encode/decode、item/completed 内容提取、turn/completed 终止判断
- `internal/codexappgateway/broker/conn_test.go`：用 `httptest.NewServer` + `websocket.Accept` 模拟 codex subprocess，覆盖 handshake、断线重建、审批自动回复、turn 完整序列
- `internal/codexappgateway/turn_api_test.go`：handler-level，mock broker
- `internal/server/codex_im_inbound_test.go`：handler-level，mock CXG REST

**集成**：
- docker-compose 拉起 agentserver + CXG + 真 codex（或一个 echo provider mock）
- routing_mode=codex 的 channel，模拟 iLink poll 投消息，断言：
  - `agent_sessions.codex_thread_id` 正确建立 / 复用
  - 出站 send endpoint 收到正确 payload
  - thread_busy / timeout / subprocess_crash 错误路径都触发对应中文文案 + StopTyping

**手测**：
- 拿一个真微信号配 routing_mode=codex
- 文本：「写个 hello world」，验证 ~10s 内回复包含代码块
- 多轮：「改成 typescript」，验证 codex 看到上下文
- 一条还在跑时再发：验证「⚠️ 上一条还在处理」
- 把 CXG kill 掉一个 turn 中途：验证错误提示 + typing 停

## Phase 2 / 3 follow-up（不在本 spec 实现）

- 流式：REST 升级 SSE，按段落 / 代码块切块送回微信
- 图片入站 / 出站
- per-thread FIFO queue（取代拒绝）
- oplog 审计：复用 `oplog/interceptor` 给 `/api/turns` 加同款拦截，归因 `system:weixin-channel` + wechat_user_id
- envmcp 工具加 annotations（`readOnlyHint` / `destructiveHint`）改善 TUI 体验
- `thread/resume` 跨 ws 断连场景验证

## 待验证（写代码时要确认的协议细节）

- codex v2 image input 的 schema 确切字段名（spec 提到支持，但 `InputItem` image variant 字段名要看 `codex-rs/app-server-protocol/src/protocol/v2/`）
- `thread/resume` 在新 ws 第一次使用旧 thread_id 时是否必须显式调用
- codex 拒绝 permessage-deflate —— broker dial 时务必 `CompressionMode: Disabled`
- `default_tools_approval_mode = "approve"` 是否完全消掉 `item/tool/requestUserInput` —— 如有遗漏，broker 兜底
