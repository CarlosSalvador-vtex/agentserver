#!/usr/bin/env python3
"""
Seed cobrança marketplace templates into the agentserver DB.

Usage (needs direct DB access — run from inside VPC or via bastion):
    DATABASE_URL="postgres://..." python3 scripts/seed-cobrana-templates.py

Or via kubectl (see scripts/seed-cobrana-job.yaml for in-cluster approach):
    DATABASE_URL=$(kubectl get secret agentserver-db-secret -n agentserver \
        -o jsonpath='{.data.database-url}' | base64 -d) \
    python3 scripts/seed-cobrana-templates.py

Requires: pip install psycopg2-binary
Idempotent: safe to run multiple times (skips if templates already exist).
"""

import json
import os
import sys

try:
    import psycopg2
except ImportError:
    print("ERROR: pip install psycopg2-binary", file=sys.stderr)
    sys.exit(1)

SKILL_DIR = os.path.join(
    os.path.dirname(__file__),
    "../deploy/helm/agentserver/skills/cobranca",
)


def read(rel: str) -> str:
    with open(os.path.join(SKILL_DIR, rel)) as f:
        return f.read()


def main() -> None:
    db_url = os.environ.get("DATABASE_URL")
    if not db_url:
        print("ERROR: DATABASE_URL not set", file=sys.stderr)
        sys.exit(1)

    prompt_md = read("prompt.md")
    index_mjs = read("index.mjs")
    leads_json = read("references/leads.json")
    package_json = read("package.json")
    plugin_json = read("openclaw.plugin.json")

    skill_files = json.dumps({
        "index.mjs": index_mjs,
        "prompt.md": prompt_md,
        "references/leads.json": leads_json,
        "package.json": package_json,
        "openclaw.plugin.json": plugin_json,
    })

    soul_frontmatter = json.dumps({"tone": "formal", "language": "pt-BR"})

    conn = psycopg2.connect(db_url)
    cur = conn.cursor()

    # Soul — "Agente de Cobrança" (workspace_id=NULL, visibility='shared')
    cur.execute(
        """
        INSERT INTO soul_drafts
            (name, description, author_user_id, workspace_id, visibility,
             frontmatter, body, status)
        SELECT %s, %s, NULL, NULL, 'shared', %s::jsonb, %s, 'draft'
        WHERE NOT EXISTS (
            SELECT 1 FROM soul_drafts
            WHERE name = %s
              AND workspace_id IS NULL
              AND author_user_id IS NULL
        )
        """,
        (
            "Agente de Cobrança",
            "Agente de cobrança empático e profissional em português brasileiro",
            soul_frontmatter,
            prompt_md,
            "Agente de Cobrança",
        ),
    )
    soul_inserted = cur.rowcount
    print(f"soul_drafts: {soul_inserted} row(s) inserted")

    # Skill — "Negociação de Dívida" (workspace_id=NULL, visibility='shared')
    cur.execute(
        """
        INSERT INTO skill_drafts
            (name, description, author_user_id, workspace_id, visibility,
             files, status)
        SELECT %s, %s, NULL, NULL, 'shared', %s::jsonb, 'draft'
        WHERE NOT EXISTS (
            SELECT 1 FROM skill_drafts
            WHERE name = %s
              AND workspace_id IS NULL
              AND author_user_id IS NULL
        )
        """,
        (
            "Negociação de Dívida",
            "Skill de negociação: proposta de pagamento, parcelamento e escalação",
            skill_files,
            "Negociação de Dívida",
        ),
    )
    skill_inserted = cur.rowcount
    print(f"skill_drafts: {skill_inserted} row(s) inserted")

    conn.commit()
    cur.close()
    conn.close()

    if soul_inserted == 0 and skill_inserted == 0:
        print("Templates already exist — nothing changed.")
    else:
        print("Done. Templates are now visible in the marketplace.")


if __name__ == "__main__":
    main()
