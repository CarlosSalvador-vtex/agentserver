# Cursor Handoff — B09: "Choose a workspace" UI no apex

**Backlog:** B09 from [`../workspace-auth-pendencies.md`](../workspace-auth-pendencies.md)
**LOC:** ~130
**Tempo:** 1 dia
**Dependências:** B08 (cross-tenant redirect token) recomendado mas opcional

## Goal

Quando usuário loga no apex (`agentserver.<base>`) sem subdomínio, mostrar picker dos workspaces a que pertence e redirecionar pro subdomínio do escolhido (já logado).

## Why now

- Apex login deixa `active_workspace_id=NULL` → frontend hoje cai no flow PR #53 (selecionar workspace via API), mas user fica ainda no apex
- Subdomínio é o canonical UX — deveria sempre cair lá após escolher
- UX inconsistente entre "logar via subdomínio" e "logar via apex" — segundo pula etapa

## Required reading

- [`../workspace-session-auth.md`](../workspace-session-auth.md) — PR #53 flow
- [`B08-codex-auth-vs-cookie.md`](B08-codex-auth-vs-cookie.md) — cross-tenant redirect
- `web/src/components/Login.tsx` — login atual
- `web/src/App.tsx` — router

## Fluxo proposto

```
1. User abre agentserver.<base>/login (apex)
2. Login com email + senha (sem workspace_slug — apex)
3. Backend: session criada, active_workspace_id = NULL
4. Frontend detecta NULL → navega pra /choose-workspace
5. /choose-workspace busca GET /api/workspaces (workspaces do user)
6. Lista:
   - empresa-a (role: developer)   [Open]
   - empresa-b (role: owner)        [Open]
   - acme       (role: viewer)      [Open]
7. Click "Open" → frontend chama POST /api/auth/cross-tenant-redirect (B08)
8. Recebe signed redirect token
9. Redirect pra https://<slug>.<base>/api/auth/redirect-login?rt=<token>
10. Target subdomain consome token + cria cookie host-only + redirect /
```

Se B08 não estiver implementado, fallback simples:

```
7'. Click "Open" → redirect pra https://<slug>.<base>/login (user precisa re-logar)
```

## Files to touch

| Path | Mudança |
|---|---|
| `web/src/pages/ChooseWorkspace.tsx` (novo) | Picker UI |
| `web/src/App.tsx` | route `/choose-workspace` |
| `web/src/components/Login.tsx` | após login no apex, navega `/choose-workspace` se `active_workspace_id` é null |
| `web/src/lib/api.ts` | `listMyWorkspaces()` (já existe?), `crossTenantRedirect(slug)` |
| `internal/server/server.go` | endpoints `/api/auth/cross-tenant-redirect` + `/api/auth/redirect-login` (se B08) |

## ChooseWorkspace.tsx (skeleton)

```tsx
export function ChooseWorkspace() {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    api.listMyWorkspaces().then(setWorkspaces).finally(() => setLoading(false));
  }, []);

  if (loading) return <Spinner />;

  if (workspaces.length === 0) {
    return (
      <Card>
        <h2>No workspaces yet</h2>
        <p>You're signed in as {me.email} but don't belong to any workspace.</p>
        <Button onClick={() => setCreating(true)}>Create one</Button>
        {creating && <CreateWorkspaceModal onCreated={(ws) => switchTo(ws.slug)} />}
      </Card>
    );
  }

  if (workspaces.length === 1) {
    // Auto-redirect (no point in showing picker for 1 ws)
    switchTo(workspaces[0].slug);
    return <Spinner label="Redirecting…" />;
  }

  return (
    <Card>
      <h2>Choose a workspace</h2>
      <p>You belong to {workspaces.length} workspaces.</p>
      <ul>
        {workspaces.map(ws => (
          <li key={ws.id}>
            <div>{ws.name}</div>
            <code>{ws.slug}</code>
            <Badge>{ws.role}</Badge>
            <Button onClick={() => switchTo(ws.slug)}>Open</Button>
          </li>
        ))}
      </ul>
      <Button variant="ghost" onClick={() => setCreating(true)}>+ Create new workspace</Button>
    </Card>
  );
}

async function switchTo(slug: string) {
  // Caminho C (B08): cross-tenant redirect
  try {
    const { redirect_url } = await api.crossTenantRedirect(slug);
    window.location.href = redirect_url;
  } catch {
    // Fallback simples: re-login no subdomínio
    window.location.href = `https://${slug}.${window.location.hostname}/login`;
  }
}
```

## Login.tsx adjustment

```tsx
async function onSubmit() {
  const slug = extractWorkspaceSlug(window.location.hostname); // já existe
  await login(email, password, slug);
  const me = await api.getMe();
  if (me.active_workspace_id) {
    navigate(`/w/${me.active_workspace_id}`);
  } else {
    navigate('/choose-workspace');
  }
}
```

## Acceptance criteria

- [ ] Login no apex sem workspace_slug → redirect pra `/choose-workspace`
- [ ] `/choose-workspace` mostra todos workspaces do user com role + slug
- [ ] Click "Open" leva pro subdomínio correto, já logado (B08) ou pra re-login (fallback)
- [ ] User com 0 workspaces vê empty state + botão "Create"
- [ ] User com 1 workspace auto-redireciona (não mostra picker)
- [ ] Tests do componente (RTL)
- [ ] Backend test (se B08 implementado)

## Test plan

```bash
# Frontend
cd web && pnpm test ChooseWorkspace

# Browser smoke DEV
# 1. Logout total
# 2. Abrir agentserver.<base>/login
# 3. Login com user que tem 3 workspaces
# 4. Esperado: picker /choose-workspace com 3 cards
# 5. Click "Open" empresa-a → redirect → cookie session em empresa-a.<base>
```

## Decisões pendentes

| Decisão | Recomendação |
|---|---|
| Auto-redirect com 1 workspace | **Sim** — UX sem fricção |
| Ordenação da lista | mais recente acessado primeiro; depois alfabético |
| Salvar última escolha pra default próximo login | LocalStorage v1 — vide R2 (cookie cross-tenant) |
| Permitir "Create workspace" inline | **Sim** (modal) — escapa do dead-end "no workspaces" |
| User não-admin pode criar workspace | Sim — qualquer user autenticado vira owner do que criou |

## Anti-patterns

- ❌ Trapped at "no workspaces" sem affordance de criar
- ❌ Listar workspaces dos quais user NÃO é membro (privacy leak)
- ❌ Permitir abrir workspace soft-deleted (filtra deleted_at IS NULL)

## Out of scope

- Drag-to-reorder favoritos (v2)
- Workspace icons/logos (v2)
- Recent activity per workspace (v2)

## Definition of done

- PR mergeado
- Tests passam
- DEV smoke: 3 workspaces, picker funciona, redirect lands no subdomínio com session ativa
