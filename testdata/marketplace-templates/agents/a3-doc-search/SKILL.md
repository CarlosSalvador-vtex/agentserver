---
name: doc-search
description: Answers FAQ questions by searching a local references file. Use when the user asks a question that might be in the FAQ, or types /faq.
tags: [faq, search, multi-file, references, ptbr]
command: /faq
platforms: [whatsapp]
---

# Skill: doc-search

When `/faq` is invoked or the user asks a support question, **follow `prompt.md`
(same folder)** and search `references/faq.json` for the best match.

## Tools (mock, executed via bash inside the sandbox)

### `search_faq(query)`

```bash
Q="${QUERY:-}"
jq -c --arg q "$Q" '.[] | select(.q | ascii_downcase | contains($q | ascii_downcase))' \
  /opt/data/skills/personal/doc-search/references/faq.json
```

Variable: `QUERY` = user question substring. Returns matching FAQ entries.
