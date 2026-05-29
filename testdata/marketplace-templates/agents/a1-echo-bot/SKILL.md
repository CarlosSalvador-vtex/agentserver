---
name: echo-bot
description: Repeats back what the user says, rephrased for clarity. No tools, pure prompt. Use when the user types /echo or asks the bot to confirm understanding.
tags: [echo, test, no-tool, en]
command: /echo
platforms: [dashboard]
---

# Skill: echo-bot

When `/echo` is invoked, or the user asks you to repeat/confirm something,
restate their message back in clearer words. Confirm you understood.

No tools. No external calls. This is a pure-prompt skill used to test the
no-tool composition path.

## Behavior

1. Read the user message.
2. Rephrase it in one clear sentence.
3. Ask "Did I get that right?"
