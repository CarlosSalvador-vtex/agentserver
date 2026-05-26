# WhatsApp Cloud (Meta) — Provider Integration

> **Status:** PR #7 — first WhatsApp provider in agentserver.
> Push-based via Meta's webhook (not poll-based like Telegram/WeChat).
> Text messages only in this MVP.

## What this PR ships

A 4th IM provider that mirrors the shape of Telegram/WeChat/Matrix but
inverts the inbound flow. Where existing providers long-poll their
upstream API, WhatsApp Cloud delivers messages via webhook — Meta
calls the agentserver, not the other way around.

Concrete additions:

- `internal/imbridge/whatsapp_provider.go` — `WhatsAppProvider` implementing
  `Provider` + `ConfigurableProvider`. `Poll()` is a no-op (5-minute
  backoff so the bridge poller goroutine stays idle); `Send()` hits the
  Graph API endpoint.
- `internal/imbridge/bridge.go` — new `Bridge.DispatchInbound(ctx, channelID, msg)`
  method that lets push-based providers feed messages into the same
  `forwardMessage` pipeline polling providers use.
- `internal/db/im_channels.go` — `FindIMChannelByProviderBot(provider, botID)`
  for webhook routing (Meta identifies the recipient by `phone_number_id`,
  not by our channel UUID). Also adds `DispatchInboundChannel(channelID)`
  used by the bridge to reconstruct credentials.
- `internal/imbridgesvc/handlers.go` — three new handlers:
  - `handleWorkspaceWhatsAppConfigure` — authenticated, creates the
    `workspace_im_channels` row with provider="whatsapp" and saves the
    Meta access token + base URL.
  - `handleWhatsAppWebhookVerify` — unauthenticated, responds to Meta's
    initial subscription handshake (`hub.challenge`).
  - `handleWhatsAppWebhookInbound` — unauthenticated, parses the
    webhook payload, looks up channels by `phone_number_id`, and
    dispatches via `Bridge.DispatchInbound`.
- `internal/imbridgesvc/server.go` — registers `/webhook/whatsapp` (GET + POST)
  outside the auth group, and `/api/workspaces/{id}/im/whatsapp/configure`
  inside it.
- `internal/server/server.go` + `im_routes.go` — proxies the same routes
  through to imbridge so split deployments still work.
- `cmd/imbridge/main.go` — registers `&imbridge.WhatsAppProvider{}` in the
  provider list.
- Helm: `whatsapp.webhookVerifyToken` value + env wiring on the imbridge
  pod (the verify-token GET handler runs there, not on agentserver).

## Credentials mapping

| Field in `workspace_im_channels` | Meta concept |
|---|---|
| `provider` | hard-coded `"whatsapp"` |
| `bot_id` | `phone_number_id` from the Meta Business dashboard |
| `bot_token` | long-lived access token (Bearer) |
| `base_url` | Graph API root — defaults to `https://graph.facebook.com/v18.0` |
| `user_id` | unused (Meta tokens are app-level, not user-bound) |

## Outbound flow

Sandbox replies → `imbridgesvc.handleNanoclawIMSend` /
`handleImbridgeDirectSend` → `provider.Send()`:

```go
POST {base_url}/{phone_number_id}/messages
Authorization: Bearer {access_token}
Content-Type: application/json

{
  "messaging_product": "whatsapp",
  "recipient_type":    "individual",
  "to":                "5527996073736",
  "type":              "text",
  "text":              {"body": "Olá!"}
}
```

Recipient `to` is the raw E.164 without `+`. The provider strips both
the `@wa` JID suffix and any leading `+` before posting.

## Inbound flow (webhook)

1. **Meta initial verification (one-time, manual)**

   In the Meta App Dashboard → Webhooks → Add Subscription:
   - **Callback URL**: `https://<your-domain>/webhook/whatsapp`
   - **Verify Token**: paste the same string set in
     `whatsapp.webhookVerifyToken` (helm) / `WHATSAPP_WEBHOOK_VERIFY_TOKEN`
     env var. Generate via `openssl rand -hex 32`.

   Meta hits `GET /webhook/whatsapp?hub.mode=subscribe&hub.verify_token=...&hub.challenge=XXX`.
   `handleWhatsAppWebhookVerify` compares `hub.verify_token` to the env
   var and returns the challenge value as plain text iff it matches.

2. **Inbound message delivery**

   Meta POSTs to the same URL with a payload like:

   ```json
   {
     "object": "whatsapp_business_account",
     "entry": [{
       "id": "WHATSAPP_BUSINESS_ACCOUNT_ID",
       "changes": [{
         "field": "messages",
         "value": {
           "messaging_product": "whatsapp",
           "metadata": {"phone_number_id": "PHONE_NUMBER_ID", "display_phone_number": "+5527..."},
           "contacts": [{"profile": {"name": "Carlos"}, "wa_id": "5527996073736"}],
           "messages": [{
             "from": "5527996073736",
             "id":   "wamid.HBgN...",
             "timestamp": "1716724567",
             "type": "text",
             "text": {"body": "Oi"}
           }]
         }
       }]
     }]
   }
   ```

   `handleWhatsAppWebhookInbound` iterates `entry[].changes[].value.messages[]`:
   - Skips non-text messages (MVP).
   - Looks up the channel via `FindIMChannelByProviderBot("whatsapp", phoneNumberID)`.
   - Builds `imbridge.InboundMessage{FromUserID: from+"@wa", SenderName, Text}`.
   - Calls `bridge.DispatchInbound(channelID, msg)` which routes through
     the existing `forwardMessage` → `forwardToNanoClaw` /
     `forwardToCodex` pipeline.

   We always return `200 OK` even on partial failures because Meta
   retries any non-2xx for up to 24h.

## Configure endpoint

```http
POST /api/workspaces/{wid}/im/whatsapp/configure
Content-Type: application/json
Cookie: <auth>

{
  "phone_number_id": "112233445566778",
  "access_token":    "EAAGZ...",
  "base_url":        "https://graph.facebook.com/v18.0"   # optional
}
```

Response:

```json
{
  "connected":  true,
  "channel_id": "uuid",
  "bot_id":     "112233445566778",
  "webhook_hint": "Configure your Meta App webhook URL to POST /webhook/whatsapp on this host; set hub.verify_token to the value of WHATSAPP_WEBHOOK_VERIFY_TOKEN."
}
```

No poller is started — WhatsApp inbound arrives via webhook only.

## Multi-tenant routing

Meta sends every event for a Business Account to a single webhook URL.
The handler uses `value.metadata.phone_number_id` to disambiguate which
workspace_im_channel row owns the message. The
`UNIQUE(workspace_id, provider, bot_id)` constraint on
`workspace_im_channels` allows the same WhatsApp number to be claimed
in different workspaces; if that happens the earliest-bound row wins
(`ORDER BY bound_at ASC LIMIT 1`).

## Limitations / next steps

This MVP cuts the following intentionally:

| Cut | Why | Where to add later |
|---|---|---|
| Media messages (image, voice, video, document) | Each media type needs a `GET /<media_id>` round-trip to fetch bytes + provider-side upload | extend `handleWhatsAppWebhookInbound` switch on `msg.Type`; add `WhatsAppProvider.SendImage` (implement `ImageSendProvider`) |
| Status updates (delivered, read, failed) | Useful for monitoring but no agent action depends on them today | parse `entry[].changes[].value.statuses` and persist to channel meta |
| `X-Hub-Signature-256` HMAC verification | Webhook hostname enforces TLS + IP allowlist via ALB if configured; signature adds defense in depth | wrap `handleWhatsAppWebhookInbound` body read in HMAC comparison against `app_secret` (new env var) |
| Reactions / template messages / interactive buttons | Same envelope but different schemas | per-type branches |
| Multi-tenant per-app secrets | One app secret per workspace lets each tenant rotate independently | move `WHATSAPP_WEBHOOK_VERIFY_TOKEN` from env to a `workspace_im_channel_meta` row keyed by `wa_verify_token` |
| Frontend modal | Today users configure via curl/Postman | add `WhatsAppConfigModal.tsx` mirroring `TelegramConfigModal.tsx` |
| Auto-bind to a sandbox | The channel still needs a sandbox; today the user calls `/im/channels/{id}/auto-bind` separately | extend `handleWorkspaceWhatsAppConfigure` to optionally call `BindSandboxChannels` / `BindSandboxToChannel` based on workspace strategy |

## Smoke test (dev EKS)

```bash
CTX="arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"
HOST="agentserver.analytics.vtex.com"

# 1. Verify the webhook GET handshake (simulating Meta's verification)
TOKEN=$(yq '.whatsapp.webhookVerifyToken' values-dev-eks.yaml)
curl -i "https://$HOST/webhook/whatsapp?hub.mode=subscribe&hub.verify_token=$TOKEN&hub.challenge=42"
# → HTTP/1.1 200 OK
# → 42

# 2. Configure a channel (use an authenticated session cookie)
curl -b cookies.txt -X POST "https://$HOST/api/workspaces/<wid>/im/whatsapp/configure" \
  -H 'Content-Type: application/json' \
  -d '{"phone_number_id":"112233445566778","access_token":"EAAGZ..."}'
# → {"connected":true,"channel_id":"<uuid>","bot_id":"112233445566778",...}

# 3. Auto-bind the channel to a sandbox (uses PR #4)
curl -b cookies.txt -X POST "https://$HOST/api/workspaces/<wid>/im/channels/<channel_id>/auto-bind"

# 4. Simulate an inbound message via the webhook
curl -i -X POST "https://$HOST/webhook/whatsapp" \
  -H 'Content-Type: application/json' \
  -d @- <<'JSON'
{
  "object": "whatsapp_business_account",
  "entry": [{
    "id": "TEST",
    "changes": [{
      "field": "messages",
      "value": {
        "messaging_product": "whatsapp",
        "metadata": {"phone_number_id": "112233445566778", "display_phone_number": "+5527..."},
        "contacts": [{"profile": {"name": "Tester"}, "wa_id": "5527996073736"}],
        "messages": [{
          "from": "5527996073736",
          "id":   "wamid.test",
          "timestamp": "1716724567",
          "type": "text",
          "text": {"body": "/cobranca"}
        }]
      }
    }]
  }]
}
JSON
# → HTTP/1.1 200 OK
# imbridge pod logs should show "DispatchInbound" + forward to sandbox

# 5. Verify in imbridge logs
kubectl --context "$CTX" logs -n agentserver deploy/agentserver-imbridge | grep -i whatsapp
```

## Why webhook lives in imbridge (not agentserver)

The webhook handler needs `*imbridge.Bridge.DispatchInbound`, which
holds the provider map and the in-memory channel-routing-mode override.
That state lives in the imbridge process. In split deployments,
agentserver reverse-proxies `/webhook/whatsapp` → imbridge so Meta
hits a single public URL; in in-process mode the same handler is
mounted directly. Both paths exercise the same code.
