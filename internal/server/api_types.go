package server

import "time"

// This file holds package-level request/response types for the
// public REST API. swaggo annotations on handler funcs reference
// these by name; inline `var req struct {...}` shapes can't be
// referenced from annotations, which is why we extract them here.
//
// Group additions by API tag (Auth, Workspaces, …). Add new types
// alphabetically within each group so PRs from different tags don't
// trip over each other.
//
// IMPORTANT: required JSON fields need `validate:"required"` so swag
// emits them in the OpenAPI schema's `required` array. Without it the
// frontend codegen treats every field as `T | undefined`.

// --- Auth ---

// AuthCredentials is the email+password body for POST /api/auth/login
// and POST /api/auth/register.
type AuthCredentials struct {
	Email    string `json:"email" example:"alice@example.com" validate:"required"`
	Password string `json:"password" example:"hunter2" validate:"required"`
} //@name AuthCredentials

// AuthStatusResponse is the {"status":"ok"} envelope returned by
// /api/auth/login, /api/auth/logout, and /api/auth/check on success.
type AuthStatusResponse struct {
	Status string `json:"status" example:"ok" validate:"required"`
} //@name AuthStatusResponse

// AuthRegisterResponse is what POST /api/auth/register returns on
// success: the new user's id and the email it was registered with.
type AuthRegisterResponse struct {
	ID    string `json:"id"    example:"7e7a4f6c-..." validate:"required"`
	Email string `json:"email" example:"alice@example.com" validate:"required"`
} //@name AuthRegisterResponse

// AuthMeResponse is the current user payload returned by GET /api/auth/me.
// Name and Picture are populated from OIDC profile data when present
// (login via password leaves both empty). Both fields are always present
// in the JSON response — nil pointers serialize as null (not omitted).
type AuthMeResponse struct {
	ID      string  `json:"id" validate:"required"`
	Email   string  `json:"email" validate:"required"`
	Name    *string `json:"name" extensions:"x-nullable=true"`
	Picture *string `json:"picture" extensions:"x-nullable=true"`
	Role    string  `json:"role" example:"developer" validate:"required"`
} //@name AuthMeResponse

// --- Workspaces ---

// WorkspaceCreateRequest is the body for POST /api/workspaces.
type WorkspaceCreateRequest struct {
	Name string `json:"name" validate:"required" example:"My Workspace"`
} // @name WorkspaceCreateRequest

// WorkspaceRenameRequest is the body for PATCH /api/workspaces/{id}.
type WorkspaceRenameRequest struct {
	Name string `json:"name" validate:"required" example:"Renamed Workspace"`
} // @name WorkspaceRenameRequest

// WorkspaceQuotaResponse is the {"current": int, "max": int} envelope
// returned by GET /api/workspaces/quota.
type WorkspaceQuotaResponse struct {
	Current int `json:"current" validate:"required"`
	Max     int `json:"max" validate:"required"`
} // @name WorkspaceQuotaResponse

// MemberAddRequest is the body for POST /api/workspaces/{id}/members.
type MemberAddRequest struct {
	Email string `json:"email" validate:"required" example:"alice@example.com"`
	Role  string `json:"role" example:"developer"` // optional; defaults to "developer"
} // @name MemberAddRequest

// MemberRoleUpdateRequest is the body for PUT /api/workspaces/{id}/members/{userId}.
type MemberRoleUpdateRequest struct {
	Role string `json:"role" validate:"required" example:"maintainer"`
} // @name MemberRoleUpdateRequest

// LLMWorkspaceQuotaPart is the per-workspace quota override stored in the
// LLM proxy DB. max_rpd is null when no custom limit is configured.
type LLMWorkspaceQuotaPart struct {
	WorkspaceID string  `json:"workspace_id" validate:"required"`
	MaxRPD      *int    `json:"max_rpd" extensions:"x-nullable=true"`
	UpdatedAt   string  `json:"updated_at" validate:"required"`
} // @name LLMWorkspaceQuotaPart

// LLMQuotaResponse mirrors the body the LLM proxy returns from its
// /internal/quotas/{workspaceId} endpoint, forwarded verbatim by
// GET /api/workspaces/{id}/llm-quota.
// workspace_quota is null when no per-workspace override is set.
type LLMQuotaResponse struct {
	DefaultMaxRPD     int                    `json:"default_max_rpd" validate:"required"`
	TodayRequestCount int                    `json:"today_request_count" validate:"required"`
	WorkspaceQuota    *LLMWorkspaceQuotaPart `json:"workspace_quota" extensions:"x-nullable=true"`
} // @name LLMQuotaResponse

// LLMModel is one entry in a workspace's per-model LLM config.
// id is the model identifier used in API calls; name is the human-readable label.
type LLMModel struct {
	ID   string `json:"id" validate:"required" example:"claude-opus-4-7"`
	Name string `json:"name" validate:"required" example:"Claude Opus 4.7"`
} // @name LLMModel

// LLMConfigResponse is the body returned by GET /api/workspaces/{id}/llm-config.
// api_key is masked (first 3 + "..." + last 4 chars) and is empty
// when no config exists.
type LLMConfigResponse struct {
	Configured bool       `json:"configured" validate:"required"`
	BaseURL    string     `json:"base_url"`
	APIKey     string     `json:"api_key"`
	Models     []LLMModel `json:"models"`
	UpdatedAt  *string    `json:"updated_at" extensions:"x-nullable=true"`
} // @name LLMConfigResponse

// LLMConfigUpsertRequest is the body for PUT /api/workspaces/{id}/llm-config.
// All three fields are required for a fresh config; for an update,
// omitting api_key retains the existing key.
type LLMConfigUpsertRequest struct {
	BaseURL string     `json:"base_url" validate:"required" example:"https://api.anthropic.com"`
	APIKey  string     `json:"api_key" example:"sk-ant-..."` // optional on update
	Models  []LLMModel `json:"models" validate:"required"`
} // @name LLMConfigUpsertRequest

// LLMConfigUpsertResponse is the body returned by the upsert endpoint.
type LLMConfigUpsertResponse struct {
	OK bool `json:"ok" validate:"required"`
} // @name LLMConfigUpsertResponse

// --- Sandboxes ---

// SandboxCreateRequest is the body for POST /api/workspaces/{wid}/sandboxes.
// All fields except name are optional and fall back to workspace/server defaults.
type SandboxCreateRequest struct {
	Name        string                 `json:"name" validate:"required" example:"my-sandbox"`
	Type        string                 `json:"type" example:"opencode"`    // optional; default "opencode"
	CPU         *int                   `json:"cpu"`                        // optional; millicores, e.g. 500 or 2000
	Memory      *int64                 `json:"memory"`                     // optional; bytes, e.g. 536870912 (512Mi)
	IdleTimeout *int                   `json:"idle_timeout"`               // optional; seconds
	Metadata    map[string]interface{} `json:"metadata"`                   // optional; arbitrary key-value metadata
} // @name SandboxCreateRequest

// SandboxRenameRequest is the body for PATCH /api/sandboxes/{id}.
type SandboxRenameRequest struct {
	Name string `json:"name" validate:"required" example:"renamed-sandbox"`
} // @name SandboxRenameRequest

// SandboxLifecycleStatusResponse is the {"status": "pausing"} envelope returned
// by POST /api/sandboxes/{id}/pause and /resume. The status reflects the
// transition initiated, not the final state (those are async).
type SandboxLifecycleStatusResponse struct {
	Status string `json:"status" validate:"required" example:"pausing"`
} // @name SandboxLifecycleStatusResponse

// SandboxUsageSummary is one row in the per-provider/model breakdown returned
// by GET /api/sandboxes/{id}/usage. It mirrors llmproxy.UsageSummary.
type SandboxUsageSummary struct {
	Provider                 string `json:"provider" validate:"required" example:"anthropic"`
	Model                    string `json:"model" validate:"required" example:"claude-sonnet-4-6"`
	InputTokens              int64  `json:"input_tokens" validate:"required"`
	OutputTokens             int64  `json:"output_tokens" validate:"required"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens" validate:"required"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens" validate:"required"`
	RequestCount             int64  `json:"request_count" validate:"required"`
} // @name SandboxUsageSummary

// SandboxUsageResponse mirrors the body the LLM proxy returns from its
// /internal/usage?sandbox_id={id} endpoint, forwarded verbatim by
// GET /api/sandboxes/{id}/usage. usage is the per-provider/model breakdown;
// since is set only when the caller supplied a ?since= query param.
type SandboxUsageResponse struct {
	Usage []SandboxUsageSummary `json:"usage" validate:"required"`
	Since *string               `json:"since,omitempty" extensions:"x-nullable=true" example:"2026-01-01T00:00:00Z"`
} // @name SandboxUsage

// --- IM Channels ---

// IMChannelResponse mirrors workspace_im_channels rows returned by
// GET /api/workspaces/{id}/im/channels via the imbridge service.
// user_id is omitted when not set (weixin sets it; telegram/matrix do not).
type IMChannelResponse struct {
	ID             string `json:"id" validate:"required"`
	WorkspaceID    string `json:"workspace_id" validate:"required"`
	Provider       string `json:"provider" validate:"required" example:"weixin"`
	BotID          string `json:"bot_id" validate:"required"`
	UserID         string `json:"user_id,omitempty"`
	RequireMention bool   `json:"require_mention"`
	RoutingMode    string `json:"routing_mode" validate:"required" example:"codex"`
	BoundAt        string `json:"bound_at" validate:"required"`
} // @name IMChannel

// IMChannelListResponse is the {"channels": [...]} envelope returned by
// GET /api/workspaces/{id}/im/channels.
type IMChannelListResponse struct {
	Channels []IMChannelResponse `json:"channels" validate:"required"`
} // @name IMChannelListResponse

// IMChannelPatchRequest is the body for PATCH /api/workspaces/{id}/im/channels/{channelId}.
// Both fields are optional — only the supplied keys are applied.
// routing_mode must be "nanoclaw" or "codex".
type IMChannelPatchRequest struct {
	RequireMention *bool   `json:"require_mention" extensions:"x-nullable=true"`
	RoutingMode    *string `json:"routing_mode" extensions:"x-nullable=true" example:"codex"`
} // @name IMChannelPatchRequest

// IMChannelPatchResponse is the body returned on success by
// PATCH /api/workspaces/{id}/im/channels/{channelId}.
type IMChannelPatchResponse struct {
	Status string `json:"status" validate:"required" example:"updated"`
} // @name IMChannelPatchResponse

// IMWeixinQRStartResponse is returned by POST .../im/weixin/qr-start.
type IMWeixinQRStartResponse struct {
	QRCodeURL string `json:"qrcode_url" validate:"required"`
	Message   string `json:"message" validate:"required"`
} // @name IMWeixinQRStartResponse

// IMWeixinQRWaitResponse is the polymorphic response from POST .../im/weixin/qr-wait.
// status values: "wait", "scaned", "confirmed", "expired", "binded_redirect",
// "verify_code_blocked", "need_verifycode".
// qrcode_url is only present when status is "expired" (new QR code generated).
// bot_id and user_id are only present when status is "confirmed".
type IMWeixinQRWaitResponse struct {
	Connected  bool    `json:"connected"`
	Status     string  `json:"status" validate:"required" example:"wait"`
	Message    *string `json:"message,omitempty" extensions:"x-nullable=true"`
	QRCodeURL  *string `json:"qrcode_url,omitempty" extensions:"x-nullable=true"`
	BotID      *string `json:"bot_id,omitempty" extensions:"x-nullable=true"`
	UserID     *string `json:"user_id,omitempty" extensions:"x-nullable=true"`
} // @name IMWeixinQRWaitResponse

// IMTelegramConfigureRequest is the body for POST .../im/telegram/configure.
type IMTelegramConfigureRequest struct {
	BotToken string `json:"bot_token" validate:"required" example:"123456:ABC-DEF..."`
} // @name IMTelegramConfigureRequest

// IMTelegramConfigureResponse is returned by POST .../im/telegram/configure.
type IMTelegramConfigureResponse struct {
	Connected bool   `json:"connected" validate:"required"`
	BotID     string `json:"bot_id" validate:"required"`
} // @name IMTelegramConfigureResponse

// IMMatrixConfigureRequest is the body for POST .../im/matrix/configure.
// recovery_key is optional and only used to enable E2EE.
type IMMatrixConfigureRequest struct {
	HomeserverURL string `json:"homeserver_url" validate:"required" example:"https://matrix.example.com"`
	AccessToken   string `json:"access_token" validate:"required"`
	RecoveryKey   string `json:"recovery_key"` // optional, for E2EE
} // @name IMMatrixConfigureRequest

// IMMatrixConfigureResponse is returned by POST .../im/matrix/configure.
type IMMatrixConfigureResponse struct {
	Connected bool   `json:"connected" validate:"required"`
	BotID     string `json:"bot_id" validate:"required"`
} // @name IMMatrixConfigureResponse

// IMSandboxBindRequest is the body for POST /api/sandboxes/{id}/im/bind.
type IMSandboxBindRequest struct {
	ChannelID string `json:"channel_id" validate:"required"`
} // @name IMSandboxBindRequest

// IMSandboxBindResponse is returned on success by POST /api/sandboxes/{id}/im/bind.
type IMSandboxBindResponse struct {
	Status string `json:"status" validate:"required" example:"bound"`
} // @name IMSandboxBindResponse

// IMSandboxUnbindResponse is returned on success by DELETE /api/sandboxes/{id}/im/bind.
type IMSandboxUnbindResponse struct {
	Status string `json:"status" validate:"required" example:"unbound"`
} // @name IMSandboxUnbindResponse

// --- Codex Tokens ---

// CodexTokenMintRequest is the body for POST /api/codex/tokens.
// ttl_days is optional; defaults to 90 (range: 1–365).
type CodexTokenMintRequest struct {
	WorkspaceID string `json:"workspace_id" validate:"required"`
	Name        string `json:"name" validate:"required" example:"my mac"`
	TTLDays     int    `json:"ttl_days,omitempty" example:"90"`
} // @name CodexTokenMintRequest

// CodexTokenMintResponse is returned (201) by POST /api/codex/tokens.
// token is the full bearer value and is shown only once — store it securely.
type CodexTokenMintResponse struct {
	ID          string    `json:"id" validate:"required"`
	Token       string    `json:"token" validate:"required"`
	Name        string    `json:"name" validate:"required"`
	WorkspaceID string    `json:"workspace_id" validate:"required"`
	ExpiresAt   time.Time `json:"expires_at" validate:"required"`
	CreatedAt   time.Time `json:"created_at" validate:"required"`
} // @name CodexTokenMintResponse

// CodexTokenListItem is one entry returned by GET /api/codex/tokens.
// The raw token value is never included. last_used_at and revoked_at
// are null when not applicable.
type CodexTokenListItem struct {
	ID          string     `json:"id" validate:"required"`
	Name        string     `json:"name" validate:"required"`
	WorkspaceID string     `json:"workspace_id" validate:"required"`
	CreatedAt   time.Time  `json:"created_at" validate:"required"`
	ExpiresAt   time.Time  `json:"expires_at" validate:"required"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty" extensions:"x-nullable=true"`
	Revoked     bool       `json:"revoked"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty" extensions:"x-nullable=true"`
} // @name CodexTokenListItem

// --- Codex Browser Sessions ---

// CodexBrowserItem is one entry returned by GET /api/workspaces/{wid}/browsers.
// It mirrors RemoteExecutor so DeviceListPanel can render both without per-type
// branches. is_online is true when at least one open browser session exists for
// the underlying codex token. All session fields come from the latest session
// row (open if any, otherwise the most recent historical row).
type CodexBrowserItem struct {
	ID             string     `json:"id" validate:"required"`
	Name           string     `json:"name" validate:"required"`
	WorkspaceID    string     `json:"workspace_id" validate:"required"`
	CreatedAt      time.Time  `json:"created_at" validate:"required"`
	ExpiresAt      time.Time  `json:"expires_at" validate:"required"`
	IsOnline       bool       `json:"is_online" validate:"required"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	ClientIP       string     `json:"client_ip,omitempty"`
	ClientUA       string     `json:"client_ua,omitempty"`
	CodexVersion   string     `json:"codex_version,omitempty"`
	OS             string     `json:"os,omitempty"`
	ConnectedAt    *time.Time `json:"connected_at,omitempty"`
	DisconnectedAt *time.Time `json:"disconnected_at,omitempty"`
} // @name CodexBrowserItem
