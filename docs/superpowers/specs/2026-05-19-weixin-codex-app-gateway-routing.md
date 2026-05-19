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
- 同一用户连发多条消息 → agentserver handler 内 FIFO 排队顺序处理

非 MVP 目标（Phase 2+）：
- 流式分段发送（按段落 / 代码块切块）
- 图片入站 / 出站
- 工具调用过程可视化
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

1. **CXG `/api/turns` 是纯 REST↔ws 格式转换器**。把 REST 请求翻成 codex v2 JSON-RPC `turn/start` 帧、等 `turn/completed`、把整个 codex `Turn` 对象塞进响应返回。**不识别业务语义**——不判断 thread-busy、不重试、不解读 `turn.error`。只在协议转换本身失败时（subprocess 起不来、ws 永久断、自己 timeout）填响应的 `transport` 字段。和 `/codex-app/ws` / `/notebook/ws` 一样保持 CXG 的纯净，只是对外协议形态不同。
2. **REST 是 JSON-RPC 的薄信封**。`POST /api/turns` 请求 / 响应字段名与嵌套 1:1 镜像 codex v2 protocol 类型，`params.input` 就是 `turn/start.params.input`、`items[]` 就是 `item/completed.item` 序列、`status` 就是 `turn/completed.status`。CXG 内部用 `json.RawMessage` 透传，不维护与 codex 同步的 schema。
3. **per-workspace 一条 loopback ws 长连**，多 turn 多路复用。首次 dial 时 initialize / initialized 一次；后续 turn 直接走。空闲 5min 或对端关时清理重建。
4. **审批全自动同意（MVP）**。两条防御：
   - `codexhome.go` 的 `[mcp_servers.agentserver]` 加 `default_tools_approval_mode = "approve"` —— 让 codex 大多数时候压根不发审批请求
   - broker 收到任何 `*Approval` / `requestUserInput` / `elicitation/request` 帧仍立即回 approve / allow —— 兜底
5. **thread_id 持久化在 agent_sessions 新列**。复用 stateless_cc 的 `(workspace_id, external_id=chat_jid)` 主键。CXG 完全无状态，thread state 在 subprocess 自己的 `CODEX_HOME` 里（supervisor 已 S3 持久化 + idle reap）。
6. **业务策略（排队 / 错误恢复 / 重试）在 agentserver imbridge handler**。per-(channel_id, user_id) in-process FIFO 队列保证同一用户的消息按发送顺序串行处理；thread-not-found 类错误（启发式匹配 `turn.error.message`）自动清 `codex_thread_id` 后重试一次（自动走 `thread/start` 新建）。详见「失败与并发」节。
7. **MVP 鉴权全靠 `X-Internal-Secret`** —— `/api/turns` 不走 Bearer。Service account token 模型留给 Phase 2（如果以后想把 `/api/turns` 公开 / 复用 CXG 现有 Bearer 验签链）。

## REST API（对齐 JSON-RPC）

### 请求

```http
POST /api/turns
X-Internal-Secret: <CODEX_INTERNAL_SECRET>
Content-Type: application/json

{
  "workspaceId": "ws-xxx",
  "threadId": "thr-xxx" | null,
  "params": {
    "input": [
      {"type": "text", "text": "..."},
      {"type": "image", "url": "data:image/jpeg;base64,..."}
    ]
  },
  "timeoutMs": 300000
}
```

字段语义：
- `workspaceId`：必填，CXG 定位 supervisor key
- `threadId`：可选；null/缺省 → broker 内部先发 `thread/start` 获取 thread id 并把结果回写到响应的 `turn.threadId` 同级字段
- `params`：**1:1 镜像 codex `TurnStartParams` 类型**（`codex-rs/app-server-protocol/src/protocol/v2/turn.rs:49`）。MVP 业务上只用 `input: UserInput[]`，但 broker 整体作为 `json.RawMessage` 透传给 ws 帧——`approvalPolicy` / `cwd` / `environments` 等其它字段如果客户端传了也直接给 codex。
  - **不要**自己在 `params` 里塞 `threadId`：codex `TurnStartParams.threadId` 由 broker 内部填，REST 调用者只填顶层的 `threadId`
- `timeoutMs`：可选，默认 300000（5min）

**字段命名约定**：整个 REST API 用 camelCase 镜像 codex v2 protocol（所有 v2 类型都带 `#[serde(rename_all = "camelCase")]`）。Go 端 struct tag 写 `` `json:"threadId"` ``。这样 broker 真的只是把 `params` 包成 ws frame 字段、把响应里的 `turn` / `item` 解出来透传，零字段重命名。

`UserInput` variants（与 codex `v2/turn.rs:238` `pub enum UserInput` 一致）：
- `{"type":"text","text":"...","textElements":[]}` —— textElements 可选
- `{"type":"image","url":"https://... or data:..."}` —— base64 用 `data:` URL 包
- `{"type":"localImage","path":"/abs/path"}`
- `{"type":"skill","name":"...","path":"..."}`
- `{"type":"mention","name":"...","path":"..."}`

### 响应

HTTP 状态 `200` 表示 codex turn 完成了一个生命周期（无论 codex 业务上成功失败）。响应 body **直接嵌入 codex 的 `Turn` 对象**：

```jsonc
{
  "threadId": "thr-xxx",
  // ↓ codex Turn (codex-rs/app-server-protocol/src/protocol/v2/thread_data.rs:153)
  "turn": {
    "id": "trn-yyy",
    "items": [
      {"type": "userMessage", "id": "...", "content": [/* UserInput[] */]},
      {"type": "reasoning", "id": "...", "summary": ["..."], "content": ["..."]},
      {"type": "mcpToolCall", "id": "...", /* ... */},
      {"type": "agentMessage", "id": "...", "text": "src/main.go 里的 ..."},
      {"type": "commandExecution", "id": "...", "command": "...", "status": "completed", /* ... */},
      {"type": "fileChange", "id": "...", "changes": [...], "status": "..."},
      {"type": "plan", "id": "...", "text": "..."}
      // 见 codex-rs/app-server-protocol/src/protocol/v2/item.rs:212 pub enum ThreadItem
    ],
    "itemsView": "full",
    "status": "completed",        // TurnStatus: completed | interrupted | failed | inProgress
    "error": null,                // 当 status=failed 时 = TurnError {message, codexErrorInfo, additionalDetails}
    "startedAt": 1747673160,
    "completedAt": 1747673165,
    "durationMs": 5000
  },
  "transport": null   // broker 加的传输层失败标记，正常请求一律 null
}
```

`transport` 字段（**broker 自己加的，不是 codex 的**）：

```jsonc
"transport": {
  "code": "brokerTimeout" | "wsDisconnect" | "subprocessCrash",
  "message": "human-readable"
}
```

规则：
- `turn` 非 null + `transport` null：codex 成功跑完一个生命周期，结果按 `turn.status` 判断
  - `turn.status = "completed"` → 真成功
  - `turn.status = "failed"` → codex 业务报错，看 `turn.error.message` / `turn.error.codexErrorInfo`（枚举：`contextWindowExceeded` / `usageLimitExceeded` / `serverOverloaded` / `cyberPolicy` / `httpConnectionFailed` / ...，定义在 `v2/shared.rs:CodexErrorInfo`）
  - `turn.status = "interrupted"` → 被 codex 内部策略 `turn/interrupt` 打断（broker 自己的 timeout 走 `transport=brokerTimeout` 路径，不会到这里）
- `turn` null + `transport` 非 null：broker 在协议转换阶段就失败了（subprocess 起不来、ws 永久断、http 超时），没拿到任何 codex 输出
- 不会两个同时非 null

**注**：broker 完全不解读 `turn.status` / `turn.error` 的业务含义，原样塞到 REST body 里。只在传输层失败时填 `transport`。这样 codex 后续新增 `TurnStatus` variant、新增 `CodexErrorInfo` variant，CXG 一行不改。

### JSON-RPC 协议错（独立通道）

如果 ws 上 codex 返了标准 JSON-RPC error response（罕见，通常是请求格式错），broker 把它当传输错处理：返 `turn=null, transport={code:"wsDisconnect"...}` 或拓展一个新 `code:"rpcError"` 把 `{code,message,data}` 塞 `message` 里。MVP 简化为 `wsDisconnect`，不区分。

### 响应（请求错 / 内部错）

| HTTP | 含义 | body |
|---|---|---|
| 400 | 缺 workspaceId、input 为空、JSON 解析失败等 | `{"error":{...}}` |
| 401 | X-Internal-Secret 不对 | text |
| 502 | subprocess 起不来、loopback ws 永久 dial 失败、broker panic | `{"error":{...}}` |
| 503 | per-workspace 并发上限（Phase 2 加） | `{"error":{...}}` |

### agentserver 侧消费

`/api/internal/imbridge/codex/turn` handler 在收到 `/api/turns` 响应后：

1. **`transport` 非 null** → broker 协议层失败：
   - `subprocessCrash` / `wsDisconnect` → 「⚠️ Codex 处理失败，请稍后重试」
   - `brokerTimeout` → 「⚠️ 处理超时，请稍后重试」

2. **`turn.status === "completed"`** → 真成功：
   - 倒序扫 `turn.items`，取**最后一条** `type:"agentMessage"` 的 `text` 字段，调 `/api/internal/imbridge/send`
   - 如果某个 item 是 codex 后续新增的图片 item type：MVP 忽略，Phase 2 走 `/api/internal/imbridge/send-image`
   - 持久化 `threadId`（来自 REST 响应顶层）到 `agent_sessions.codex_thread_id`

3. **`turn.status === "failed"`** → codex 业务报错，看 `turn.error`：
   - `error.codexErrorInfo` 是 `"contextWindowExceeded"` → 「⚠️ 上下文已满，请新开会话」+ **清空 `codex_thread_id`** 让下条消息开新 thread
   - `error.codexErrorInfo` 是 `"usageLimitExceeded"` → 「⚠️ Codex 配额已用尽」
   - `error.codexErrorInfo` 是 `"serverOverloaded"` → 「⚠️ Codex 繁忙，请稍后重试」
   - `error.message` 模式匹配 thread-not-found 类（启发式：含 `"thread"` + (`"not found"` / `"unknown"` / `"missing"`)）→ **清空 `codex_thread_id`，handler 内部立即重新调一次 `/api/turns`（threadId=null 新建）**；二次失败按通用失败
   - 其它 `turn.error` → 「⚠️ Codex 处理失败」+ `error.message` 写日志

4. **`turn.status === "interrupted"`** → 被打断（broker 因 timeoutMs 触发，或 codex 内部判断 cancel）：
   - 区分不出来 broker 自己 cancel 还是 codex 自己 cancel —— 统一返 「⚠️ 处理已取消，请重发」

5. **`turn.status === "inProgress"`** → 不应该见到（broker 只在 `turn/completed` notification 后才返），见到当作 transport=wsDisconnect 处理

无论上述哪条路径，调 send endpoint 都触发 `bridge.StopTyping` 副作用。

## 失败与并发

### 排队：agentserver handler 内 per-user FIFO

agentserver `/api/internal/imbridge/codex/turn` handler 维护一个进程内 dispatcher：

```go
type codexDispatcher struct {
    mu     sync.Mutex
    queues map[string]chan *codexRequest   // key: channelID + ":" + userID
}
```

- 入站消息 → handler 给 bridge 立即返 202 → 把请求 enqueue 到对应 key 的 chan
- 每个 key 第一次 enqueue 时启动一个 goroutine worker，从 chan 串行取任务、调 CXG、发回复
- 队列长度上限 5；超限 → drop 最旧的入队任务，对被 drop 的用户发一次合并提示「⚠️ 消息过多，已忽略 N 条早前消息」
- worker 在 chan 空且空闲 30s 后退出（懒清理 map 条目）

为什么不在 bridge.go 加 queue？bridge 已经是 per-channel cursor 推进，加 per-user queue 会和 cursor 语义纠缠。把队列放 handler 层既贴近业务也不污染 imbridge。CXG broker 不动——它依然只是协议转换器。

### 失败场景表

| 场景 | 处理（broker 永远只做协议中转） |
|---|---|
| 同 user 连发 N 条消息 | agentserver handler FIFO queue 串行处理；broker 看到的永远是单线 |
| 不同 user 同 workspace 并发 | agentserver handler 启不同 worker → broker 内 loopback ws 多路复用并发发 → codex subprocess 内部自己串行 thread |
| codex 业务报错 | broker 原样返 `turn.status="failed"` + `turn.error` —— broker 不解读；handler 按 `turn.error.codexErrorInfo` / `message` 启发式分流 |
| codex 报 thread-not-found（thread id 过期） | broker 同上原样返；handler 检测后清 `codex_thread_id` + **重试一次**（threadId=null 新建）；二次失败走通用失败 |
| codex subprocess 崩 | supervisor respawn；该 workspace 下 in-flight turn 立即 `turn=null, transport={code:"subprocessCrash"}` |
| loopback ws 失联 | 该 workspace 下 in-flight turn 立即 `turn=null, transport={code:"wsDisconnect"}`；下次访问重建 |
| 任何 `item/*/requestApproval` / `item/tool/requestUserInput` / `item/permissions/requestApproval` / `mcpServer/elicitation/request` 帧 | broker 立即回 approve / allow，turn 继续（不出现在 REST 响应里） |
| turn 超过 timeoutMs | broker 发 `turn/interrupt`，等到 `turn/completed` 后**丢弃 turn 内容**直接返 `turn=null, transport={code:"brokerTimeout"}`。代价：失去任何 partial output；好处：保持「`turn` 和 `transport` 互斥」的简单语义 |
| codex 自己 interrupt（罕见，例如内部策略中止） | broker 原样返 `turn.status="interrupted", transport=null`；handler 文案「⚠️ 处理已取消，请重发」 |
| agentserver → CXG HTTP 5xx | imbridge handler 返回 502 给 bridge → bridge 不推进 cursor → 下次 poll 重试（at-least-once） |
| CXG → agentserver send 失败 | imbridge handler log + drop reply（与 stateless_cc 一致；MVP 不重试） |
| 微信端 -14 session expired | bridge 自动 1h backoff（之前 PR 已加） |

### typing indicator
- imbridge handler enqueue 后**立刻给 bridge 返 202** → bridge 启 5min typing keepalive（已有逻辑）
- worker 处理完一条 task → POST 微信 send endpoint → 出站 handler 自动 `bridge.StopTyping`
- **MVP 限制**：queue 里第 2+ 条任务被 worker 处理时不会重启 typing（因为这些 task 不会触发新一轮 bridge.forward）。用户在等队列中后续消息回复时看不到 typing。可接受；Phase 2 加一个内部 endpoint 让 handler 能主动 ping bridge 重启 typing。
- 所有失败路径都要走 send endpoint（哪怕只发错误文案）以触发 StopTyping

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
  - brokerTimeout / subprocessCrash / wsDisconnect / thread-not-found 自动重建 等错误路径都触发对应中文文案 + StopTyping
  - 同 user 连发 3 条消息按 FIFO 顺序串行处理，互不交错

**手测**：
- 拿一个真微信号配 routing_mode=codex
- 文本：「写个 hello world」，验证 ~10s 内回复包含代码块
- 多轮：「改成 typescript」，验证 codex 看到上下文
- 一条还在跑时连发 2 条：验证两条都按发送顺序拿到回复
- 把 CXG kill 掉一个 turn 中途：验证错误提示 + typing 停

## Phase 2 / 3 follow-up（不在本 spec 实现）

- 流式：REST 升级 SSE，按段落 / 代码块切块送回微信
- 图片入站 / 出站
- queue 期间 typing keepalive 自动续约（agentserver handler 主动 ping bridge 重启）
- oplog 审计：复用 `oplog/interceptor` 给 `/api/turns` 加同款拦截，归因 `system:weixin-channel` + wechat_user_id
- envmcp 工具加 annotations（`readOnlyHint` / `destructiveHint`）改善 TUI 体验
- `thread/resume` 跨 ws 断连场景验证

## 待验证（写代码时要确认的协议细节）

- codex v2 image input 的 schema 确切字段名（spec 提到支持，但 `InputItem` image variant 字段名要看 `codex-rs/app-server-protocol/src/protocol/v2/`）
- `thread/resume` 在新 ws 第一次使用旧 thread_id 时是否必须显式调用
- codex 拒绝 permessage-deflate —— broker dial 时务必 `CompressionMode: Disabled`
- `default_tools_approval_mode = "approve"` 是否完全消掉 `item/tool/requestUserInput` —— 如有遗漏，broker 兜底
