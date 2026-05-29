---
name: 天気エージェント
description: 都市の天気を調べます（モック）。ユーザーが /tenki <都市> と入力したときに使用します。
tags: [cjk, edge-case, weather, mock]
command: /tenki
platforms: [dashboard]
---

# Skill: 天気エージェント

`/tenki <都市>` が呼ばれたら、`get_tenki` を呼び出して結果を返します。
モックデータのみ。CJK名のサニタイズ（#126）をテストするためのスキルです。

## Tools (mock)

### `get_tenki(都市)`

```bash
CITY="${CITY:-tokyo}"
echo "{\"city\":\"$CITY\",\"temp_c\":22,\"summary\":\"晴れ (mock)\"}"
```
