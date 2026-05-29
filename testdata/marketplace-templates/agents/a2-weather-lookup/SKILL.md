---
name: weather-lookup
description: Looks up mock weather for a city via a single bash tool. Use when the user asks about weather or types /weather <city>.
tags: [weather, mock, single-tool, test]
command: /weather
platforms: [telegram]
---

# Skill: weather-lookup

When `/weather <city>` is invoked, call the `get_weather` tool and report the
result in one short line. Mock data only — no real API.

## Tools (mock, executed via bash inside the sandbox)

### `get_weather(city)`

```bash
CITY="${CITY:-unknown}"
# Deterministic pseudo-weather from the city name length (mock).
TEMPS=(12 17 21 25 28 31)
IDX=$(( ${#CITY} % 6 ))
echo "{\"city\":\"$CITY\",\"temp_c\":${TEMPS[$IDX]},\"summary\":\"partly cloudy (mock)\"}"
```

Variable: `CITY` = city name string. Returns mock weather JSON.
