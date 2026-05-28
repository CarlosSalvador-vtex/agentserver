# Playground & Marketplace

Generated from [`openapi.yaml`](../openapi.yaml). Regenerate with `make api-docs`.

**35 endpoints** in this section.

### `GET /api/admin/marketplace/skills` {#op-get-api-admin-marketplace-skills}
List system skills (admin)
Lists skill drafts owned by the system workspace (shared visibility).

**Auth:** `ApiKeyAuth`


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.marketplaceSkillListResponse`](#schema-server-marketplaceskilllistresponse) |
| `500` | Internal Server Error | `object` |


### `POST /api/admin/marketplace/skills/import` {#op-post-api-admin-marketplace-skills-import}
Import marketplace skill (admin)
Creates a shared skill draft from export payload JSON.

**Auth:** `ApiKeyAuth`


**Request body**

Content-Type: `application/json`

Schema: [`server.skillExportPayload`](#schema-server-skillexportpayload)

```yaml
{
  description?: string
  files?: object
  name?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`server.playgroundSkillFull`](#schema-server-playgroundskillfull) |
| `400` | Bad Request | `object` |
| `500` | Internal Server Error | `object` |


### `DELETE /api/admin/marketplace/skills/{id}` {#op-delete-api-admin-marketplace-skills-id}
Archive system skill (admin)
Soft-deletes a system-owned skill draft.

**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` |  | — |
| `404` | Not Found | `object` |
| `500` | Internal Server Error | `object` |


### `GET /api/admin/marketplace/souls` {#op-get-api-admin-marketplace-souls}
List system souls (admin)
Lists soul drafts owned by the system workspace (shared visibility).

**Auth:** `ApiKeyAuth`


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.marketplaceSoulListResponse`](#schema-server-marketplacesoullistresponse) |
| `500` | Internal Server Error | `object` |


### `POST /api/admin/marketplace/souls/import` {#op-post-api-admin-marketplace-souls-import}
Import marketplace soul (admin)
Creates a shared soul draft from export payload JSON.

**Auth:** `ApiKeyAuth`


**Request body**

Content-Type: `application/json`

Schema: [`server.soulExportPayload`](#schema-server-soulexportpayload)

```yaml
{
  body?: string
  description?: string
  frontmatter?: object
  name?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`server.playgroundSoulFull`](#schema-server-playgroundsoulfull) |
| `400` | Bad Request | `object` |
| `500` | Internal Server Error | `object` |


### `DELETE /api/admin/marketplace/souls/{id}` {#op-delete-api-admin-marketplace-souls-id}
Archive system soul (admin)
Soft-deletes a system-owned soul draft.

**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Soul draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` |  | — |
| `404` | Not Found | `object` |
| `500` | Internal Server Error | `object` |


### `PATCH /api/admin/playground/skills/{id}/visibility` {#op-patch-api-admin-playground-skills-id-visibility}
Admin set skill visibility
Admin-only visibility patch for marketplace moderation.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |


**Request body**

Content-Type: `application/json`

```yaml
{ <key>: string }
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | `object` |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `404` | Not Found | `object` |


### `PATCH /api/admin/playground/souls/{id}/visibility` {#op-patch-api-admin-playground-souls-id-visibility}
Admin set soul visibility
Admin-only visibility patch for marketplace moderation.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Soul draft ID |


**Request body**

Content-Type: `application/json`

```yaml
{ <key>: string }
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | `object` |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `404` | Not Found | `object` |


### `GET /api/marketplace/skills` {#op-get-api-marketplace-skills}
List marketplace skills
Returns skill drafts shared to the marketplace catalog.

**Auth:** `ApiKeyAuth`


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.marketplaceSkillListResponse`](#schema-server-marketplaceskilllistresponse) |
| `401` | Unauthorized | `object` |


### `GET /api/marketplace/skills/{id}/export` {#op-get-api-marketplace-skills-id-export}
Export marketplace skill
Downloads full skill draft JSON (shared visibility only).

**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.skillExportPayload`](#schema-server-skillexportpayload) |
| `404` | Not Found | `object` |


### `POST /api/marketplace/skills/{id}/fork` {#op-post-api-marketplace-skills-id-fork}
Fork marketplace skill
Creates a private copy of a shared marketplace skill draft in the target workspace.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Marketplace skill draft ID |


**Request body**

Content-Type: `application/json`

Schema: [`server.marketplaceForkRequest`](#schema-server-marketplaceforkrequest)

```yaml
{
  workspace_id?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`server.playgroundSkillSummary`](#schema-server-playgroundskillsummary) |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |


### `GET /api/marketplace/skills/{id}/preview` {#op-get-api-marketplace-skills-id-preview}
Preview marketplace skill
Returns description and file excerpts for a shared skill (not the full draft).

**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.marketplaceSkillPreview`](#schema-server-marketplaceskillpreview) |
| `404` | Not Found | `object` |


### `GET /api/marketplace/souls` {#op-get-api-marketplace-souls}
List marketplace souls
Returns soul drafts shared to the marketplace catalog.

**Auth:** `ApiKeyAuth`


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.marketplaceSoulListResponse`](#schema-server-marketplacesoullistresponse) |
| `401` | Unauthorized | `object` |


### `GET /api/marketplace/souls/{id}/export` {#op-get-api-marketplace-souls-id-export}
Export marketplace soul
Downloads full soul draft JSON (shared visibility only).

**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Soul draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.soulExportPayload`](#schema-server-soulexportpayload) |
| `404` | Not Found | `object` |


### `POST /api/marketplace/souls/{id}/fork` {#op-post-api-marketplace-souls-id-fork}
Fork marketplace soul
Creates a private copy of a shared marketplace soul draft in the target workspace.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Marketplace soul draft ID |


**Request body**

Content-Type: `application/json`

Schema: [`server.marketplaceForkRequest`](#schema-server-marketplaceforkrequest)

```yaml
{
  workspace_id?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`server.playgroundSoulSummary`](#schema-server-playgroundsoulsummary) |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |


### `GET /api/marketplace/souls/{id}/preview` {#op-get-api-marketplace-souls-id-preview}
Preview marketplace soul
Returns description and body excerpt for a shared soul (not the full draft).

**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Soul draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.marketplaceSoulPreview`](#schema-server-marketplacesoulpreview) |
| `404` | Not Found | `object` |


### `GET /api/playground/skills` {#op-get-api-playground-skills}
List skill drafts
Returns skill drafts owned by the current user in the playground catalog.

**Auth:** `ApiKeyAuth`


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | drafts | [`server.playgroundSkillListResponse`](#schema-server-playgroundskilllistresponse) |
| `401` | Unauthorized | `object` |


### `POST /api/playground/skills` {#op-post-api-playground-skills}
Create skill draft
Creates a skill draft in the author's workspace playground catalog.

**Auth:** `ApiKeyAuth`


**Request body**

Content-Type: `application/json`

Schema: [`server.playgroundSkillFull`](#schema-server-playgroundskillfull)

```yaml
{
  can_set_visibility?: boolean
  description?: string
  files?: object
  id?: string
  name?: string
  promoted_commit?: string
  promoted_pr_state?: string
  promoted_pr_url?: string
  status?: string
  updated_at?: string
  visibility?: string
  workspace_id?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`server.playgroundSkillSummary`](#schema-server-playgroundskillsummary) |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |


### `GET /api/playground/skills/{id}` {#op-get-api-playground-skills-id}
Get skill draft
Returns full skill draft content for the author.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.playgroundSkillFull`](#schema-server-playgroundskillfull) |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |
| `404` | Not Found | `object` |


### `PATCH /api/playground/skills/{id}` {#op-patch-api-playground-skills-id}
Update skill draft
Partially updates skill draft fields for the author.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |


**Request body**

Content-Type: `application/json`

Schema: [`server.playgroundSkillFull`](#schema-server-playgroundskillfull)

```yaml
{
  can_set_visibility?: boolean
  description?: string
  files?: object
  id?: string
  name?: string
  promoted_commit?: string
  promoted_pr_state?: string
  promoted_pr_url?: string
  status?: string
  updated_at?: string
  visibility?: string
  workspace_id?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.playgroundSkillFull`](#schema-server-playgroundskillfull) |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |
| `404` | Not Found | `object` |


### `DELETE /api/playground/skills/{id}` {#op-delete-api-playground-skills-id}
Archive skill draft
Soft-deletes a skill draft owned by the author.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` | No Content | — |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |
| `404` | Not Found | `object` |


### `GET /api/playground/skills/{id}/audit` {#op-get-api-playground-skills-id-audit}
List skill draft audit events
Returns recent audit events for the skill draft, newest first.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |

**Query parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `limit` | `integer` | no | Max events (default 50) |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.playgroundAuditListResponse`](#schema-server-playgroundauditlistresponse) |
| `401` | Unauthorized | `object` |
| `404` | Not Found | `object` |


### `POST /api/playground/skills/{id}/dry-run` {#op-post-api-playground-skills-id-dry-run}
Dry-run skill draft prompt
Composes system prompt and optional LLM completion preview without persisting changes.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |


**Request body**

Content-Type: `application/json`

Schema: [`server.playgroundDryRunRequest`](#schema-server-playgrounddryrunrequest)

```yaml
{
  config?: object
  history?: []server.playgroundDryRunMessage
  model?: string
  soul_ref?: string
  user_message?: string
  workspace_id?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.playgroundDryRunResponse`](#schema-server-playgrounddryrunresponse) |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `404` | Not Found | `object` |


### `POST /api/playground/skills/{id}/promote` {#op-post-api-playground-skills-id-promote}
Promote skill draft to GitHub
Creates a branch and pull request from the draft (requires maintainer/owner and GITHUB_PROMOTE_TOKEN).

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.promoteDraftResponse`](#schema-server-promotedraftresponse) |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |
| `503` | Service Unavailable | `object` |


### `POST /api/playground/skills/{id}/test-sandbox` {#op-post-api-playground-skills-id-test-sandbox}
Test skill draft in sandbox
Runs the draft skill in a sandbox and returns sandbox metadata (strategy test).

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |


**Request body**

Content-Type: `application/json`

Schema: [`server.playgroundTestSandboxRequest`](#schema-server-playgroundtestsandboxrequest)

```yaml
{
  name?: string
  sandbox_type?: string
  soul_ref?: string
  workspace_id?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.playgroundTestSandboxResponse`](#schema-server-playgroundtestsandboxresponse) |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `404` | Not Found | `object` |
| `503` | Service Unavailable | `object` |


### `PATCH /api/playground/skills/{id}/visibility` {#op-patch-api-playground-skills-id-visibility}
Set skill draft visibility
Patches visibility to public or private for the draft author.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Skill draft ID |


**Request body**

Content-Type: `application/json`

```yaml
{ <key>: string }
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | `object` |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |
| `404` | Not Found | `object` |


### `GET /api/playground/souls` {#op-get-api-playground-souls}
List soul drafts
Returns soul drafts owned by the current user in the playground catalog.

**Auth:** `ApiKeyAuth`


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | drafts | [`server.playgroundSoulListResponse`](#schema-server-playgroundsoullistresponse) |
| `401` | Unauthorized | `object` |


### `POST /api/playground/souls` {#op-post-api-playground-souls}
Create soul draft
Creates a soul draft in the author's workspace playground catalog.

**Auth:** `ApiKeyAuth`


**Request body**

Content-Type: `application/json`

```yaml
{}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`server.playgroundSoulSummary`](#schema-server-playgroundsoulsummary) |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |


### `GET /api/playground/souls/{id}` {#op-get-api-playground-souls-id}
Get soul draft
Returns full soul draft content for the author.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Soul draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.playgroundSoulFull`](#schema-server-playgroundsoulfull) |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |
| `404` | Not Found | `object` |


### `PATCH /api/playground/souls/{id}` {#op-patch-api-playground-souls-id}
Update soul draft
Partially updates soul draft fields for the author.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Soul draft ID |


**Request body**

Content-Type: `application/json`

Schema: [`server.playgroundSoulFull`](#schema-server-playgroundsoulfull)

```yaml
{
  body?: string
  can_set_visibility?: boolean
  description?: string
  frontmatter?: object
  id?: string
  name?: string
  promoted_commit?: string
  promoted_pr_state?: string
  promoted_pr_url?: string
  schema_version?: string
  status?: string
  updated_at?: string
  visibility?: string
  workspace_id?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.playgroundSoulFull`](#schema-server-playgroundsoulfull) |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |
| `404` | Not Found | `object` |


### `DELETE /api/playground/souls/{id}` {#op-delete-api-playground-souls-id}
Archive soul draft
Soft-deletes a soul draft owned by the author.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Soul draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `204` | No Content | — |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |
| `404` | Not Found | `object` |


### `GET /api/playground/souls/{id}/audit` {#op-get-api-playground-souls-id-audit}
List soul draft audit events
Returns recent audit events for the soul draft, newest first.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Soul draft ID |

**Query parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `limit` | `integer` | no | Max events (default 50) |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.playgroundAuditListResponse`](#schema-server-playgroundauditlistresponse) |
| `401` | Unauthorized | `object` |
| `404` | Not Found | `object` |


### `POST /api/playground/souls/{id}/dry-run` {#op-post-api-playground-souls-id-dry-run}
Dry-run soul draft prompt
Composes system prompt and optional LLM completion preview without persisting changes.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Soul draft ID |


**Request body**

Content-Type: `application/json`

Schema: [`server.playgroundDryRunRequest`](#schema-server-playgrounddryrunrequest)

```yaml
{
  config?: object
  history?: []server.playgroundDryRunMessage
  model?: string
  soul_ref?: string
  user_message?: string
  workspace_id?: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.playgroundDryRunResponse`](#schema-server-playgrounddryrunresponse) |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `404` | Not Found | `object` |


### `POST /api/playground/souls/{id}/promote` {#op-post-api-playground-souls-id-promote}
Promote soul draft to GitHub
Creates a branch and pull request from the draft (requires maintainer/owner and GITHUB_PROMOTE_TOKEN).

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Soul draft ID |


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`server.promoteDraftResponse`](#schema-server-promotedraftresponse) |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |
| `503` | Service Unavailable | `object` |


### `PATCH /api/playground/souls/{id}/visibility` {#op-patch-api-playground-souls-id-visibility}
Set soul draft visibility
Patches visibility to public or private for the draft author.

**Auth:** `ApiKeyAuth`


**Path parameters**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `id` | `string` | yes | Soul draft ID |


**Request body**

Content-Type: `application/json`

```yaml
{ <key>: string }
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | `object` |
| `400` | Bad Request | `object` |
| `401` | Unauthorized | `object` |
| `403` | Forbidden | `object` |
| `404` | Not Found | `object` |

