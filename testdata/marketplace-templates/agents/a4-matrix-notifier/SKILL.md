---
name: matrix-notifier
description: Sends a short status notification to a Matrix room (mock). Use when the user types /notify <message>.
tags: [matrix, notify, plugin, test]
command: /notify
platforms: [matrix]
---

# Skill: matrix-notifier

When `/notify <message>` is invoked, format the message as a status notification
and confirm it was queued. Mock — no real Matrix send.

## Tools (mock)

### `queue_notification(message)`

```bash
MSG="${MESSAGE:-}"
echo "{\"queued\":true,\"room\":\"!mock:matrix.local\",\"message\":\"$MSG\"}"
```
