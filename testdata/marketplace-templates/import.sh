#!/usr/bin/env bash
#
# import.sh — package the marketplace template fixtures into the
# SkillExportPayload / SoulExportPayload JSON shape, then either:
#   - write them to dist/ for manual upload via the admin UI "Import JSON"
#     button (pack mode), or
#   - POST them to the admin import endpoints (import mode).
#
# Personas (souls) live in personas/<id>/ as:
#     meta.json        {name, description}
#     frontmatter.json soul frontmatter object
#     body.md          system prompt / identity
#
# Agents (skills) live in agents/<id>/ as:
#     meta.json        {name, description}
#     <files...>       every other file becomes a key in `files` (relative path)
#
# Usage:
#   ./import.sh pack                  # write dist/*.json (for UI Import JSON)
#   ./import.sh import                # POST all fixtures to the API
#
# Env (import mode):
#   AGENTSERVER_URL     base URL, e.g. https://agent.cs.ac.cn (default http://localhost:8080)
#   AGENTSERVER_COOKIE  admin session cookie, e.g. "agentserver-token=..."
#
# Requires: jq, curl.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODE="${1:-pack}"
URL="${AGENTSERVER_URL:-http://localhost:8080}"
DIST="$ROOT/dist"

die() { echo "ERROR: $*" >&2; exit 1; }
command -v jq >/dev/null || die "jq not found"

# Build a SoulExportPayload JSON for a persona dir → stdout.
pack_soul() {
  local dir="$1"
  local name desc
  name="$(jq -r '.name' "$dir/meta.json")"
  desc="$(jq -r '.description // ""' "$dir/meta.json")"
  jq -n \
    --arg name "$name" \
    --arg description "$desc" \
    --slurpfile frontmatter "$dir/frontmatter.json" \
    --rawfile body "$dir/body.md" \
    '{name: $name, description: $description, frontmatter: $frontmatter[0], body: $body}'
}

# Build a SkillExportPayload JSON for an agent dir → stdout.
# `files` is a map of relative-path → file contents for every file except meta.json.
pack_skill() {
  local dir="${1%/}"  # strip any trailing slash so relative-path stripping works
  local name desc
  name="$(jq -r '.name' "$dir/meta.json")"
  desc="$(jq -r '.description // ""' "$dir/meta.json")"
  local files_json="{}"
  while IFS= read -r -d '' f; do
    local rel
    rel="${f#"$dir"/}"
    [ "$rel" = "meta.json" ] && continue
    files_json="$(jq -n \
      --argjson acc "$files_json" \
      --arg key "$rel" \
      --rawfile val "$f" \
      '$acc + {($key): $val}')"
  done < <(find "$dir" -type f -print0 | sort -z)
  jq -n \
    --arg name "$name" \
    --arg description "$desc" \
    --argjson files "$files_json" \
    '{name: $name, description: $description, files: $files}'
}

post() {
  local endpoint="$1" payload="$2" label="$3"
  [ -n "${AGENTSERVER_COOKIE:-}" ] || die "AGENTSERVER_COOKIE required for import mode"
  local code
  code="$(curl -sS -o /tmp/import-resp.json -w '%{http_code}' \
    -X POST "$URL$endpoint" \
    -H 'Content-Type: application/json' \
    -H "Cookie: $AGENTSERVER_COOKIE" \
    --data-binary "$payload")"
  if [ "$code" = "201" ]; then
    echo "  OK   $label"
  else
    echo "  FAIL $label (HTTP $code): $(cat /tmp/import-resp.json)" >&2
  fi
}

case "$MODE" in
  pack)
    rm -rf "$DIST"; mkdir -p "$DIST/personas" "$DIST/agents"
    for dir in "$ROOT"/personas/*/; do
      id="$(basename "$dir")"
      pack_soul "$dir" > "$DIST/personas/$id.json"
      echo "packed persona → dist/personas/$id.json"
    done
    for dir in "$ROOT"/agents/*/; do
      id="$(basename "$dir")"
      pack_skill "$dir" > "$DIST/agents/$id.json"
      echo "packed agent   → dist/agents/$id.json"
    done
    echo ""
    echo "Done. Upload any dist/**/*.json via Admin → Templates → Import JSON,"
    echo "or run './import.sh import' to POST them all."
    ;;
  import)
    echo "Importing personas (souls) → $URL"
    for dir in "$ROOT"/personas/*/; do
      post "/api/admin/marketplace/souls/import" "$(pack_soul "$dir")" "persona $(basename "$dir")"
    done
    echo "Importing agents (skills) → $URL"
    for dir in "$ROOT"/agents/*/; do
      post "/api/admin/marketplace/skills/import" "$(pack_skill "$dir")" "agent $(basename "$dir")"
    done
    echo "Done."
    ;;
  *)
    die "unknown mode '$MODE' (use 'pack' or 'import')"
    ;;
esac
