package server

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
