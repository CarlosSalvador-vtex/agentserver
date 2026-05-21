# OpenAPI 组织 — 设计

**日期**: 2026-05-21
**作者**: brainstorming with user (mryao)
**关联**: 现有 `docs/api/tui-integration.openapi.yaml`（已 stale，引用了 #135 删掉的 executor-registry，本期不复用）

## 目标

把 agentserver 现有的 public REST API（76 条 `/api/...`，本期收紧到 ~55 条 REST CRUD）整理成 OpenAPI 3.0 spec：

1. **可读文档** — 开发者 / 第三方对接者通过 SwaggerUI/Redoc 查接口
2. **前端 TypeScript client 替代手写 `api.ts`** — 类型 + 调用全部 codegen 生成，`web/src/lib/api.ts` 最终移除

## 非目标（本期不做）

- OAuth / Hydra proxy / OIDC SSO 端点（4 + 2 + 4，后续 phase）
- WebSocket / SSE 端点（OpenAPI 3.0 模型不合，单独 markdown 文档处理）
- `/api/internal/...` 服务间 RPC（scope 外）
- 运行时 request/response 校验中间件（推迟，本期只 docs + codegen）
- 第三方语言 SDK（Python/Go/Rust，本期不做）
- 统一 error envelope 重构（本期按 handler 实际形状写）
- API versioning prefix（`/v1/...`）—— 现状无 prefix，保持

## 架构

```
        Go handler (// @Summary ... 注释)
                │
                ▼ swag init -g internal/server/server.go
        docs/api/openapi.yaml + openapi.json   (committed)
                │
                ├──► SwaggerUI / Redoc 静态部署（人读）
                └──► openapi-typescript 生成
                        web/src/lib/api-generated/schema.d.ts (gitignored, CI 现生)
                                │
                                ▼
                        web/src/lib/api.ts 各 helper 内部
                        改用 generated types + fetch；
                        外部签名不变 → 替换完成后整个文件移除
```

**Source of truth = Go handler 的 swaggo 注释**。改接口必须同步改注释，CI 在 build 时跑 `make openapi-check` 检测 spec 是否同步（drift fail）。

## Scope

Phase 1 覆盖 **55 个 REST CRUD endpoint**，按 tag 分组：

| Tag | 数量约 | 端点示例 |
|---|---|---|
| Auth | 5 | `/api/auth/login`, `/register`, `/check`, `/logout`, `/me` |
| Workspaces | ~10 | `/api/workspaces`, `/{id}`, `/{id}/members`, `/quota` |
| Sandboxes | ~8 | `/api/workspaces/{id}/sandboxes`, `/api/sandboxes/{id}` |
| IM Channels | ~8 | `/api/workspaces/{id}/im/channels` + provider variants |
| Codex Tokens | ~4 | `/api/workspaces/{id}/codex/tokens` |
| Codex Browser Sessions | ~4 | session create/list/revoke |
| Agent Discovery / Tasks / Mailbox | ~10 | `/api/agent/discovery/cards`, `/tasks/...`, `/mailbox/...` |
| Misc | ~5 | `/api/agent/register`, workspace rename, etc. |

**显式不做**：OAuth flow / OIDC / Hydra proxy / WebSocket / SSE / `/api/internal/*` / `/healthz`, `/readyz` / 静态资源。

## 文件布局

```
agentserver/
├── docs/api/
│   ├── openapi.yaml        ← swag 输出（committed）
│   ├── openapi.json        ← 同上 JSON 版（committed）
│   └── README.md           ← 怎么改、怎么生成、怎么 view、frontend codegen
├── Makefile
│   • openapi:           swag init -g internal/server/server.go -o docs/api/ --parseDependency
│   • openapi-check:     swag init ... -o /tmp/openapi-check/ && diff docs/api/openapi.yaml /tmp/openapi-check/openapi.yaml
└── .github/workflows/build.yml
    test job 增 step: make openapi-check  →  Go test 之后跑，drift 即 fail

web/
├── package.json
│   • scripts.openapi:gen: openapi-typescript ../docs/api/openapi.yaml -o src/lib/api-generated/schema.d.ts
├── src/lib/api-generated/   ← gitignored；CI build 时现生；本地按需 npm run openapi:gen
└── src/lib/api.ts            ← 渐进替换；Phase 1.b 结束后移除
```

**Codegen 选择**：`openapi-typescript`（只生成 types/schema，不生成 client 函数）+ 一个薄 fetch wrapper（位于 `web/src/lib/apiClient.ts`，~30 行，职责：① 拼基础 URL ② 用 generated schema 的 path+method 推导 request/response 类型 ③ 把 4xx/5xx 转成抛出的 `ApiError` ④ 透传 cookie 凭证 `credentials: 'include'`）。理由：项目已有 fetch 习惯，重型的 `openapi-typescript-codegen`（含 axios client）会引入不必要的运行时依赖。

## Phasing

### Phase 1.a — Infra + Auth 作 proof（一个 PR）

- 加 `github.com/swaggo/swag` 依赖
- 加 Makefile targets `openapi` / `openapi-check`
- CI 加 drift check
- 给 Auth 5 个 handler 加 `// @Summary ... // @Tags Auth ... // @Param ... // @Success ...` 注释
- 抽取 Auth handler 里的匿名 request/response struct 为命名 type（swaggo 需要 named type 才能引用）
- 跑 `make openapi` 生成 `docs/api/openapi.yaml`
- 前端：加 `openapi-typescript` 依赖、加 `npm run openapi:gen`、把 `api.ts` 里的 `login` / `register` / `logout` / `me` / `check` 5 个 helper 改为内部用 generated `schema.d.ts` 的 types + fetch，外部签名不变
- 端到端验证：改一处 Auth handler 注释不同步 → CI fail；前端 `pnpm tsc` 通过

### Phase 1.b — 其余 7 个 tag，按顺序各一个 PR

顺序：Workspaces → Sandboxes → IM Channels → Codex Tokens → Codex Browser Sessions → Agent Discovery/Tasks/Mailbox → Misc

每个 PR 模板：
- handler 加 swaggo 注释
- 抽取匿名 struct 为命名 type
- 跑 `make openapi`，commit 生成产物
- 前端：对应 tag 下的 `api.ts` helper 内部改成 generated；外部签名不变
- 最后一个 PR（Misc 完成后）把 `web/src/lib/api.ts` 整个删掉，直接 export generated client wrapper

## 关键决策记录

| 决策 | 选项 | 选定 | 理由 |
|---|---|---|---|
| Spec source of truth | hand-written / swaggo / spec-first oapi-codegen | swaggo | 现有代码不需要大改，注释贴在 handler 旁就近维护 |
| 范围 | all / public / 对外集成面 / 分层 | 只 public 76 → 本期 55 (REST CRUD) | 76 已经够 phase 一期；OAuth/WS 单独 phase |
| 前端 client | 与手写共存 / 替代手写 | **替代手写**，最终移除 `api.ts` | 单一 source、避免长期维护两套 |
| codegen 工具 | openapi-typescript / openapi-typescript-codegen | openapi-typescript（仅 types）+ 项目自带 fetch wrapper | 不引入 axios，运行时更轻 |
| Error envelope | 现状保留 / 重构统一 | 现状保留（按 handler 注释实际形状） | 重构是独立工程，不绑 OpenAPI 推进 |
| spec 版本号 | 跟 chart / 独立 | 独立（`info.version: 0.1.0` 起步） | spec 节奏比 chart 慢得多 |
| generated TS 是否 commit | yes / gitignore | gitignore（CI 现生） | 减少 diff 噪音，本质是 derived |
| API path versioning | 现在引入 `/v1/...` / 推迟 | 推迟 | 当前所有 client 都跟主分支走，无 break compat 需求 |

## 风险 & 缓解

- **匿名 struct 抽取改动量**：每个 tag PR 都需要把 inline `var req struct {...}` 提到 package-level named type。视 handler 风格而定，每个 PR 5-15 个 struct。**缓解**：每个 tag PR 自包含、可独立 review。
- **swaggo 注释 与 实际行为漂移**：CI drift check 只检测 spec 是否随注释更新，**不**检测注释是否描述准确。**缓解**：每个 Phase 1.b 的 PR 在 review 时人肉对 swaggo 注释 vs handler 实际行为。
- **OpenAPI 3.0 模型限制**（multipart 上传、cookie auth 表达力）：先按 swaggo 默认能表达的写，无法表达的写在 description 里。本期不为这类极少数 endpoint 引入特殊处理。
- **frontend codegen 输出体积**：`schema.d.ts` 可能几千行。**缓解**：纯 types 文件、tree-shake 友好，运行时 0 成本。

## 后续 phase（不在本期范围）

- **Phase 2**: OAuth + OIDC + Hydra proxy 端点纳入
- **Phase 3**: WebSocket / SSE 端点的单独 markdown 文档
- **Phase 4**: 第三方 SDK（Python 至少一个）
- **Phase 5**: 运行时 request validation middleware（如果届时仍需要）
- **Phase 6**: 统一 error envelope（如果届时业务需要）
